// Package openaicompat 通用 OpenAI 兼容适配器：canonical ↔ chat 转换后转发。
package openaicompat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/protocol/convert"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// New 工厂：proxyURL 为该提供商专用 HTTP 代理（空走系统环境代理）。
func New(baseURL, apiKey, proxyURL string, responseHeaderTimeout ...time.Duration) *Adapter {
	timeout := provider.DefaultResponseHeaderTimeout
	if len(responseHeaderTimeout) > 0 && responseHeaderTimeout[0] > 0 {
		timeout = responseHeaderTimeout[0]
	}
	return &Adapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Transport: provider.TransportWithResponseHeaderTimeout(proxyURL, timeout)},
	}
}

type Adapter struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

const maxErrorBodyBytes = 2 << 20

func (a *Adapter) buildRequest(ctx context.Context, req *anthropic.Request, body []byte, stream bool) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	applyClientHeaders(httpReq, req)
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	return httpReq, nil
}

// applyClientHeaders 透传 canonical header bag 里的客户端身份头（User-Agent 等，
// 中转不改变来源）；适配器身份头（鉴权/Content-Type）不受影响。
func applyClientHeaders(httpReq *http.Request, req *anthropic.Request) {
	if req == nil || req.Header == nil {
		return
	}
	for _, h := range []string{"User-Agent", "X-Api-Source", "X-Title", "Http-X-Title"} {
		if v := req.Header.Get(h); v != "" {
			httpReq.Header.Set(h, v)
		}
	}
}

func (a *Adapter) Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	body, err := convert.AnthropicToChatRequest(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := a.buildRequest(ctx, req, body, false)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamError(resp.StatusCode, resp.Status, data)
	}
	return chatResponseToAnthropic(data)
}

// Stream 把上游 chat SSE 转换为 canonical 事件流（重建 Anthropic 事件序列）。
func (a *Adapter) Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error) {
	// 上游 chat 请求需要 stream:true
	streamReq := *req
	streamReq.Stream = true
	body, err := convert.AnthropicToChatRequest(&streamReq)
	if err != nil {
		return nil, err
	}
	httpReq, err := a.buildRequest(ctx, &streamReq, body, true)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
		return nil, upstreamError(resp.StatusCode, resp.Status, data)
	}
	events := make(chan anthropic.Event, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		accumulateChatSSE(resp.Body, events)
	}()
	return events, nil
}

