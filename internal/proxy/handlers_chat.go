package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/protocol/convert"
	"github.com/werbenhu/one-proxy/internal/protocol/openaichat"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
)

// ChatCompletions OpenAI Chat 入口：请求 → canonical → 渠道适配器 → 响应/SSE
// 转回 chat 格式。数据流遵循 plan.md §4：handler 保留原始 body，路由选定
// 渠道后才做转换。
func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeChatError(w, http.StatusUnauthorized, "invalid_api_key", "本地代理密钥校验失败")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败")
		return
	}
	if len(body) > MaxBodyBytes {
		writeChatError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "请求体过大")
		return
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Model == "" {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}
	model, direct := resolveModel(probe.Model)
	_ = direct

	req, err := convert.ChatToAnthropic(body)
	if err != nil {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "解析请求失败: "+err.Error())
		return
	}
	req.Header = chatForwardHeaders(r)
	req.Model = model

	if req.Stream {
		events, err := h.router.Stream(r.Context(), req, req.Model)
		if err != nil {
			h.writeChatUpstreamError(w, err)
			return
		}
		h.serveChatStream(w, events)
		return
	}
	resp, err := h.router.Invoke(r.Context(), req, req.Model)
	if err != nil {
		h.writeChatUpstreamError(w, err)
		return
	}
	chatResp := convert.AnthropicToChatResponse(resp)
	writeJSON(w, http.StatusOK, chatCompletionEnvelope(chatResp))
}

// chatCompletionEnvelope 包装成 OpenAI 标准响应结构（choices[]）。
func chatCompletionEnvelope(c *openaichat.ChatResponse) map[string]any {
	msg := map[string]any{"role": "assistant", "content": c.Content}
	if len(c.ToolCalls) > 0 {
		msg["tool_calls"] = c.ToolCalls
	}
	return map[string]any{
		"id":      c.ID,
		"object":  "chat.completion",
		"created": c.Created,
		"model":   c.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       msg,
			"finish_reason": c.FinishReason,
		}},
		"usage": c.Usage,
	}
}

// serveChatStream canonical 事件 → chat chunk SSE。
func (h *Handler) serveChatStream(w http.ResponseWriter, events <-chan anthropic.Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	write := func(payload string) bool {
		if _, err := io.WriteString(w, "data: "+payload+"\n\n"); err != nil {
			return false
		}
		if fl != nil {
			fl.Flush()
		}
		return true
	}
	for ev := range events {
		if ev.Type == "error" {
			// 上游错误事件原样转成 chat error chunk
			if !write(string(ev.Raw)) {
				return
			}
			continue
		}
		chunk := convert.AnthropicToChatChunk(ev)
		if chunk == nil {
			continue
		}
		data, err := json.Marshal(chunk)
		if err != nil {
			continue
		}
		if !write(string(data)) {
			return
		}
	}
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
}

// chatForwardHeaders chat 入口无 anthropic 头，透传 openai 侧可转发头（当前
// 无白名单项，预留）。
func chatForwardHeaders(r *http.Request) http.Header { return http.Header{} }

func (h *Handler) writeChatUpstreamError(w http.ResponseWriter, err error) {
	var nc *router.ErrNoCandidates
	if errors.As(err, &nc) {
		writeChatError(w, http.StatusServiceUnavailable, "server_error", nc.Error())
		return
	}
	var ue *provider.UpstreamError
	if errors.As(err, &ue) {
		status := ue.StatusCode
		if status == 0 {
			status = http.StatusBadGateway
		}
		writeChatError(w, status, "api_error", ue.Error())
		return
	}
	writeChatError(w, http.StatusBadGateway, "api_error", err.Error())
}

func writeChatError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"type": errType, "message": message},
	})
}
