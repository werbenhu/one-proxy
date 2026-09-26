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
		resp, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6")
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

	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6"); err != nil {
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
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6"); err != nil {
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
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6"); err != nil {
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

	_, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6")
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
	_, err = r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6")
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
	_, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6")
	if err == nil {
		t.Fatal("应报错")
	}
	if len(adapters["ch-b"].modelsSeen) != 0 {
		t.Fatal("schema 400 不应切换")
	}

	channels2, adapters2 := twoKimiChannels()
	adapters2["ch-a"].invokeErr = &provider.UpstreamError{Kind: provider.ErrKindBadRequest, StatusCode: 400, Body: `{"error":{"message":"model kimi-k3 not found"}}`}
	r2, _ := newTestRouter(t, channels2, adapters2)
	if _, err := r2.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6"); err != nil {
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
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8}, "claude-sonnet-4-6"); err != nil {
		t.Fatal(err)
	}
	if len(adapters["ch-b"].modelsSeen) != 1 {
		t.Fatal("5xx 应切换")
	}
	if registry.State("ch-a").Status != provider.StatusOK {
		t.Fatal("5xx 不应改状态")
	}
}

// 直连（URL 路径前缀）：强制指定渠道/提供商，模型名原样透传。
func TestDirectAndOrgModel(t *testing.T) {
	channels, adapters := twoKimiChannels()
	r, _ := newTestRouter(t, channels, adapters)

	targets, err := r.ResolveDirect("ch-b", "kimi-k2.7-code")
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Channel.ID != "ch-b" || targets[0].UpstreamModel != "kimi-k2.7-code" {
		t.Fatalf("直连解析: %+v", targets)
	}
	// 直连未声明模型也可用（透传）
	if _, err := r.InvokeDirect(context.Background(), &anthropic.Request{Model: "anything", MaxTokens: 8}, "anything", "ch-a"); err != nil {
		t.Fatal(err)
	}
	if got := adapters["ch-a"].modelsSeen; len(got) != 1 || got[0] != "anything" {
		t.Fatalf("直连模型: %v", got)
	}
	// 未知目标报错
	if _, err := r.ResolveDirect("nope", "m"); err == nil {
		t.Fatal("未知直连目标应报错")
	}
}

// 流式失败切换。
func TestStreamFailover(t *testing.T) {
	channels, adapters := twoKimiChannels()
	adapters["ch-a"].streamErr = &provider.UpstreamError{Kind: provider.ErrKindAuth, StatusCode: 401}
	r, _ := newTestRouter(t, channels, adapters)
	ch, err := r.Stream(context.Background(), &anthropic.Request{Model: "claude-sonnet-4-6", MaxTokens: 8, Stream: true}, "claude-sonnet-4-6")
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
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "nope", MaxTokens: 8}, "nope"); err == nil {
		t.Fatal("应报错")
	}
}

func TestRoundRobinRouting(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{
		{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true},
		{ID: "pv-b", Name: "B", Type: config.TypeOpenAICompat, BaseURL: "https://b", APIKey: "b", Enabled: true},
	}
	cfg.Channels = []config.Channel{{ID: "ch-coding", Name: "Coding", Model: "coding", Strategy: config.StrategyRoundRobin, Enabled: true, Targets: []config.ChannelTarget{
		{ProviderID: "pv-a", UpstreamModel: "model-a", Weight: 1, Enabled: true},
		{ProviderID: "pv-b", UpstreamModel: "model-b", Weight: 1, Enabled: true},
	}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a, b := &fakeAdapter{}, &fakeAdapter{}
	registry := provider.NewRegistry()
	registry.Register("pv-a", a)
	registry.Register("pv-b", b)
	r := New(store, registry)
	for i := 0; i < 4; i++ {
		if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "coding", MaxTokens: 8}, "coding"); err != nil {
			t.Fatal(err)
		}
	}
	if len(a.modelsSeen) != 2 || len(b.modelsSeen) != 2 {
		t.Fatalf("轮询不均衡: a=%v b=%v", a.modelsSeen, b.modelsSeen)
	}
}

func TestDirectProviderRouting(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true}}
	cfg.Channels = []config.Channel{{ID: "ch-public", Name: "Public", Model: "public", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-a", UpstreamModel: "normal", Enabled: true}}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{}
	registry := provider.NewRegistry()
	registry.Register("pv-a", a)
	r := New(store, registry)
	if _, err := r.InvokeDirect(context.Background(), &anthropic.Request{Model: "special", MaxTokens: 8}, "special", "pv-a"); err != nil {
		t.Fatal(err)
	}
	if len(a.modelsSeen) != 1 || a.modelsSeen[0] != "special" {
		t.Fatalf("直连模型错误: %v", a.modelsSeen)
	}
}

// 上游模型留空 = 透传客户端请求的对外模型名。
func TestEmptyUpstreamModelPassthrough(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true}}
	cfg.Channels = []config.Channel{{ID: "ch-kimi", Name: "Kimi", Model: "kimi", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-a", Enabled: true}}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a := &fakeAdapter{}
	registry := provider.NewRegistry()
	registry.Register("pv-a", a)
	r := New(store, registry)
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "kimi", MaxTokens: 8}, "kimi"); err != nil {
		t.Fatal(err)
	}
	if len(a.modelsSeen) != 1 || a.modelsSeen[0] != "kimi" {
		t.Fatalf("透传模型错误: %v", a.modelsSeen)
	}
}

