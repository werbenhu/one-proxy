package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
)

// Responses forwards to an upstream adapter that supports the Responses API.
func (h *Handler) Responses(w http.ResponseWriter, r *http.Request) {
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
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Model == "" {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}

	var targets []router.Target
	if direct := directTarget(r); direct != "" {
		targets, err = h.router.ResolveDirect(direct, probe.Model)
	} else {
		targets, err = h.router.Resolve(probe.Model)
	}
	if err != nil {
		h.writeChatUpstreamError(w, err)
		return
	}
	var reasons []string
	for _, t := range targets {
		if !h.registry.Available(t.Provider.ID) {
			reasons = append(reasons, t.Provider.Name+" 正在冷却或鉴权失败")
			continue
		}
		raw, ok := t.Adapter.(provider.RawForwardCapable)
		if !ok || !supportsProtocol(raw, provider.ProtocolResponses) {
			reasons = append(reasons, t.Provider.Name+" 不支持 Responses")
			continue
		}
		fwdBody := body
		if t.UpstreamModel != probe.Model {
			fwdBody, err = rewriteModel(body, t.UpstreamModel)
			if err != nil {
				writeChatError(w, http.StatusBadRequest, "invalid_request_error", "重写 model 失败")
				return
			}
		}
		result, callErr := raw.ForwardRaw(r.Context(), provider.ProtocolResponses, fwdBody, nil, probe.Stream)
		if callErr != nil {
			h.applyDirectFailure(t, callErr)
			if !provider.MaySwitch(callErr) {
				h.writeChatUpstreamError(w, callErr)
				return
			}
			reasons = append(reasons, t.Provider.Name+"："+callErr.Error())
			continue
		}
		defer result.Body.Close()
		ct := result.Header.Get("Content-Type")
		if ct == "" {
			ct = "application/json"
		}
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(result.StatusCode)
		_, _ = io.Copy(w, result.Body)
		return
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "没有支持 Responses 协议的可用提供商")
	}
	writeChatError(w, http.StatusServiceUnavailable, "server_error", strings.Join(reasons, "；"))
}

func supportsProtocol(a provider.RawForwardCapable, p provider.Protocol) bool {
	for _, sp := range a.SupportedProtocols() {
		if sp == p {
			return true
		}
	}
	return false
}

func (h *Handler) applyDirectFailure(t router.Target, err error) {
	switch provider.KindOf(err) {
	case provider.ErrKindQuota:
		h.registry.SetCooling(t.Provider.ID, time.Now().Add(5*time.Minute), "429 配额/限速")
	case provider.ErrKindAuth:
		h.registry.SetAuthFailed(t.Provider.ID, "上游 401/403")
	}
}

// rewriteModel 替换 raw JSON 的 model 字段（直通路径的模型重写）。
func rewriteModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	m["model"] = encoded
	return json.Marshal(m)
}
