package anthropiccompat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// 非流式往返：上游收到的请求体与客户端原始语义等价（除 model 重写），
// 响应未知字段原样回传。
func TestInvokeRoundtrip(t *testing.T) {
	var gotBody map[string]json.RawMessage
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("鉴权头缺失")
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"kimi-k3",
			"content":[{"type":"text","text":"你好"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":5,"output_tokens":3},"new_field":1}`))
	}))
	defer upstream.Close()

	adapter := New(upstream.URL, "sk-test")
	req, err := anthropic.ParseRequest([]byte(`{"model":"claude-sonnet-4-6","max_tokens":100,
		"system":"be brief","messages":[{"role":"user","content":"hi"}],"top_k":7}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Model = "kimi-k3"
	resp, err := adapter.Invoke(context.Background(), req)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if resp.ID != "msg_1" || resp.Usage.OutputTokens != 3 {
		t.Fatalf("响应解析错误: %+v", resp)
	}
	var extra map[string]json.RawMessage
	out, _ := anthropic.WriteResponseBytes(resp)
	_ = json.Unmarshal(out, &extra)
	if string(extra["new_field"]) != "1" {
		t.Errorf("响应未知字段丢失: %s", extra["new_field"])
	}
	if string(gotBody["system"]) != `"be brief"` || string(gotBody["top_k"]) != "7" {
		t.Errorf("请求保真失败: system=%s top_k=%s", gotBody["system"], gotBody["top_k"])
	}
	if string(gotBody["model"]) != `"kimi-k3"` {
		t.Errorf("model 重写失败: %s", gotBody["model"])
	}
}

// beta/version 头透传，非白名单头不透传。
func TestHeaderForwarding(t *testing.T) {
	var gotAuth, gotBeta, gotVersion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBeta = r.Header.Get("anthropic-beta")
		gotVersion = r.Header.Get("anthropic-version")
		_, _ = w.Write([]byte(`{"id":"m","role":"assistant","content":[],"usage":{"input_tokens":1}}`))
	}))
	defer upstream.Close()

	adapter := New(upstream.URL, "k")
	req := &anthropic.Request{Model: "m", MaxTokens: 8, Header: http.Header{}}
	req.Header.Set("anthropic-beta", "context-1m-2025-08-07")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-custom", "no")
	if _, err := adapter.Invoke(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer k" || gotBeta != "context-1m-2025-08-07" || gotVersion != "2023-06-01" {
		t.Fatalf("头透传错误: %q %q %q", gotAuth, gotBeta, gotVersion)
	}
}

// 流式：SSE 事件序列原样归一化。
func TestStreamEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("流式 Accept 头错误: %s", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		events := []string{
			`event: message_start` + "\n" + `data: {"type":"message_start","message":{"usage":{"input_tokens":5}}}` + "\n\n",
			`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}` + "\n\n",
			`event: unknown_kind` + "\n" + `data: {"type":"unknown_kind","x":1}` + "\n\n",
			`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n",
		}
		for _, e := range events {
			_, _ = w.Write([]byte(e))
			fl.Flush()
		}
	}))
	defer upstream.Close()

	adapter := New(upstream.URL, "k")
	req, _ := anthropic.ParseRequest([]byte(`{"model":"m","max_tokens":8,"stream":true,"messages":[]}`))
	ch, err := adapter.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var types []string
	for ev := range ch {
		types = append(types, ev.Type)
	}
	want := []string{"message_start", "content_block_delta", "unknown_kind", "message_stop"}
	if len(types) != len(want) {
		t.Fatalf("事件数量: %v", types)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("事件序列: %v", types)
		}
	}
}

// 错误映射矩阵。
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status int
		want   provider.ErrorKind
	}{
		{401, provider.ErrKindAuth},
		{403, provider.ErrKindAuth},
		{429, provider.ErrKindQuota},
		{500, provider.ErrKindUpstream},
		{400, provider.ErrKindBadRequest},
	}
	for _, c := range cases {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"error":{"message":"x"}}`))
		}))
		adapter := New(upstream.URL, "k")
		req := &anthropic.Request{Model: "m", MaxTokens: 8}
		_, err := adapter.Invoke(context.Background(), req)
		if err == nil {
			t.Fatalf("status %d 应报错", c.status)
		}
		if got := adapter.NormalizeError(err); got != c.want {
			t.Errorf("status %d: want=%v got=%v", c.status, c.want, got)
		}
		upstream.Close()
	}
}

// 流式请求上游报错：Stream 返回 UpstreamError 而非事件流。
func TestStreamUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":"quota"}`))
	}))
	defer upstream.Close()
	adapter := New(upstream.URL, "k")
	req := &anthropic.Request{Model: "m", MaxTokens: 8, Stream: true}
	_, err := adapter.Stream(context.Background(), req)
	if err == nil || adapter.NormalizeError(err) != provider.ErrKindQuota {
		t.Fatalf("429 流式错误映射失败: %v", err)
	}
}

func TestModels(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("路径: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"kimi-k3"},{"id":"kimi-k2.7-code"}]}`))
	}))
	defer upstream.Close()
	adapter := New(upstream.URL, "k")
	models, err := adapter.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].ID != "kimi-k3" {
		t.Fatalf("模型列表: %+v", models)
	}
}

func TestStreamIdleTimeout(t *testing.T) {
	// 上游慢速输出事件，验证读取不因单次 Read 阻塞而死锁。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for i := 0; i < 3; i++ {
			time.Sleep(30 * time.Millisecond)
			_, _ = w.Write([]byte("data: {\"type\":\"ping\"}\n\n"))
			fl.Flush()
		}
	}))
	defer upstream.Close()
	adapter := New(upstream.URL, "k")
	req := &anthropic.Request{Model: "m", MaxTokens: 8, Stream: true}
	ch, err := adapter.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for range ch {
		count++
	}
	if count != 3 {
		t.Fatalf("事件数: %d", count)
	}
}
