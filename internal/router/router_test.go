package router

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// fakeAdapter 可编程假适配器：按脚本返回结果。
type fakeAdapter struct {
	invokeErr  error
	streamErr  error
	invokeResp *anthropic.Response
	modelsSeen []string
}

func (f *fakeAdapter) Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	f.modelsSeen = append(f.modelsSeen, req.Model)
	if f.invokeErr != nil {
		return nil, f.invokeErr
	}
	if f.invokeResp != nil {
		return f.invokeResp, nil
	}
	return &anthropic.Response{ID: "m", Type: "message", Role: "assistant", Content: []anthropic.ContentBlock{{Type: "text", Text: "ok"}}}, nil
}

func (f *fakeAdapter) Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error) {
	f.modelsSeen = append(f.modelsSeen, req.Model)
	if f.streamErr != nil {
		return nil, f.streamErr
	}
	ch := make(chan anthropic.Event, 1)
	ch <- anthropic.Event{Type: "message_stop", Raw: json.RawMessage(`{"type":"message_stop"}`)}
	close(ch)
	return ch, nil
}

func (f *fakeAdapter) Models(ctx context.Context) ([]provider.ModelInfo, error) { return nil, nil }
func (f *fakeAdapter) NormalizeError(err error) provider.ErrorKind              { return provider.KindOf(err) }

func newTestRouter(t *testing.T, channels []config.Channel, adapters map[string]*fakeAdapter) (*Router, *provider.Registry) {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Channels = channels
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	for id, a := range adapters {
		registry.Register(id, a)
	}
	return New(store, registry), registry
}

func twoKimiChannels() ([]config.Channel, map[string]*fakeAdapter) {
	channels := []config.Channel{
		{ID: "ch-a", Name: "Kimi套餐A", Type: config.TypeAnthropicCompat, BaseURL: "https://x", APIKey: "k1",
			Models: []string{"claude-sonnet-4-6"}, ModelMapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}, Priority: 10, Enabled: true},
		{ID: "ch-b", Name: "Kimi套餐B", Type: config.TypeAnthropicCompat, BaseURL: "https://x", APIKey: "k2",
			Models: []string{"claude-sonnet-4-6"}, ModelMapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}, Priority: 5, Enabled: true},
	}
	adapters := map[string]*fakeAdapter{
		"ch-a": {},
		"ch-b": {},
	}
	return channels, adapters
}

// 优先级主备：正常固定走 A。
func TestPrioritySticky(t *testing.T) {
	channels, adapters := twoKimiChannels()
	r, _ := newTestRouter(t, channels, adapters)
	for i := 0; i < 3; i++ {
		resp, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8})
		if err != nil {
			t.Fatal(err)
		}
		if resp.ID != "m" {
			t.Fatal("响应错误")
		}
	}
	if got := adapters["ch-a"].modelsSeen; len(got) != 3 {
		t.Fatalf("应固定走 A: %v", got)
	}
	if got := adapters["ch-b"].modelsSeen; len(got) != 0 {
		t.Fatalf("B 不应被调用: %v", got)
	}
	if got := adapters["ch-a"].modelsSeen[0]; got != "kimi-k3" {
		t.Fatalf("model 映射失败: %s", got)
	}
}

// 429 → A 冷却 → 切 B；A 冷却期继续走 B。
func TestQuotaFailover(t *testing.T) {
	channels, adapters := twoKimiChannels()
	adapters["ch-a"].invokeErr = &provider.UpstreamError{Kind: provider.ErrKindQuota, StatusCode: 429, Body: "quota"}
	r, registry := newTestRouter(t, channels, adapters)

	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if got := adapters["ch-b"].modelsSeen; len(got) != 1 {
		t.Fatalf("应切到 B: %v", got)
	}
	if registry.State("ch-a").Status != provider.StatusCooling {
		t.Fatalf("A 应冷却: %+v", registry.State("ch-a"))
	}
	// 冷却期再次请求仍走 B
	adapters["ch-a"].invokeErr = nil
	adapters["ch-b"].invokeErr = nil
	adapters["ch-a"].modelsSeen = nil
	adapters["ch-b"].modelsSeen = nil
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if len(adapters["ch-a"].modelsSeen) != 0 || len(adapters["ch-b"].modelsSeen) != 1 {
		t.Fatalf("冷却期应走 B: a=%v b=%v", adapters["ch-a"].modelsSeen, adapters["ch-b"].modelsSeen)
	}
}

// 冷却期满自动回 A。
func TestCooldownExpiry(t *testing.T) {
	channels, adapters := twoKimiChannels()
	r, registry := newTestRouter(t, channels, adapters)
	registry.SetCooling("ch-a", time.Now().Add(-time.Second), "429")
	adapters["ch-b"].modelsSeen = nil
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if len(adapters["ch-a"].modelsSeen) != 1 {
		t.Fatal("冷却期满应回 A")
	}
}

