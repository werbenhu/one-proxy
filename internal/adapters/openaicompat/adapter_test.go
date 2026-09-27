package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// 非流式：canonical → chat 上游 → canonical。
func TestInvokeRoundtrip(t *testing.T) {
	var gotBody map[string]json.RawMessage
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径: %s", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &gotBody)
		_, _ = w.Write([]byte(`{"id":"cmpl-1","model":"deepseek-v4-pro","choices":[{
			"message":{"role":"assistant","content":"ok","tool_calls":[
				{"id":"c1","type":"function","function":{"name":"f","arguments":"{\"a\":1}"}}]},
			"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13}}`))
	}))
	defer up.Close()

	a := New(up.URL, "sk-ds", "")
	req, _ := anthropic.ParseRequest([]byte(`{"model":"deepseek","max_tokens":64,
		"system":"be brief","messages":[{"role":"user","content":"hi"}],"temperature":0.5}`))
	resp, err := a.Invoke(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Content) != 2 || resp.Content[0].Text != "ok" {
		t.Fatalf("content: %+v", resp.Content)
	}
	if resp.Content[1].Type != "tool_use" || string(resp.Content[1].Input) != `{"a":1}` {
		t.Fatalf("tool_use: %+v", resp.Content[1])
	}
	if resp.StopReason != "tool_use" || resp.Usage.InputTokens != 9 || resp.Usage.OutputTokens != 4 {
		t.Fatalf("stop/usage: %s %+v", resp.StopReason, resp.Usage)
	}
	if string(gotBody["model"]) != `"deepseek"` || string(gotBody["temperature"]) != "0.5" {
		t.Fatalf("上行请求: %s", gotBody)
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(gotBody["messages"], &msgs)
	if string(msgs[0]["role"]) != `"system"` || string(msgs[0]["content"]) != `"be brief"` {
		t.Fatalf("system 映射: %s", gotBody["messages"])
	}
}

