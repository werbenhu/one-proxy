package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
)

// Messages Anthropic 入口。数据流（plan.md §4）：鉴权 → 读原始 body →
// 解析 model → router 路由（含直连语法/主备/切换）→ 转发。
func (h *Handler) Messages(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_api_key", "本地代理密钥校验失败")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败")
		return
	}
	if len(body) > MaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "请求体过大")
		return
	}
	var probe struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Model == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}

	req, err := anthropic.ParseRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "解析请求失败: "+err.Error())
		return
	}
	req.Header = whitelistHeaders(r)
	direct := directTarget(r)

	if req.Stream {
		var events <-chan anthropic.Event
		if direct != "" {
			events, err = h.router.StreamDirect(r.Context(), req, req.Model, direct)
		} else {
			events, err = h.router.Stream(r.Context(), req, req.Model)
		}
		if err != nil {
			h.writeUpstreamError(w, err)
			return
		}
		h.serveAnthropicStream(w, events)
		return
	}
	requestedModel := req.Model
	var resp *anthropic.Response
	if direct != "" {
		resp, err = h.router.InvokeDirect(r.Context(), req, requestedModel, direct)
	} else {
		resp, err = h.router.Invoke(r.Context(), req, requestedModel)
	}
	if err != nil {
		h.writeUpstreamError(w, err)
		return
	}
	out, err := anthropic.WriteResponseBytes(resp)
	if err != nil {
		writeError(w, http.StatusBadGateway, "api_error", "序列化响应失败")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (h *Handler) serveAnthropicStream(w http.ResponseWriter, events <-chan anthropic.Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	for ev := range events {
		if err := anthropic.SSEWrite(w, ev); err != nil {
			return
		}
		if fl != nil {
			fl.Flush()
		}
	}
}

// forwardedClientHeaders 客户端身份头：随请求透传给上游（中转不改变来源）。
var forwardedClientHeaders = []string{"User-Agent", "X-Api-Source", "X-Title", "Http-X-Title"}

// whitelistHeaders 提取白名单头进 canonical header bag。
func whitelistHeaders(r *http.Request) http.Header {
	out := http.Header{}
	for _, name := range []string{"anthropic-beta", "anthropic-version"} {
		if v := r.Header.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	for _, name := range forwardedClientHeaders {
		if v := r.Header.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}

func (h *Handler) writeUpstreamError(w http.ResponseWriter, err error) {
	var nc *router.ErrNoCandidates
	if errors.As(err, &nc) {
		writeError(w, http.StatusServiceUnavailable, "overloaded_error", nc.Error())
		return
	}
	if errors.Is(err, router.ErrModelNotDeclared) {
		writeError(w, http.StatusNotFound, "invalid_request_error", err.Error())
		return
	}
	var ue *provider.UpstreamError
	if errors.As(err, &ue) {
		status := ue.StatusCode
		if status == 0 {
			status = http.StatusBadGateway
		}
		writeError(w, status, "api_error", ue.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "api_error", err.Error())
}
