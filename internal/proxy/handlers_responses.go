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

// Responses OpenAI Responses 入口。V1 仅支持实现了 ForwardRaw 的适配器
// （grok）：原始 body 直通上游，不做通用转换（plan.md §9）。
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

	targets, err := h.router.Resolve(probe.Model)
	if err != nil {
		h.writeChatUpstreamError(w, err)
		return
	}
	// 直通候选：第一个支持 responses 协议的可用渠道
	var chosen *router.Target
	var reasons []string
	for i := range targets {
		t := targets[i]
		if !h.registry.Available(t.Channel.ID) {
			continue
		}
		raw, ok := t.Adapter.(provider.RawForwardCapable)
		if !ok || !supportsProtocol(raw, provider.ProtocolResponses) {
			continue
		}
		chosen = &targets[i]
		break
	}
	if chosen == nil {
		if len(reasons) == 0 {
			reasons = append(reasons, "没有支持 responses 协议的可用渠道（V1 仅 grok 渠道支持）")
		}
		writeChatError(w, http.StatusServiceUnavailable, "server_error", strings.Join(reasons, "；"))
		return
	}
	raw := chosen.Adapter.(provider.RawForwardCapable)
	// 直通路径的 model 重写：raw JSON 的 model 字段（plan.md §4）
	fwdBody := body
	if chosen.UpstreamModel != probe.Model {
		fwdBody, err = rewriteModel(body, chosen.UpstreamModel)
		if err != nil {
			writeChatError(w, http.StatusBadRequest, "invalid_request_error", "重写 model 失败")
			return
		}
	}
	result, err := raw.ForwardRaw(r.Context(), provider.ProtocolResponses, fwdBody, nil, probe.Stream)
	if err != nil {
		h.applyDirectFailure(*chosen, err)
		h.writeChatUpstreamError(w, err)
		return
	}
	defer result.Body.Close()
	// 上游响应原样回写（responses 协议恒等）
	ct := result.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(result.StatusCode)
	_, _ = io.Copy(w, result.Body)
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
		h.registry.SetCooling(t.Channel.ID, time.Now().Add(5*time.Minute), "429 配额/限速")
	case provider.ErrKindAuth:
		h.registry.SetAuthFailed(t.Channel.ID, "上游 401/403")
	}
}

// rewriteModel 替换 raw JSON 的 model 字段（直通路径的模型重写）。
func rewriteModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["model"] = []byte(`"` + model + `"`)
	return json.Marshal(m)
}
