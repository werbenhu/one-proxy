// Package router 渠道路由：模型名 → 候选渠道，优先级主备 + 故障切换。
//
// 设计约束（plan.md §5.3）：
//   - 候选 = Models 声明 ∧ Enabled ∧ 状态可用（cooling 过期自动恢复）
//   - 固定取最高可用优先级（粘性，prompt cache 友好），无权重随机
//   - 冷却内存态，重启清零
//   - 全不可用：不对 auth-failed 强打，聚合人话错误返回
package router

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

const (
	cooldownBase = 5 * time.Minute
	cooldownMax  = 30 * time.Minute
)

// ErrModelNotDeclared 没有任何渠道声明请求的模型。
var ErrModelNotDeclared = errors.New("没有渠道声明该模型")

// ErrNoCandidates 全部候选不可用（携带各渠道不可用原因）。
type ErrNoCandidates struct {
	Reasons []string
}

func (e *ErrNoCandidates) Error() string {
	return "没有可用渠道：" + strings.Join(e.Reasons, "；")
}

// Router 无自身状态；渠道状态在 Registry（内存态）。
type Router struct {
	store    *config.Store
	registry *provider.Registry
	now      func() time.Time
}

func New(store *config.Store, registry *provider.Registry) *Router {
	return &Router{store: store, registry: registry, now: time.Now}
}

// Target 一次路由决策的结果：渠道 + 重写后的上游模型名。
type Target struct {
	Channel       config.Channel
	Adapter       provider.Adapter
	UpstreamModel string
}

// Resolve 解析模型名（含 ch-<id>/<model> 直连语法）并返回候选列表
// （按 Priority 降序；直连时为单元素）。
func (r *Router) Resolve(model string) ([]Target, error) {
	cfg := r.store.Get()
	if id, rest, ok := splitDirect(model); ok {
		ch, found := cfg.Channel(id)
		if !found || !ch.Enabled {
			return nil, fmt.Errorf("直连渠道 %s 不存在或未启用", id)
		}
		adapter, ok := r.registry.Get(ch.ID)
		if !ok {
			return nil, fmt.Errorf("渠道 %s 适配器未注册", ch.ID)
		}
		return []Target{{Channel: ch, Adapter: adapter, UpstreamModel: rest}}, nil
	}
	var candidates []Target
	for _, ch := range cfg.Channels {
		if !ch.Enabled {
			continue
		}
		declared := false
		for _, m := range ch.Models {
			if m == model {
				declared = true
				break
			}
		}
		if !declared {
			continue
		}
		adapter, ok := r.registry.Get(ch.ID)
		if !ok {
			continue
		}
		upstream := model
		if mapped, ok := ch.ModelMapping[model]; ok {
			upstream = mapped
		}
		candidates = append(candidates, Target{Channel: ch, Adapter: adapter, UpstreamModel: upstream})
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrModelNotDeclared, model)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Channel.Priority > candidates[j].Channel.Priority
	})
	return candidates, nil
}

// splitDirect 解析 ch-<id>/<model>；仅 ch- 前缀（避免拆 OpenRouter org/model）。
func splitDirect(model string) (id, rest string, ok bool) {
	if !strings.HasPrefix(model, "ch-") {
		return "", "", false
	}
	idx := strings.Index(model, "/")
	if idx <= 0 {
		return "", "", false
	}
	return model[:idx], model[idx+1:], true
}

// pickAvailable 过滤不可用候选；返回可用列表与不可用原因（聚合错误用）。
func (r *Router) pickAvailable(candidates []Target) ([]Target, []string) {
	now := r.now()
	var available []Target
	var reasons []string
	for _, t := range candidates {
		state := r.registry.State(t.Channel.ID)
		switch {
		case state.Status == provider.StatusAuthFailed:
			reasons = append(reasons, fmt.Sprintf("%s 鉴权失败（%s）", t.Channel.Name, state.FailReason))
		case state.Status == provider.StatusCooling && !now.After(state.CoolingUntil):
			reasons = append(reasons, fmt.Sprintf("%s 冷却至 %s", t.Channel.Name, state.CoolingUntil.Format("15:04:05")))
		default:
			available = append(available, t)
		}
	}
	return available, reasons
}

// Invoke 按优先级依次尝试，失败按错误类别决定冷却/标记并切换。
func (r *Router) Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	candidates, err := r.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	available, reasons := r.pickAvailable(candidates)
	if len(available) == 0 {
		return nil, &ErrNoCandidates{Reasons: reasons}
	}
	var lastErr error
	for _, t := range available {
		attempt := *req
		attempt.Model = t.UpstreamModel
		resp, err := t.Adapter.Invoke(ctx, &attempt)
		if err == nil {
			r.applySuccess(t)
			return resp, nil
		}
		lastErr = err
		r.applyFailure(t, err)
		if !provider.MaySwitch(err) {
			return nil, err
		}
		reasons = append(reasons, fmt.Sprintf("%s：%s", t.Channel.Name, err.Error()))
	}
	if len(candidates) == 1 {
		// 仅一条候选渠道：直接返回原始错误（聚合无额外价值）
		return nil, lastErr
	}
	return nil, &ErrNoCandidates{Reasons: reasons}
}

// Stream 同 Invoke 的流式版本：返回事件 channel。
// 注意：流式场景上游错误多在 Stream() 调用时（首事件前）暴露；
// 事件流中途的错误以 error 事件透传，不再切换（plan.md：首字节后不重试）。
func (r *Router) Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error) {
	candidates, err := r.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	available, reasons := r.pickAvailable(candidates)
	if len(available) == 0 {
		return nil, &ErrNoCandidates{Reasons: reasons}
	}
	var lastErr error
	for _, t := range available {
		attempt := *req
		attempt.Model = t.UpstreamModel
		events, err := t.Adapter.Stream(ctx, &attempt)
		if err == nil {
			r.applySuccess(t)
			return events, nil
		}
		lastErr = err
		r.applyFailure(t, err)
		if !provider.MaySwitch(err) {
			return nil, err
		}
		reasons = append(reasons, fmt.Sprintf("%s：%s", t.Channel.Name, err.Error()))
	}
	if len(candidates) == 1 {
		return nil, lastErr
	}
	return nil, &ErrNoCandidates{Reasons: reasons}
}

// applyFailure 根据错误类别更新渠道状态（内存态冷却，指数退避封顶）。
func (r *Router) applyFailure(t Target, err error) {
	kind := provider.KindOf(err)
	switch kind {
	case provider.ErrKindQuota:
		until := r.now().Add(cooldownBase)
		r.registry.SetCooling(t.Channel.ID, until, "429 配额/限速")
	case provider.ErrKindRateLimit:
		r.registry.SetCooling(t.Channel.ID, r.now().Add(time.Minute), "限速")
	case provider.ErrKindAuth:
		r.registry.SetAuthFailed(t.Channel.ID, "上游 401/403")
	case provider.ErrKindUpstream, provider.ErrKindBadRequest, provider.ErrKindNone:
		// 5xx/网络/400 不改状态
	}
}

// applySuccess 恢复渠道状态（成功调用后清除冷却标记）。
func (r *Router) applySuccess(t Target) {
	r.registry.SetOK(t.Channel.ID)
}
