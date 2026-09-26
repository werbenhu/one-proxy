// Package router maps public channels to provider accounts, then applies the
// configured priority or weighted round-robin policy with failover.
//
// 设计约束（plan.md §5.3）：
//   - 候选 = 渠道绑定 ∧ 提供商 Enabled ∧ 状态可用（cooling 过期自动恢复）
//   - priority 固定取最高优先级；round-robin 按权重轮询
//   - 冷却内存态，重启清零
//   - 全不可用：不对 auth-failed 强打，聚合人话错误返回
package router

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
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
	rrMu     sync.Mutex
	rrNext   map[string]int

	// onUsage 用量埋点回调（proxy 注入；可为 nil）。成功与失败都上报，
	// 流式在事件流结束后回调。
	onUsage func(RequestInfo)
}

// RequestInfo 一次请求的路由结果信息（usage 埋点用）。
type RequestInfo struct {
	ChannelID      string
	ChannelName    string
	ProviderID     string
	ModelRequested string // 客户端请求的对外模型名
	ModelUpstream  string // 实际转发给上游的模型名
	Usage          anthropic.Usage
	Status         int
	LatencyMs      int64
	Error          string
}

// SetUsageHook 注入用量回调。
func (r *Router) SetUsageHook(fn func(RequestInfo)) { r.onUsage = fn }

// RecordUsage 供原始转发路径（Responses 入口）手动记账。
func (r *Router) RecordUsage(info RequestInfo) { r.emitUsage(info) }

func (r *Router) emitUsage(info RequestInfo) {
	if r.onUsage != nil {
		r.onUsage(info)
	}
}

func New(store *config.Store, registry *provider.Registry) *Router {
	return &Router{store: store, registry: registry, now: time.Now, rrNext: map[string]int{}}
}

// Target 一次路由决策的结果：渠道 + 重写后的上游模型名。
type Target struct {
	Channel       config.Channel
	Provider      config.ProviderAccount
	Adapter       provider.Adapter
	UpstreamModel string
}

// Resolve 按对外模型名匹配渠道并返回候选列表（按 Priority 降序）。
// 具名渠道优先（允许重名，按列表顺序首个启用的命中）；
// 都没有命中时退到通配渠道（对外模型名留空）。
// 强制指定渠道/提供商走 URL 路径前缀（/<id>/v1/...），见 ResolveDirect。
func (r *Router) Resolve(model string) ([]Target, error) {
	cfg := r.store.Get()
	for _, ch := range cfg.Channels {
		if ch.Enabled && ch.Model == model {
			return r.resolveChannel(cfg, ch, model)
		}
	}
	for _, ch := range cfg.Channels {
		if ch.Enabled && strings.TrimSpace(ch.Model) == "" {
			return r.resolveChannel(cfg, ch, model)
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrModelNotDeclared, model)
}

// ResolveDirect 强制指定渠道或提供商（客户端 BASE_URL 路径前缀 /<id>/），
// model 作为请求模型名：渠道走正常的目标/策略/透传逻辑；提供商为单候选透传。
func (r *Router) ResolveDirect(id, model string) ([]Target, error) {
	cfg := r.store.Get()
	if ch, found := cfg.Channel(id); found {
		if !ch.Enabled {
			return nil, fmt.Errorf("渠道 %s 未启用", id)
		}
		candidates, err := r.resolveChannel(cfg, ch, model)
		if err != nil {
			return nil, fmt.Errorf("渠道 %s 没有可用提供商", id)
		}
		return candidates, nil
	}
	if p, found := cfg.Provider(id); found && p.Enabled {
		adapter, registered := r.registry.Get(p.ID)
		if !registered {
			return nil, fmt.Errorf("提供商 %s 适配器未注册", p.ID)
		}
		return []Target{{Channel: config.Channel{ID: id, Name: p.Name, Model: model, Enabled: true}, Provider: p, Adapter: adapter, UpstreamModel: model}}, nil
	}
	return nil, fmt.Errorf("直连目标 %s 不存在或未启用", id)
}

func (r *Router) resolveChannel(cfg config.Config, ch config.Channel, model string) ([]Target, error) {
	candidates := r.targetsForChannel(cfg, ch)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: %s（渠道没有已启用的提供商）", ErrModelNotDeclared, model)
	}
	// 上游模型留空 = 透传：把客户端请求的模型名原样发给上游
	for i := range candidates {
		if strings.TrimSpace(candidates[i].UpstreamModel) == "" {
			candidates[i].UpstreamModel = model
		}
	}
	if ch.Strategy == config.StrategyRoundRobin {
		return r.rotate(ch.ID, candidates), nil
	}
	return candidates, nil
}

