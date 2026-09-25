package grokadapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// Responses 上游假服务：返回固定 message 输出。
func responsesUpstream(t *testing.T, hit *map[string]json.RawMessage, path *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*path = r.URL.Path
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"grok-4.5"}]}`))
			return
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, hit)
		_, _ = w.Write([]byte(`{"id":"resp_1","model":"grok-4.5","output":[
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello from grok"}]}],
			"usage":{"input_tokens":5,"output_tokens":3}}`))
	}))
}

// Invoke：canonical Anthropic → Responses → Anthropic。
func TestInvokeRoundtrip(t *testing.T) {
	var hit map[string]json.RawMessage
	var path string
	up := responsesUpstream(t, &hit, &path)
	defer up.Close()

	a := New("xai-key", up.URL)
	req, _ := anthropic.ParseRequest([]byte(`{"model":"grok-4.5","max_tokens":64,
		"messages":[{"role":"user","content":"hi"}]}`))
	resp, err := a.Invoke(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/responses" {
		t.Fatalf("上游路径: %s", path)
	}
	if len(resp.Content) == 0 || resp.Content[0].Text != "hello from grok" {
		t.Fatalf("content: %+v", resp.Content)
	}
	// Responses 请求形态：input 数组 + model
	if string(hit["model"]) != `"grok-4.5"` {
		t.Fatalf("model: %s", hit["model"])
	}
	var input []map[string]any
	if json.Unmarshal(hit["input"], &input) != nil || len(input) == 0 {
		t.Fatalf("input: %s", hit["input"])
	}
}

// ForwardRaw 三协议直通。
func TestForwardRawProtocols(t *testing.T) {
	var hit map[string]json.RawMessage
	var path string
	up := responsesUpstream(t, &hit, &path)
	defer up.Close()
	a := New("xai-key", up.URL)

	// chat 直通
	result, err := a.ForwardRaw(context.Background(), provider.ProtocolChat,
		[]byte(`{"model":"grok-4.5","messages":[{"role":"user","content":"hi"}]}`), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(result.Body)
	result.Body.Close()
	if !strings.Contains(string(body), "output_text") {
		t.Fatalf("chat 直通应返回 Responses 原文: %s", body)
	}

	// responses 直通（恒等，只透传）
	result2, err := a.ForwardRaw(context.Background(), provider.ProtocolResponses,
		[]byte(`{"model":"grok-4.5","input":"hi"}`), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	result2.Body.Close()
	if path != "/responses" {
		t.Fatalf("responses 直通路径: %s", path)
	}

	// 不支持的协议报错
	if _, err := a.ForwardRaw(context.Background(), "bogus", []byte(`{}`), nil, false); err == nil {
		t.Fatal("未知协议应报错")
	}
}

// Stream：canonical 流式事件。
func TestStreamEvents(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		events := []string{
			`event: response.output_text.delta` + "\n" + `data: {"type":"response.output_text.delta","delta":"你"}` + "\n\n",
			`event: response.completed` + "\n" + `data: {"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":1}}}` + "\n\n",
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e))
			fl.Flush()
		}
	}))
	defer up.Close()
	a := New("k", up.URL)
	req := &anthropic.Request{Model: "grok-4.5", MaxTokens: 8, Stream: true,
		Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}
	ch, err := a.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for ev := range ch {
		types = append(types, ev.Type)
	}
	if len(types) == 0 {
		t.Fatal("无事件")
	}
	joined := strings.Join(types, ",")
	if !strings.Contains(joined, "message_stop") && !strings.Contains(joined, "message_delta") && len(types) < 2 {
		t.Fatalf("事件序列不完整: %v", types)
	}
}

// 错误映射。
func TestErrorClassification(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"quota"}`))
	}))
	defer up.Close()
	a := New("k", up.URL)
	_, err := a.Invoke(context.Background(), &anthropic.Request{Model: "grok-4.5", MaxTokens: 8,
		Messages: json.RawMessage(`[{"role":"user","content":"x"}]`)})
	if err == nil || a.NormalizeError(err) != provider.ErrKindQuota {
		t.Fatalf("429 映射: %v", err)
	}
}

func TestModels(t *testing.T) {
	var hit map[string]json.RawMessage
	var path string
	up := responsesUpstream(t, &hit, &path)
	defer up.Close()
	a := New("k", up.URL)
	models, err := a.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "grok-4.5" {
		t.Fatalf("models: %+v", models)
	}
}

// OAuth 状态回调：凭据不可用时触发。
func TestOAuthCredentialMissing(t *testing.T) {
	var statusErr error
	a := NewOAuth("", func() []byte { return []byte(`{"mode":"oauth"}`) }, func([]byte) error { return nil }, func(err error) { statusErr = err })
	_, err := a.Invoke(context.Background(), &anthropic.Request{Model: "grok-4.5", MaxTokens: 8,
		Messages: json.RawMessage(`[{"role":"user","content":"x"}]`)})
	if err == nil {
		t.Fatal("缺 refresh token 应报错")
	}
	if statusErr == nil {
		t.Fatal("状态回调未触发")
	}
}