// 同名渠道：按列表顺序首个启用的渠道命中；ch-<id>/<model> 直连语法指定特定渠道。
func TestDuplicateModelChannels(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{
		{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true},
		{ID: "pv-b", Name: "B", Type: config.TypeOpenAICompat, BaseURL: "https://b", APIKey: "b", Enabled: true},
	}
	cfg.Channels = []config.Channel{
		{ID: "ch-a", Name: "A", Model: "kimi", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-a", UpstreamModel: "model-a", Enabled: true}}},
		{ID: "ch-b", Name: "B", Model: "kimi", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-b", UpstreamModel: "model-b", Enabled: true}}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a, b := &fakeAdapter{}, &fakeAdapter{}
	registry := provider.NewRegistry()
	registry.Register("pv-a", a)
	registry.Register("pv-b", b)
	r := New(store, registry)
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "kimi", MaxTokens: 8}, "kimi"); err != nil {
		t.Fatal(err)
	}
	if len(a.modelsSeen) != 1 || len(b.modelsSeen) != 0 {
		t.Fatalf("首个同名渠道应命中: a=%v b=%v", a.modelsSeen, b.modelsSeen)
	}
	if _, err := r.InvokeDirect(context.Background(), &anthropic.Request{Model: "kimi", MaxTokens: 8}, "kimi", "ch-b"); err != nil {
		t.Fatal(err)
	}
	if len(b.modelsSeen) != 1 || b.modelsSeen[0] != "model-b" {
		t.Fatalf("直连应命中 ch-b 并走其映射: %v", b.modelsSeen)
	}
}

// 通配渠道（对外模型名留空）：匹配未被具名渠道声明的模型并原样透传。
func TestWildcardChannelRouting(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{
		{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true},
		{ID: "pv-b", Name: "B", Type: config.TypeOpenAICompat, BaseURL: "https://b", APIKey: "b", Enabled: true},
	}
	cfg.Channels = []config.Channel{
		{ID: "ch-a", Name: "A", Model: "kimi", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-a", UpstreamModel: "model-a", Enabled: true}}},
		{ID: "ch-any", Name: "Any", Model: "", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-b", Enabled: true}}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a, b := &fakeAdapter{}, &fakeAdapter{}
	registry := provider.NewRegistry()
	registry.Register("pv-a", a)
	registry.Register("pv-b", b)
	r := New(store, registry)
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "kimi", MaxTokens: 8}, "kimi"); err != nil {
		t.Fatal(err)
	}
	if len(a.modelsSeen) != 1 || len(b.modelsSeen) != 0 {
		t.Fatalf("具名渠道优先: a=%v b=%v", a.modelsSeen, b.modelsSeen)
	}
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "gpt-5", MaxTokens: 8}, "gpt-5"); err != nil {
		t.Fatal(err)
	}
	if len(b.modelsSeen) != 1 || b.modelsSeen[0] != "gpt-5" {
		t.Fatalf("通配渠道应透传模型名: %v", b.modelsSeen)
	}
}

// 模型名本身带斜杠（OpenRouter 的 qwen/qwen-3.8）：无前缀按普通模型名匹配，
// 直连语法只按第一个斜杠切，剩余部分完整保留。
func TestSlashedModelNames(t *testing.T) {
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{
		{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true},
		{ID: "pv-b", Name: "B", Type: config.TypeOpenAICompat, BaseURL: "https://b", APIKey: "b", Enabled: true},
	}
	cfg.Channels = []config.Channel{
		{ID: "ch-or", Name: "OR", Model: "qwen/qwen-3.8", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-a", Enabled: true}}},
		{ID: "ch-any", Name: "Any", Model: "", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-b", Enabled: true}}},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	a, b := &fakeAdapter{}, &fakeAdapter{}
	registry := provider.NewRegistry()
	registry.Register("pv-a", a)
	registry.Register("pv-b", b)
	r := New(store, registry)
	// 具名渠道：qwen/qwen-3.8 不被误判为直连语法
	if _, err := r.Invoke(context.Background(), &anthropic.Request{Model: "qwen/qwen-3.8", MaxTokens: 8}, "qwen/qwen-3.8"); err != nil {
		t.Fatal(err)
	}
	if len(a.modelsSeen) != 1 || a.modelsSeen[0] != "qwen/qwen-3.8" {
		t.Fatalf("带斜杠模型名应走具名渠道并透传: %v", a.modelsSeen)
	}
	// 直连通配渠道：剩余部分完整保留透传
	if _, err := r.InvokeDirect(context.Background(), &anthropic.Request{Model: "openai/gpt-4o", MaxTokens: 8}, "openai/gpt-4o", "ch-any"); err != nil {
		t.Fatal(err)
	}
	if len(b.modelsSeen) != 1 || b.modelsSeen[0] != "openai/gpt-4o" {
		t.Fatalf("直连应保留完整模型名: %v", b.modelsSeen)
	}
}
