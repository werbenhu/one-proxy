package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	anthropiccompat "github.com/werbenhu/one-proxy/internal/adapters/anthropiccompat"
	openaicompat "github.com/werbenhu/one-proxy/internal/adapters/openaicompat"
	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// chat 入口 × anthropic 上游（跨协议转换）。
func TestChatEndpointToAnthropicUpstream(t *testing.T) {
	var gotBody map[string]json.RawMessage
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("anthropic 上游路径: %s", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"kimi-k3",
			"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer up.Close()
	ts, _ := newTestServer(t, []config.Channel{
		{ID: "ch-kimi", Name: "Kimi", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"my-model"}, ModelMapping: map[string]string{"my-model": "kimi-k3"}, Enabled: true},
	})
	resp, err := http.Post(ts.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// 无密钥
	if resp.StatusCode == 200 {
		t.Fatal("应 401")
	}
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(
		`{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("chat 端点: %d %s", resp2.StatusCode, body)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage map[string]int64 `json:"usage"`
	}
	_ = json.NewDecoder(resp2.Body).Decode(&out)
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "ok" {
		t.Fatalf("choices: %+v", out)
	}
	if out.Usage["total_tokens"] != 5 {
		t.Fatalf("usage: %+v", out.Usage)
	}
	if string(gotBody["model"]) != `"kimi-k3"` {
		t.Fatalf("上游 model 映射: %s", gotBody["model"])
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(gotBody["messages"], &msgs)
	if string(msgs[0]["role"]) != `"user"` {
		t.Fatalf("messages: %s", gotBody["messages"])
	}
}

// chat 入口 × openai 上游（恒等协议，转换双向不碰上游格式语义）。
func TestChatEndpointToOpenAIUpstream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("openai 上游路径: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"cmpl-1","choices":[{"message":{"role":"assistant","content":"hi from ds"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer up.Close()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = []config.Channel{
		{ID: "ch-ds", Name: "DS", Type: config.TypeOpenAICompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"deepseek"}, Enabled: true},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("ch-ds", openaicompat.New(up.URL, "k"))
	srv := NewServer(store, registry)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(
		`{"model":"deepseek","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("chat: %d %s", resp.StatusCode, body)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "hi from ds" {
		t.Fatalf("choices: %+v", out)
	}
}

// chat 入口流式 × anthropic 上游：SSE chunk 序列与 [DONE]。
func TestChatStreamEndpoint(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		events := []string{
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"model\":\"k\",\"role\":\"assistant\"}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\n\n",
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e))
			fl.Flush()
		}
	}))
	defer up.Close()
	ts, _ := newTestServer(t, []config.Channel{
		{ID: "ch-k", Name: "K", Type: config.TypeAnthropicCompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"m"}, Enabled: true},
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/chat/completions", strings.NewReader(
		`{"model":"m","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	s := string(data)
	if !strings.Contains(s, `"content":"你好"`) {
		t.Fatalf("缺少文本 chunk: %s", s)
	}
	if !strings.Contains(s, `"finish_reason":"stop"`) {
		t.Fatalf("缺少 finish chunk: %s", s)
	}
	if !strings.Contains(s, "data: [DONE]") {
		t.Fatalf("缺少 [DONE]: %s", s)
	}
}

// anthropic 入口 × openai 上游（混合路由反向）。
func TestAnthropicEndpointToOpenAIUpstream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"cmpl-1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer up.Close()
	dir := t.TempDir()
	store := config.NewStore(dir + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "testkey123"
	cfg.Channels = []config.Channel{
		{ID: "ch-ds", Name: "DS", Type: config.TypeOpenAICompat, BaseURL: up.URL, APIKey: "k",
			Models: []string{"glm"}, Enabled: true},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := provider.NewRegistry()
	registry.Register("ch-ds", openaicompat.New(up.URL, "k"))
	srv := NewServer(store, registry)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/messages", strings.NewReader(
		`{"model":"glm","max_tokens":64,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer testkey123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("anthropic→openai: %d %s", resp.StatusCode, body)
	}
	var out map[string]json.RawMessage
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if string(out["type"]) != `"message"` {
		t.Fatalf("响应类型: %s", out["type"])
	}
}

var _ = anthropiccompat.New
