package proxy

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
)

type Handler struct {
	store    *config.Store
	registry *provider.Registry
	router   *router.Router
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_api_key", "本地代理密钥校验失败")
		return
	}
	h.listDirectModels(w, r, directTarget(r))
}

// MissingChannel 无渠道 ID 的入口：引导客户端改用 /<渠道ID>/v1/... 形式。
func (h *Handler) MissingChannel(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusBadRequest, "invalid_request_error",
		"URL 缺少渠道 ID：请使用 /<渠道ID>"+r.URL.Path+"（如 /glm"+r.URL.Path+"）")
}

// listDirectModels 路径直连的模型列表：渠道只暴露其对外模型（通配渠道为空列表），
// 提供商则实时拉取上游模型。
func (h *Handler) listDirectModels(w http.ResponseWriter, r *http.Request, target string) {
	cfg := h.store.Get()
	data := []map[string]any{}
	if ch, found := cfg.Channel(target); found {
		if ch.Enabled && strings.TrimSpace(ch.Model) != "" {
			data = append(data, map[string]any{"id": ch.Model, "object": "model", "created": 0, "owned_by": ch.ID})
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
		return
	}
	if p, found := cfg.Provider(target); found && p.Enabled {
		if adapter, ok := h.registry.Get(p.ID); ok {
			if models, err := adapter.Models(r.Context()); err == nil {
				for _, m := range models {
					data = append(data, map[string]any{"id": m.ID, "object": "model", "created": 0, "owned_by": p.ID})
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
		return
	}
	writeError(w, http.StatusNotFound, "invalid_request_error", "直连目标 "+target+" 不存在或未启用")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 按 Anthropic 错误格式输出（三种入口共用；chat 入口由
// handlers_chat 覆写格式）。
func writeError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, map[string]any{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
}

func (h *Handler) authorized(r *http.Request) bool {
	key := h.key()
	if key == "" {
		return false
	}
	return checkKey(r, key)
}

func (h *Handler) key() string { return h.store.Get().LocalKey }

var _ = provider.ErrKindNone
