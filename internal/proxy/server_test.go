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
			registry.Register(ch.ID, anthropiccompat.New(ch.BaseURL, ch.APIKey))
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
	// 直连：ch-kimi/kimi-k3 即使渠道没声明 kimi-k3 也可用（调试语法）
	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(
		`{"model":"ch-kimi/kimi-k3","max_tokens":8,"messages":[]}`))
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

func TestOpenRouterModelNameNotSplit(t *testing.T) {
	// openrouter/anthropic/claude-... 不能被当直连语法拆分
	model, direct := resolveModel("anthropic/claude-sonnet-4.6")
	if direct != "" || model != "anthropic/claude-sonnet-4.6" {
		t.Fatalf("org/model 被误拆: %s %s", model, direct)
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
	registry.Register("ch-u", anthropiccompat.New(up.URL, "k"))
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
	if r0.ChannelID != "ch-u" || r0.ModelRequested != "m" || r0.ModelUpstream != "kimi-k3" {
		t.Fatalf("埋点维度: %+v", r0)
	}
	if r0.InputTokens != 11 || r0.OutputTokens != 7 || r0.CacheRead != 3 {
		t.Fatalf("token 记账: %+v", r0)
	}
}
