package proxy

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// checkKey 常数时间比较密钥，避免时序侧信道。
func checkKey(r *http.Request, key string) bool {
	got := r.Header.Get("Authorization")
	if strings.HasPrefix(got, "Bearer ") {
		got = strings.TrimPrefix(got, "Bearer ")
	} else {
		got = r.Header.Get("x-api-key")
	}
	got = strings.TrimSpace(got)
	return len(got) == len(key) && subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1
}

// resolveModel 返回 (模型名, 是否直连语法)。直连 ch-<id>/<model> 由 router
// 解析；这里只拆出模型名给 canonical 转换前的 probe 用。
func resolveModel(raw string) (model, directChannel string) {
	if strings.HasPrefix(raw, "ch-") {
		if idx := strings.Index(raw, "/"); idx > 0 {
			return raw[idx+1:], raw[:idx]
		}
	}
	return raw, ""
}
