package proxy

import (
	"encoding/json"
	"net/http"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
)

type Handler struct {
	store    *config.Store
	registry *provider.Registry
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "invalid_api_key", "本地代理密钥校验失败")
		return
	}
	seen := map[string]bool{}
	var data []map[string]any
	for _, ch := range h.store.Get().Channels {
		if !ch.Enabled {
			continue
		}
		for _, m := range ch.Models {
			if seen[m] {
				continue
			}
			seen[m] = true
			data = append(data, map[string]any{
				"id": m, "object": "model", "created": 0, "owned_by": ch.ID,
			})
		}
	}
	if data == nil {
		data = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
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