// accumulateChatSSE 把 chat chunk 流重建为 Anthropic 事件序列：
// 首个 chunk → message_start；delta.content → content_block_delta(text_delta)；
// tool_calls → content_block_start(tool_use)+input_json_delta；
// finish → message_delta(stop_reason)+message_stop。
func accumulateChatSSE(r io.Reader, events chan<- anthropic.Event) {
	emit := func(e anthropic.Event) { events <- e }
	sendStart := false
	blockOpen := false
	pendingStop := ""
	var outputTokens int64
	// 延迟到流结束再发 message_delta：上游可能把 usage 放在 finish 之后的独立 chunk 里，
	// 且 zcode 等客户端要求 usage.output_tokens 必为数字。
	flushFinish := func() {
		if pendingStop == "" {
			return
		}
		emit(anthropic.Event{Type: "message_delta", Raw: mustJSON(map[string]any{
			"type":  "message_delta",
			"delta": map[string]string{"stop_reason": pendingStop},
			"usage": map[string]any{"output_tokens": outputTokens},
		})})
		emit(anthropic.Event{Type: "message_stop", Raw: mustJSON(map[string]string{"type": "message_stop"})})
		pendingStop = ""
	}
	send := func(data []byte) {
		var chunk struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Index    int    `json:"index"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int64 `json:"prompt_tokens"`
				CompletionTokens int64 `json:"completion_tokens"`
				PromptDetails    *struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal(data, &chunk) != nil {
			return
		}
		if !sendStart && (chunk.ID != "" || len(chunk.Choices) > 0) {
			sendStart = true
			// OpenAI/GLM 口径：prompt_tokens 为全量输入（含 cached）；记账用互斥口径，输入侧减去缓存读。
			usage := map[string]any{"input_tokens": 0, "output_tokens": 0}
			if chunk.Usage != nil {
				cached := int64(0)
				if chunk.Usage.PromptDetails != nil {
					cached = chunk.Usage.PromptDetails.CachedTokens
				}
				input := chunk.Usage.PromptTokens - cached
				if input < 0 {
					input = 0
				}
				usage["input_tokens"] = input
				if cached > 0 {
					usage["cache_read_input_tokens"] = cached
				}
			}
			emit(anthropic.Event{Type: "message_start", Raw: mustJSON(map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": chunk.ID, "type": "message", "role": "assistant", "model": chunk.Model,
					"content": []any{}, "usage": usage,
				},
			})})
			emit(anthropic.Event{Type: "content_block_start", Raw: mustJSON(map[string]any{
				"type": "content_block_start", "index": 0,
				"content_block": map[string]string{"type": "text", "text": ""},
			})})
			blockOpen = true
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				emit(anthropic.Event{Type: "content_block_delta", Raw: mustJSON(map[string]any{
					"type": "content_block_delta", "index": 0,
					"delta": map[string]string{"type": "text_delta", "text": choice.Delta.Content},
				})})
			}
			for _, tc := range choice.Delta.ToolCalls {
				if tc.ID != "" {
					if blockOpen {
						emit(anthropic.Event{Type: "content_block_stop", Raw: mustJSON(map[string]any{"type": "content_block_stop", "index": 0})})
						blockOpen = false
					}
					emit(anthropic.Event{Type: "content_block_start", Raw: mustJSON(map[string]any{
						"type": "content_block_start", "index": 1,
						"content_block": map[string]any{"type": "tool_use", "id": tc.ID, "name": tc.Function.Name, "input": map[string]any{}},
					})})
				}
				if tc.Function.Arguments != "" {
					emit(anthropic.Event{Type: "content_block_delta", Raw: mustJSON(map[string]any{
						"type": "content_block_delta", "index": 1,
						"delta": map[string]string{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
					})})
				}
			}
			if choice.FinishReason != nil {
				if blockOpen {
					emit(anthropic.Event{Type: "content_block_stop", Raw: mustJSON(map[string]any{"type": "content_block_stop", "index": 0})})
					blockOpen = false
				}
				pendingStop = finishToStopReason(*choice.FinishReason)
			}
		}
		if chunk.Usage != nil {
			outputTokens = chunk.Usage.CompletionTokens
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		send([]byte(payload))
	}
	flushFinish()
	if err := sc.Err(); err != nil {
		emit(anthropic.Event{Type: "error", Raw: mustJSON(map[string]any{
			"type": "error", "error": map[string]string{"type": "api_error", "message": err.Error()},
		})})
	}
}

func finishToStopReason(finish string) string {
	switch finish {
	case "stop":
		return "end_turn"
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

// chatResponseToAnthropic 非流式响应 → canonical。
func chatResponseToAnthropic(data []byte) (*anthropic.Response, error) {
	var resp struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			PromptDetails    *struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse chat response: %w", err)
	}
	out := &anthropic.Response{
		ID: resp.ID, Type: "message", Role: "assistant", Model: resp.Model,
		Content: []anthropic.ContentBlock{}, Extra: map[string]json.RawMessage{},
	}
	if len(resp.Choices) > 0 {
		msg := resp.Choices[0].Message
		if msg.Content != "" {
			out.Content = append(out.Content, anthropic.ContentBlock{Type: "text", Text: msg.Content})
		}
		for _, tc := range msg.ToolCalls {
			input := json.RawMessage(tc.Function.Arguments)
			if len(input) == 0 || string(input) == "" {
				input = json.RawMessage("{}")
			}
			out.Content = append(out.Content, anthropic.ContentBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: input})
		}
		out.StopReason = finishToStopReason(resp.Choices[0].FinishReason)
	}
	if resp.Usage != nil {
		cached := int64(0)
		if resp.Usage.PromptDetails != nil {
			cached = resp.Usage.PromptDetails.CachedTokens
		}
		input := resp.Usage.PromptTokens - cached
		if input < 0 {
			input = 0
		}
		out.Usage = anthropic.Usage{InputTokens: input, OutputTokens: resp.Usage.CompletionTokens, CacheReadInputTokens: cached}
	}
	return out, nil
}

func (a *Adapter) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamError(resp.StatusCode, resp.Status, data)
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse model list: %w", err)
	}
	if envelope.Data == nil {
		// 智谱 bigmodel 等上游把鉴权/业务错误包在 HTTP 200 里返回，
		// body 形如 {"code":1001,"msg":"...","success":false}，没有 data 字段。
		if msg := bigmodelErrorMessage(data); msg != "" {
			return nil, bigmodelUpstreamError(data, msg)
		}
		return nil, fmt.Errorf("upstream response missing data field: %s", snipBody(data, 200))
	}
	out := make([]provider.ModelInfo, 0, len(envelope.Data))
	for _, m := range envelope.Data {
		out = append(out, provider.ModelInfo{ID: m.ID})
	}
	return out, nil
}

func (a *Adapter) NormalizeError(err error) provider.ErrorKind { return provider.KindOf(err) }

func upstreamError(status int, statusText string, body []byte) error {
	snip := strings.TrimSpace(string(body))
	if len(snip) > 512 {
		snip = snip[:512] + "…"
	}
	var ue provider.UpstreamError
	ue.StatusCode, ue.Status, ue.Body = status, statusText, snip
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		ue.Kind = provider.ErrKindAuth
	case status == http.StatusTooManyRequests:
		ue.Kind = provider.ErrKindQuota
	case status >= 500:
		ue.Kind = provider.ErrKindUpstream
	case status >= 400:
		ue.Kind = provider.ErrKindBadRequest
	default:
		ue.Kind = provider.ErrKindUpstream
	}
	return &ue
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// snipBody 截断上游响应体用于错误信息。
func snipBody(body []byte, limit int) string {
	snip := strings.TrimSpace(string(body))
	if len(snip) > limit {
		snip = snip[:limit] + "…"
	}
	return snip
}

// bigmodelErrorMessage 识别智谱 bigmodel 包在 HTTP 200 里的错误信封
// （{"code":xxx,"msg":"...","success":false}），返回其中的 msg；非该格式返回空串。
func bigmodelErrorMessage(body []byte) string {
	var env struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Success *bool  `json:"success"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return ""
	}
	if env.Code == 0 || env.Msg == "" {
		return ""
	}
	// success 字段存在且为 true 时不是错误
	if env.Success != nil && *env.Success {
		return ""
	}
	return env.Msg
}

// bigmodelUpstreamError 把智谱错误信封转成 UpstreamError，code 尽量映射到错误类别。
func bigmodelUpstreamError(body []byte, msg string) error {
	var env struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(body, &env)
	ue := &provider.UpstreamError{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       snipBody(body, 512),
	}
	switch {
	case env.Code == 401 || env.Code == 1001:
		// 401 未授权；1001 缺少/无效 Authorization
		ue.Kind = provider.ErrKindAuth
	case env.Code == 429:
		ue.Kind = provider.ErrKindQuota
	default:
		ue.Kind = provider.ErrKindUpstream
	}
	// msg 已经包含在 Body 里，无需额外包装
	_ = msg
	return ue
}