func (r *Router) targetsForChannel(cfg config.Config, ch config.Channel) []Target {
	var candidates []Target
	for _, binding := range ch.Targets {
		if !binding.Enabled {
			continue
		}
		p, ok := cfg.Provider(binding.ProviderID)
		if !ok || !p.Enabled {
			continue
		}
		adapter, ok := r.registry.Get(p.ID)
		if !ok {
			continue
		}
		candidates = append(candidates, Target{Channel: ch, Provider: p, Adapter: adapter, UpstreamModel: binding.UpstreamModel})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return targetPriority(ch, candidates[i].Provider.ID) > targetPriority(ch, candidates[j].Provider.ID)
	})
	return candidates
}

func targetPriority(ch config.Channel, providerID string) int {
	for _, target := range ch.Targets {
		if target.ProviderID == providerID {
			return target.Priority
		}
	}
	return 0
}

func targetWeight(ch config.Channel, providerID string) int {
	for _, target := range ch.Targets {
		if target.ProviderID == providerID {
			if target.Weight > 0 {
				return target.Weight
			}
			return 1
		}
	}
	return 1
}

func (r *Router) rotate(channelID string, candidates []Target) []Target {
	var ring []int
	for i, target := range candidates {
		for n := 0; n < targetWeight(target.Channel, target.Provider.ID); n++ {
			ring = append(ring, i)
		}
	}
	if len(ring) == 0 {
		return candidates
	}
	r.rrMu.Lock()
	start := r.rrNext[channelID] % len(ring)
	r.rrNext[channelID] = (start + 1) % len(ring)
	r.rrMu.Unlock()
	out, seen := make([]Target, 0, len(candidates)), map[int]bool{}
	for n := 0; n < len(ring); n++ {
		i := ring[(start+n)%len(ring)]
		if !seen[i] {
			out = append(out, candidates[i])
			seen[i] = true
		}
	}
	for i := range candidates {
		if !seen[i] {
			out = append(out, candidates[i])
		}
	}
	return out
}

// pickAvailable 过滤不可用候选；返回可用列表与不可用原因（聚合错误用）。
func (r *Router) pickAvailable(candidates []Target) ([]Target, []string) {
	now := r.now()
	var available []Target
	var reasons []string
	for _, t := range candidates {
		state := r.registry.State(t.Provider.ID)
		switch {
		case state.Status == provider.StatusAuthFailed:
			reasons = append(reasons, fmt.Sprintf("%s 鉴权失败（%s）", t.Provider.Name, state.FailReason))
		case state.Status == provider.StatusCooling && !now.After(state.CoolingUntil):
			reasons = append(reasons, fmt.Sprintf("%s 冷却至 %s", t.Provider.Name, state.CoolingUntil.Format("15:04:05")))
		default:
			available = append(available, t)
		}
	}
	return available, reasons
}

// Invoke 按优先级依次尝试，失败按错误类别决定冷却/标记并切换。
func (r *Router) Invoke(ctx context.Context, req *anthropic.Request, requestedModel string) (*anthropic.Response, error) {
	candidates, err := r.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	return r.invoke(ctx, req, requestedModel, candidates)
}

// InvokeDirect 同 Invoke，但强制走 id 指定的渠道/提供商（URL 路径前缀）。
func (r *Router) InvokeDirect(ctx context.Context, req *anthropic.Request, requestedModel, id string) (*anthropic.Response, error) {
	candidates, err := r.ResolveDirect(id, req.Model)
	if err != nil {
		return nil, err
	}
	return r.invoke(ctx, req, requestedModel, candidates)
}