// 流式：chat chunk 重建为 Anthropic 事件序列。
func TestStreamRebuild(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		chunks := []string{
			`data: {"id":"cmpl-1","model":"ds","choices":[{"delta":{"role":"assistant"}}]}`,
			`data: {"id":"cmpl-1","model":"ds","choices":[{"delta":{"content":"你"}}]}`,
			`data: {"id":"cmpl-1","model":"ds","choices":[{"delta":{"content":"好"}}]}`,
			`data: {"id":"cmpl-1","choices":[{"delta":{"tool_calls":[{"id":"c1","index":0,"function":{"name":"f","arguments":""}}]}},{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":"}}]}]}`,
			`data: {"id":"cmpl-1","choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":6}}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			_, _ = w.Write([]byte(c + "\n\n"))
			fl.Flush()
		}
	}))
	defer up.Close()

	a := New(up.URL, "k", "")
	req := &anthropic.Request{Model: "ds", MaxTokens: 8, Stream: true,
		Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}
	ch, err := a.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	var fullText strings.Builder
	for ev := range ch {
		types = append(types, ev.Type)
		var probe struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		_ = json.Unmarshal(ev.Raw, &probe)
		fullText.WriteString(probe.Delta.Text)
	}
	joined := strings.Join(types, ",")
	for _, want := range []string{"message_start", "content_block_delta", "content_block_delta", "content_block_start", "message_delta", "message_stop"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("缺少事件 %s: %v", want, types)
		}
	}
	if fullText.String() != "你好" {
		t.Fatalf("文本拼接: %q", fullText.String())
	}
}

// 回归：上游 finish chunk 不带 usage 时，message_delta 也必须带 output_tokens（zcode 等客户端强校验）。
func TestStreamFinishWithoutUsage(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range []string{
			`data: {"id":"cmpl-1","model":"ds","choices":[{"delta":{"content":"hi"}}]}`,
			`data: {"id":"cmpl-1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		} {
			_, _ = w.Write([]byte(c + "\n\n"))
			fl.Flush()
		}
	}))
	defer up.Close()

	a := New(up.URL, "k", "")
	req := &anthropic.Request{Model: "ds", MaxTokens: 8, Stream: true,
		Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}
	ch, err := a.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var deltaRaw json.RawMessage
	for ev := range ch {
		if ev.Type == "message_delta" {
			deltaRaw = ev.Raw
		}
	}
	if deltaRaw == nil {
		t.Fatal("缺少 message_delta 事件")
	}
	var probe struct {
		Usage struct {
			OutputTokens *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(deltaRaw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Usage.OutputTokens == nil {
		t.Fatalf("message_delta 缺 output_tokens: %s", deltaRaw)
	}
}

// 回归：usage 在 finish 之后的独立 chunk 里（stream_options.include_usage 风格），
// message_delta 应等到流结束再发并带上最终 output_tokens。
func TestStreamUsageAfterFinish(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for _, c := range []string{
			`data: {"id":"cmpl-1","model":"ds","choices":[{"delta":{"content":"hi"}}]}`,
			`data: {"id":"cmpl-1","choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"cmpl-1","choices":[],"usage":{"prompt_tokens":5,"completion_tokens":6}}`,
			`data: [DONE]`,
		} {
			_, _ = w.Write([]byte(c + "\n\n"))
			fl.Flush()
		}
	}))
	defer up.Close()

	a := New(up.URL, "k", "")
	req := &anthropic.Request{Model: "ds", MaxTokens: 8, Stream: true,
		Messages: json.RawMessage(`[{"role":"user","content":"hi"}]`)}
	ch, err := a.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var deltaRaw json.RawMessage
	deltaCount := 0
	for ev := range ch {
		if ev.Type == "message_delta" {
			deltaCount++
			deltaRaw = ev.Raw
		}
	}
	if deltaCount != 1 {
		t.Fatalf("message_delta 应恰好一次: %d", deltaCount)
	}
	var probe struct {
		Usage struct {
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(deltaRaw, &probe); err != nil {
		t.Fatal(err)
	}
	if probe.Usage.OutputTokens != 6 {
		t.Fatalf("output_tokens 应为 6: %s", deltaRaw)
	}
}

func TestErrorMapping(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer up.Close()
	a := New(up.URL, "k", "")
	_, err := a.Invoke(context.Background(), &anthropic.Request{Model: "m", MaxTokens: 8,
		Messages: json.RawMessage(`[{"role":"user","content":"x"}]`)})
	if err == nil || a.NormalizeError(err) != provider.ErrKindAuth {
		t.Fatalf("401 映射: %v", err)
	}
}

func TestModels(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("路径: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"deepseek-chat"}]}`))
	}))
	defer up.Close()
	a := New(up.URL, "k", "")
	models, err := a.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "deepseek-chat" {
		t.Fatalf("models: %+v", models)
	}
}

// 智谱风格错误：HTTP 200 但 body 是 {"code":1001,"msg":"...","success":false}，
// 必须转成上游错误而不是当作空模型列表。
func TestModelsBigmodelErrorEnvelope(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":1001,"msg":"Header中未收到Authorization参数，无法进行身份验证。","success":false}`))
	}))
	defer up.Close()
	a := New(up.URL, "", "")
	_, err := a.Models(context.Background())
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !strings.Contains(err.Error(), "Header中未收到Authorization参数") {
		t.Fatalf("错误应包含上游 msg: %v", err)
	}
	var ue *provider.UpstreamError
	if !errors.As(err, &ue) || a.NormalizeError(err) != provider.ErrKindAuth {
		t.Fatalf("1001 应映射为鉴权错误: %v", err)
	}
}

// 智谱鉴权失败：code=401 映射为 ErrKindAuth。
func TestModelsBigmodelUnauthorized(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":401,"msg":"令牌已过期或验证不正确","success":false}`))
	}))
	defer up.Close()
	a := New(up.URL, "sk-bad", "")
	_, err := a.Models(context.Background())
	if a.NormalizeError(err) != provider.ErrKindAuth {
		t.Fatalf("401 映射: %v", err)
	}
}
