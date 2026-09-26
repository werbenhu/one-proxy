package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	anthropiccompat "github.com/werbenhu/one-proxy/internal/adapters/anthropiccompat"
	grokadapter "github.com/werbenhu/one-proxy/internal/adapters/grok"
	openaicompat "github.com/werbenhu/one-proxy/internal/adapters/openaicompat"
	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/usage"
)

func newTestServer(t *testing.T, channels []config.Channel) (*httptest.Server, *config.Store) {
	t.Helper()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = channels
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	for _, ch := range channels {
		if !ch.Enabled {
			continue
		}
		switch ch.Type {
		case config.TypeAnthropicCompat:
			registry.Register(ch.ID, anthropiccompat.New(ch.BaseURL, ch.APIKey, ""))
		default:
			t.Fatalf("测试不支持渠道类型: %s", ch.Type)
		}
	}
	srv := NewServer(store, registry)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, store
}

func TestAuthRejected(t *testing.T) {
	ts, _ := newTestServer(t, nil)
	resp, err := http.Post(ts.URL+"/v1/messages", "application/json", strings.NewReader(`{"model":"m"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("无密钥应 401, got %d", resp.StatusCode)
	}
	resp2, _ := http.Post(ts.URL+"/v1/messages", "application/json",
		strings.NewReader(`{"model":"m"}`))
	resp2.Body.Close()
	_ = resp2
}

func TestModelsAggregation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	channels := []config.Channel{
		{ID: "ch-a", Name: "A", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"m1", "m2"}, Enabled: true},
		{ID: "ch-b", Name: "B", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"m2", "m3"}, Enabled: true},
		{ID: "ch-c", Name: "C", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"disabled"}, Enabled: false},
	}
	ts, _ := newTestServer(t, channels)
	req, _ := http.NewRequest("GET", ts.URL+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("models: %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Data) != 3 {
		t.Fatalf("聚合去重错误: %+v", out.Data)
	}
}

func TestMessagesNonStreamEndToEnd(t *testing.T) {
	var gotBody map[string]json.RawMessage
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = w.Write([]byte(`{"id":"msg_9","type":"message","role":"assistant","model":"kimi-k3",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":7,"output_tokens":2}}`))
	}))
	defer up.Close()
	channels := []config.Channel{
		{ID: "ch-kimi", Name: "Kimi", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "kk",
			Models: []string{"claude-sonnet-4-6"}, ModelMapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}, Enabled: true},
	}
	ts, _ := newTestServer(t, channels)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(
		`{"model":"claude-sonnet-4-6","max_tokens":64,"system":"s","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	req.Header.Set("anthropic-beta", "context-1m-2025-08-07")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("messages: %d", resp.StatusCode)
	}
	var out map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if string(out["id"]) != `"msg_9"` {
		t.Fatalf("响应错误: %s", out["id"])
	}
	// 上游应看到重写后的 model 与透传的 beta 头在 gotBody 里 model 已映射
	if string(gotBody["model"]) != `"kimi-k3"` {
		t.Fatalf("别名映射失败: %s", gotBody["model"])
	}
	if string(gotBody["system"]) != `"s"` {
		t.Fatalf("system 保真失败: %s", gotBody["system"])
	}
}

func TestMessagesStreamEndToEnd(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n"))
		_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"))
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		fl.Flush()
	}))
	defer up.Close()
	channels := []config.Channel{
		{ID: "ch-kimi", Name: "Kimi", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "kk",
			Models: []string{"m"}, Enabled: true},
	}
	ts, _ := newTestServer(t, channels)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(
		`{"model":"m","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("x-api-key", "testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type: %s", ct)
	}
	data, _ := io.ReadAll(resp.Body)
	s := string(data)
	if !strings.Contains(s, "event: message_start") || !strings.Contains(s, "event: message_stop") {
		t.Fatalf("SSE 内容错误: %s", s)
	}
}

func TestDirectChannelSyntax(t *testing.T) {
	var gotPath string
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		var m map[string]json.RawMessage
		_ = json.Unmarshal(data, &m)
		gotModel = string(m["model"])
		_, _ = w.Write([]byte(`{"id":"m","role":"assistant","content":[],"usage":{"input_tokens":1}}`))
	}))
	defer up.Close()
	channels := []config.Channel{
		{ID: "ch-kimi", Name: "Kimi", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "kk",
			Models: []string{"other"}, Enabled: true},
	}
	ts, _ := newTestServer(t, channels)
	// 路径直连：/ch-kimi/v1/messages 强制走目标 ch-kimi，模型名原样透传
	req, _ := http.NewRequest("POST", ts.URL+"/ch-kimi/v1/messages", strings.NewReader(
		`{"model":"kimi-k3","max_tokens":8,"messages":[]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("直连失败: %d", resp.StatusCode)
	}
	if gotPath != "/v1/messages" || gotModel != `"kimi-k3"` {
		t.Fatalf("直连重写错误: path=%s model=%s", gotPath, gotModel)
	}
}

func TestUnknownModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	channels := []config.Channel{
		{ID: "ch-a", Name: "A", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"m1"}, Enabled: true},
	}
	ts, _ := newTestServer(t, channels)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(`{"model":"nope","max_tokens":8}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("未知模型应 404, got %d", resp.StatusCode)
	}
}

