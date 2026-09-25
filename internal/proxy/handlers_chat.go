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

	ch, ok := h.pickChannel(direct, model)
	if !ok {
		writeChatError(w, http.StatusNotFound, "invalid_request_error", "没有渠道声明模型 "+model)
		return
	}
	adapter, ok := h.registry.Get(ch.ID)
	if !ok {
		writeChatError(w, http.StatusBadGateway, "api_error", "渠道 "+ch.ID+" 适配器未注册")
		return
	}

	req, err := convert.ChatToAnthropic(body)
	if err != nil {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "解析请求失败: "+err.Error())
		return
	}
	req.Header = chatForwardHeaders(r)
	if mapped, ok := ch.ModelMapping[model]; ok && direct == "" {
		req.Model = mapped
	} else {
		req.Model = model
	}

	if req.Stream {
		h.serveChatStream(w, r, adapter, req)
		return
	}
	resp, err := adapter.Invoke(r.Context(), req)
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
func (h *Handler) serveChatStream(w http.ResponseWriter, r *http.Request, adapter provider.Adapter, req *anthropic.Request) {
	events, err := adapter.Stream(r.Context(), req)
	if err != nil {
		h.writeChatUpstreamError(w, err)
		return
	}
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
