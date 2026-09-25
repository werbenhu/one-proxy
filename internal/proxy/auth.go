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
