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

// directTarget 取 URL 路径首段的强制路由目标（/<id>/v1/... 注册形式）；
// 普通请求返回空串。
func directTarget(r *http.Request) string { return r.PathValue("target") }