func TestUpstreamErrorPassthrough(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"quota"}`))
	}))
	defer up.Close()
	channels := []config.Channel{
		{ID: "ch-a", Name: "A", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"m1"}, Enabled: true},
	}
	ts, _ := newTestServer(t, channels)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(`{"model":"m1","max_tokens":8}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 429 {
		t.Fatalf("上游 429 应透传, got %d", resp.StatusCode)
	}
}

// 编译期断言：server_test 引用的类型存在。
var (
	_ = bytes.MinRead
	_ = context.Background
	_ = time.Second
	_ anthropic.Event
)

// 端到端 failover：主渠道 429 → 自动切备渠道（同模型双渠道）。
func TestEndToEndFailover(t *testing.T) {
	var bHits int
	upA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"quota"}`))
	}))
	defer upA.Close()
	upB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bHits++
		_, _ = w.Write([]byte(`{"id":"msg_b","type":"message","role":"assistant","model":"kimi-k3",
			"content":[{"type":"text","text":"from-b"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upB.Close()
	ts, _ := newTestServer(t, []config.Channel{
		{ID: "ch-a", Name: "A", Type: config.TypeAnthropicCompat, BaseURL: upA.URL, APIKey: "k",
			Models: []string{"kimi"}, Priority: 10, Enabled: true},
		{ID: "ch-b", Name: "B", Type: config.TypeAnthropicCompat, BaseURL: upB.URL, APIKey: "k",
			Models: []string{"kimi"}, Priority: 5, Enabled: true},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(`{"model":"kimi","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("failover 失败: %d %s", resp.StatusCode, body)
	}
	var out map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if string(out["id"]) != `"msg_b"` {
		t.Fatalf("应从 B 渠道响应: %s", out["id"])
	}
	if bHits != 1 {
		t.Fatalf("B 命中数: %d", bHits)
	}
}

// 端到端用量埋点：请求成功后 request_log 落库。
func TestUsageRecording(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"msg_u","type":"message","role":"assistant","model":"kimi-k3",
			"content":[{"type":"text","text":"x"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":11,"output_tokens":7,"cache_read_input_tokens":3}}`))
	}))
	defer up.Close()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = []config.Channel{{ID: "ch-u", Name: "U", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
		Models: []string{"m"}, ModelMapping: map[string]string{"m": "kimi-k3"}, Enabled: true}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("ch-u", anthropiccompat.New(up.URL, "k", ""))
	srv := NewServer(store, registry)
	dbPath := filepath.Join(t.TempDir(), "u2.db")
	if err := srv.AttachUsage(dbPath, "anthropic"); err != nil {
		t.Fatal(err)
	}
	tsv := httptest.NewServer(srv.Handler())
	defer tsv.Close()
	defer srv.CloseUsage()

	req, _ := http.NewRequest("POST", tsv.URL+"/v1/messages", strings.NewReader(`{"model":"m","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 等异步 recorder 刷写（轮询至多 2s）
	deadline := time.Now().Add(2 * time.Second)
	var rows []usage.AggRow
	for time.Now().Before(deadline) {
		rows, err = srv.UsageStore().Summary(time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rows) != 1 {
		t.Fatalf("埋点未落库: %+v", rows)
	}
	r0 := rows[0]
	if r0.ChannelID != store.Get().Channels[0].ID || r0.ModelRequested != "m" || r0.ModelUpstream != "kimi-k3" {
		t.Fatalf("埋点维度: %+v", r0)
	}
	if r0.InputTokens != 11 || r0.OutputTokens != 7 || r0.CacheRead != 3 {
		t.Fatalf("token 记账: %+v", r0)
	}
	providerToday, err := srv.UsageStore().ProviderToday(time.Now())
	if err != nil || providerToday["ch-u"] != 21 {
		t.Fatalf("提供商用量: %+v, %v", providerToday, err)
	}
}

// /v1/responses 直通端到端：grok 渠道原样转发。
func TestResponsesEndpointDirect(t *testing.T) {
	var hit map[string]json.RawMessage
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("grok 上游路径: %s", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &hit)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_9","object":"response","model":"grok-4.5","output":[],"usage":{"input_tokens":1}}`))
	}))
	defer up.Close()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = []config.Channel{
		{ID: "ch-grok", Name: "Grok", Type: config.TypeGrok, BaseURL: up.URL, APIKey: "xk",
			Models: []string{"grok-4.5"}, Enabled: true},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("ch-grok", grokadapter.New("xk", up.URL, ""))
	srv := NewServer(store, registry)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", strings.NewReader(
		`{"model":"grok-4.5","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("responses: %d %s", resp.StatusCode, body)
	}
	var out map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if string(out["id"]) != `"resp_9"` {
		t.Fatalf("直通响应: %s", out["id"])
	}
	if string(hit["input"]) != `"hi"` {
		t.Fatalf("直通请求保真: %s", hit["input"])
	}
}

// /v1/responses 直通路径也要记账（raw 转发不经 Invoke/Stream）。
func TestResponsesUsageRecorded(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","model":"grok-4.5","output":[],"usage":{"input_tokens":12,"output_tokens":5,"input_tokens_details":{"cached_tokens":4}}}`))
	}))
	defer up.Close()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = []config.Channel{
		{ID: "ch-grok", Name: "Grok", Type: config.TypeGrok, BaseURL: up.URL, APIKey: "xk",
			Models: []string{"grok-4.5"}, Enabled: true},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("ch-grok", grokadapter.New("xk", up.URL, ""))
	srv := NewServer(store, registry)
	dbPath := filepath.Join(t.TempDir(), "u3.db")
	if err := srv.AttachUsage(dbPath, "api"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.CloseUsage()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", strings.NewReader(`{"model":"grok-4.5","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	// 等异步 recorder 刷写（轮询至多 2s）
	deadline := time.Now().Add(2 * time.Second)
	var rows []usage.AggRow
	for time.Now().Before(deadline) {
		rows, err = srv.UsageStore().Summary(time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rows) != 1 {
		t.Fatalf("responses 埋点未落库: %+v", rows)
	}
	if rows[0].InputTokens != 12 || rows[0].OutputTokens != 5 || rows[0].CacheRead != 4 {
		t.Fatalf("responses token 记账: %+v", rows[0])
	}
}

// 流式直通：tee 旁路从 response.completed 事件取 usage 记账。
func TestResponsesStreamUsageRecorded(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":9,\"output_tokens\":3}}}\n\n"))
	}))
	defer up.Close()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = []config.Channel{
		{ID: "ch-grok", Name: "Grok", Type: config.TypeGrok, BaseURL: up.URL, APIKey: "xk",
			Models: []string{"grok-4.5"}, Enabled: true},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("ch-grok", grokadapter.New("xk", up.URL, ""))
	srv := NewServer(store, registry)
	dbPath := filepath.Join(t.TempDir(), "u4.db")
	if err := srv.AttachUsage(dbPath, "api"); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.CloseUsage()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", strings.NewReader(`{"model":"grok-4.5","input":"hi","stream":true}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "response.output_text.delta") {
		t.Fatalf("流式直通保真: %s", body)
	}

	deadline := time.Now().Add(2 * time.Second)
	var rows []usage.AggRow
	for time.Now().Before(deadline) {
		rows, err = srv.UsageStore().Summary(time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(rows) != 1 || rows[0].InputTokens != 9 || rows[0].OutputTokens != 3 {
		t.Fatalf("流式 responses 记账: %+v", rows)
	}
}

func TestResponsesOpenAICompatibleFailover(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"quota"}`, http.StatusTooManyRequests)
	}))
	defer first.Close()
	var observedModel string
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("上游路径: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer key-b" {
			t.Errorf("上游鉴权错误")
		}
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		observedModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp_b","object":"response","output":[]}`))
	}))
	defer second.Close()
	store := config.NewStore(filepath.Join(t.TempDir(), "config.json"))
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Providers = []config.ProviderAccount{
		{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: first.URL + "/v1", APIKey: "key-a", Enabled: true},
		{ID: "pv-b", Name: "B", Type: config.TypeOpenAICompat, BaseURL: second.URL + "/v1", APIKey: "key-b", Enabled: true},
	}
	cfg.Channels = []config.Channel{{ID: "ch-public", Name: "Public", Model: "public", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{
		{ProviderID: "pv-a", UpstreamModel: "upstream-a", Priority: 10, Enabled: true},
		{ProviderID: "pv-b", UpstreamModel: "upstream-b", Priority: 5, Enabled: true},
	}}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("pv-a", openaicompat.New(first.URL+"/v1", "key-a", ""))
	registry.Register("pv-b", openaicompat.New(second.URL+"/v1", "key-b", ""))
	server := NewServer(store, registry)
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/responses", strings.NewReader(`{"model":"public","input":"hi"}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, data)
	}
	if observedModel != "upstream-b" {
		t.Fatalf("上游模型映射错误: %s", observedModel)
	}
	if registry.State("pv-a").Status != provider.StatusCooling {
		t.Fatalf("首选提供商未冷却: %+v", registry.State("pv-a"))
	}
}
