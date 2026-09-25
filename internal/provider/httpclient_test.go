package provider

import (
	"net/http"
	"testing"
)

func TestTransportUsesProxyURL(t *testing.T) {
	tr := Transport("http://127.0.0.1:7890")
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.Host != "127.0.0.1:7890" {
		t.Fatalf("应使用指定代理: %v", u)
	}
}

func TestTransportFallsBackOnInvalidProxy(t *testing.T) {
	for _, raw := range []string{"", ":::bad", "noscheme"} {
		tr := Transport(raw)
		if tr.Proxy == nil {
			t.Fatalf("代理为空时应回退环境代理: %q", raw)
		}
	}
}