// auth-failed：渠道不被强打，全不可用时聚合人话错误。
func TestAuthFailedNoRetry(t *testing.T) {
	channels, adapters := twoKimiChannels()
	r, registry := newTestRouter(t, channels, adapters)
	registry.SetAuthFailed("ch-a", "上游 401/403")
	adapters["ch-b"].invokeErr = &provider.UpstreamError{Kind: provider.ErrKindQuota, StatusCode: 429}

	_, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8})
	if err == nil {
		t.Fatal("应报错")
	}
	var nc *ErrNoCandidates
	if !errors.As(err, &nc) {
		t.Fatalf("应是 ErrNoCandidates: %v", err)
	}
	joined := strings.Join(nc.Reasons, ";")
	if !strings.Contains(joined, "Kimi套餐A 鉴权失败") || !strings.Contains(joined, "Kimi套餐B：上游返回 HTTP 429") {
		t.Fatalf("聚合错误信息: %v", nc.Reasons)
	}
	// auth-failed 的 A 没有被调用过
	if len(adapters["ch-a"].modelsSeen) != 0 {
		t.Fatal("auth-failed 渠道不应被调用")
	}

	// 再次请求：B 已冷却、A 仍 auth-failed → 前置过滤即返回聚合错误（B 不再被调用）
	adapters["ch-b"].modelsSeen = nil
	_, err = r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8})
	if err == nil {
		t.Fatal("应报错")
	}
	if !errors.As(err, &nc) {
		t.Fatalf("第二次应是 ErrNoCandidates: %v", err)
	}
	joined = strings.Join(nc.Reasons, ";")
	if !strings.Contains(joined, "Kimi套餐B 冷却至") {
		t.Fatalf("第二次聚合错误信息: %v", nc.Reasons)
	}
	if len(adapters["ch-b"].modelsSeen) != 0 {
		t.Fatal("冷却中的 B 不应被调用")
	}
}

// 400 细分：schema 错误不切，model 错误切一次。
func TestBadRequestSwitching(t *testing.T) {
	channels, adapters := twoKimiChannels()
	adapters["ch-a"].invokeErr = &provider.UpstreamError{Kind: provider.ErrKindBadRequest, StatusCode: 400, Body: `{"error":{"message":"invalid tool schema"}}`}
	r, _ := newTestRouter(t, channels, adapters)
	_, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8})
	if err == nil {
		t.Fatal("应报错")
	}
	if len(adapters["ch-b"].modelsSeen) != 0 {
		t.Fatal("schema 400 不应切换")
	}

	channels2, adapters2 := twoKimiChannels()
	adapters2["ch-a"].invokeErr = &provider.UpstreamError{Kind: provider.ErrKindBadRequest, StatusCode: 400, Body: `{"error":{"message":"model kimi-k3 not found"}}`}
	r2, _ := newTestRouter(t, channels2, adapters2)
	if _, err := r2.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if len(adapters2["ch-b"].modelsSeen) != 1 {
		t.Fatal("model 400 应切换一次")
	}
}

// 5xx 切换但不改渠道状态。
func TestUpstreamSwitchNoState(t *testing.T) {
	channels, adapters := twoKimiChannels()
	adapters["ch-a"].invokeErr = &provider.UpstreamError{Kind: provider.ErrKindUpstream, StatusCode: 502}
	r, registry := newTestRouter(t, channels, adapters)
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if len(adapters["ch-b"].modelsSeen) != 1 {
		t.Fatal("5xx 应切换")
	}
	if registry.State("ch-a").Status != provider.StatusOK {
		t.Fatal("5xx 不应改状态")
	}
}

// 直连语法 + org/model 不误拆。
func TestDirectAndOrgModel(t *testing.T) {
	channels, adapters := twoKimiChannels()
	r, _ := newTestRouter(t, channels, adapters)

	targets, err := r.Resolve("ch-b/kimi-k2.7-code")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Channel.ID != "ch-b" || targets[0].UpstreamModel != "kimi-k2.7-code" {
		t.Fatalf("直连解析: %+v", targets)
	}
	// OpenRouter org/model 不拆
	if id, _, ok := splitDirect("openrouter/anthropic/claude-sonnet-4.6"); ok {
		t.Fatalf("org/model 被误拆: %s", id)
	}
	// 直连未声明模型也可用
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "ch-a/anything", MaxTokens: 8}); err != nil {
		t.Fatal(err)
	}
	if got := adapters["ch-a"].modelsSeen; len(got) != 1 || got[0] != "anything" {
		t.Fatalf("直连模型: %v", got)
	}
}

// 流式失败切换。
func TestStreamFailover(t *testing.T) {
	channels, adapters := twoKimiChannels()
	adapters["ch-a"].streamErr = &provider.UpstreamError{Kind: provider.ErrKindAuth, StatusCode: 401}
	r, _ := newTestRouter(t, channels, adapters)
	ch, err := r.Stream(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if len(adapters["ch-b"].modelsSeen) != 1 {
		t.Fatal("流式 401 应切换到 B")
	}
}

// 未声明模型报错。
func TestUnknownModel(t *testing.T) {
	channels, adapters := twoKimiChannels()
	r, _ := newTestRouter(t, channels, adapters)
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "nope", MaxTokens: 8}); err == nil {
		t.Fatal("应报错")
	}
}
