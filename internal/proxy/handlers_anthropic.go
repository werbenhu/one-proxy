package proxy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// resolveModel 返回 (对外模型名, 直连渠道ID或空)。
// 直连语法：ch-<id>/<model> —— 仅 ch- 前缀才解析，避免拆错 OpenRouter 的 org/model。
func resolveModel(raw string) (model, directChannel string) {
	if strings.HasPrefix(raw, "ch-") {
		if idx := strings.Index(raw, "/"); idx > 0 {
			return raw[idx+1:], raw[:idx]
		}
	}
	return raw, ""
}

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
	model, direct := resolveModel(probe.Model)
	if direct != "" {
		body, err = rewriteModel(body, model)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request_error", "重写 model 失败")
			return
		}
	}

	ch, ok := h.pickChannel(direct, model)
	if !ok {
		writeError(w, http.StatusNotFound, "invalid_request_error", "没有渠道声明模型 "+model)
		return
	}
	adapter, ok := h.registry.Get(ch.ID)
	if !ok {
		writeError(w, http.StatusBadGateway, "api_error", "渠道 "+ch.ID+" 适配器未注册")
		return
	}

	req, err := anthropic.ParseRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "解析请求失败: "+err.Error())
		return
	}
	req.Header = whitelistHeaders(r)

	// ModelMapping 重写（兼任模型别名）
	if mapped, ok := ch.ModelMapping[model]; ok && direct == "" {
		req.Model = mapped
	} else if direct != "" {
		req.Model = model
	} else {
		req.Model = model
	}

	if req.Stream {
		h.serveStream(w, r, adapter, req)
		return
	}
	resp, err := adapter.Invoke(r.Context(), req)
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

func (h *Handler) serveStream(w http.ResponseWriter, r *http.Request, adapter provider.Adapter, req *anthropic.Request) {
	events, err := adapter.Stream(r.Context(), req)
	if err != nil {
		h.writeUpstreamError(w, err)
		return
	}
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

// whitelistHeaders 提取白名单头进 canonical header bag。
func whitelistHeaders(r *http.Request) http.Header {
	out := http.Header{}
	for _, name := range []string{"anthropic-beta", "anthropic-version"} {
		if v := r.Header.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}

// pickChannel M1 简化路由：直连优先，其次第一条声明该模型的启用渠道。
func (h *Handler) pickChannel(direct, model string) (config.Channel, bool) {
	cfg := h.store.Get()
	if direct != "" {
		if ch, ok := cfg.Channel(direct); ok && ch.Enabled {
			return ch, true
		}
		return config.Channel{}, false
	}
	for _, ch := range cfg.Channels {
		if !ch.Enabled {
			continue
		}
		for _, m := range ch.Models {
			if m == model {
				return ch, true
			}
		}
	}
	return config.Channel{}, false
}

func (h *Handler) writeUpstreamError(w http.ResponseWriter, err error) {
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

func rewriteModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["model"] = []byte(`"` + model + `"`)
	return json.Marshal(m)
}