func (r *Router) invoke(ctx context.Context, req *anthropic.Request, requestedModel string, candidates []Target) (*anthropic.Response, error) {
	available, reasons := r.pickAvailable(candidates)
	if len(available) == 0 {
		return nil, &ErrNoCandidates{Reasons: reasons}
	}
	var lastErr error
	for _, t := range available {
		attempt := *req
		attempt.Model = t.UpstreamModel
		start := r.now()
		resp, err := t.Adapter.Invoke(ctx, &attempt)
		latency := r.now().Sub(start).Milliseconds()
		if err == nil {
			r.applySuccess(t)
			r.emitUsage(RequestInfo{
				ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
				ModelRequested: requestedModel, ModelUpstream: t.UpstreamModel,
				Usage: resp.Usage, Status: 200, LatencyMs: latency,
			})
			return resp, nil
		}
		lastErr = err
		r.applyFailure(t, err)
		r.emitUsage(RequestInfo{
			ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
			ModelRequested: requestedModel, ModelUpstream: t.UpstreamModel,
			Status: statusCodeOf(err), LatencyMs: latency, Error: err.Error(),
		})
		if !provider.MaySwitch(err) {
			return nil, err
		}
		reasons = append(reasons, fmt.Sprintf("%s：%s", t.Provider.Name, err.Error()))
	}
	if len(candidates) == 1 {
		// 仅一条候选渠道：直接返回原始错误（聚合无额外价值）
		return nil, lastErr
	}
	return nil, &ErrNoCandidates{Reasons: reasons}
}

func statusCodeOf(err error) int {
	var ue *provider.UpstreamError
	if errors.As(err, &ue) {
		return ue.StatusCode
	}
	return 0
}

// Stream 同 Invoke 的流式版本：返回事件 channel。
// 注意：流式场景上游错误多在 Stream() 调用时（首事件前）暴露；
// 事件流中途的错误以 error 事件透传，不再切换（plan.md：首字节后不重试）。
// 返回的 channel 关闭时通过 done 通知（usage 埋点在流结束后上报）。
func (r *Router) Stream(ctx context.Context, req *anthropic.Request, requestedModel string) (<-chan anthropic.Event, error) {
	candidates, err := r.Resolve(req.Model)
	if err != nil {
		return nil, err
	}
	return r.stream(ctx, req, requestedModel, candidates)
}

// StreamDirect 同 Stream，但强制走 id 指定的渠道/提供商（URL 路径前缀）。
func (r *Router) StreamDirect(ctx context.Context, req *anthropic.Request, requestedModel, id string) (<-chan anthropic.Event, error) {
	candidates, err := r.ResolveDirect(id, req.Model)
	if err != nil {
		return nil, err
	}
	return r.stream(ctx, req, requestedModel, candidates)
}

func (r *Router) stream(ctx context.Context, req *anthropic.Request, requestedModel string, candidates []Target) (<-chan anthropic.Event, error) {
	available, reasons := r.pickAvailable(candidates)
	if len(available) == 0 {
		return nil, &ErrNoCandidates{Reasons: reasons}
	}
	var lastErr error
	for _, t := range available {
		attempt := *req
		attempt.Model = t.UpstreamModel
		start := r.now()
		events, err := t.Adapter.Stream(ctx, &attempt)
		if err != nil {
			latency := r.now().Sub(start).Milliseconds()
			lastErr = err
			r.applyFailure(t, err)
			r.emitUsage(RequestInfo{
				ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
				ModelRequested: requestedModel, ModelUpstream: t.UpstreamModel,
				Status: statusCodeOf(err), LatencyMs: latency, Error: err.Error(),
			})
			if !provider.MaySwitch(err) {
				return nil, err
			}
			reasons = append(reasons, fmt.Sprintf("%s：%s", t.Provider.Name, err.Error()))
			continue
		}
		r.applySuccess(t)
		// 包装事件流：累计 usage，结束后上报
		wrapped := make(chan anthropic.Event, 16)
		go func(t Target, requested, upstream string, started time.Time) {
			defer close(wrapped)
			u := anthropic.Usage{}
			for ev := range events {
				u.MergeEvent(ev)
				wrapped <- ev
			}
			r.emitUsage(RequestInfo{
				ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
				ModelRequested: requested, ModelUpstream: upstream,
				Usage: u, Status: 200, LatencyMs: time.Since(started).Milliseconds(),
			})
		}(t, requestedModel, t.UpstreamModel, start)
		return wrapped, nil
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
		r.registry.SetCooling(t.Provider.ID, until, "429 配额/限速")
	case provider.ErrKindRateLimit:
		r.registry.SetCooling(t.Provider.ID, r.now().Add(time.Minute), "限速")
	case provider.ErrKindAuth:
		r.registry.SetAuthFailed(t.Provider.ID, "上游 401/403")
	case provider.ErrKindUpstream, provider.ErrKindBadRequest, provider.ErrKindNone:
		// 5xx/网络/400 不改状态
	}
}

// applySuccess 恢复渠道状态（成功调用后清除冷却标记）。
func (r *Router) applySuccess(t Target) {
	r.registry.SetOK(t.Provider.ID)
}
