package provider

import (
	"net/http"
	"testing"
	"time"
)

func TestTransportUsesLLMFriendlyDefaultTimeout(t *testing.T) {
	if DefaultResponseHeaderTimeout != 300*time.Second {
		t.Fatalf("默认常量 = %s, want 300s", DefaultResponseHeaderTimeout)
	}
	tr := Transport("")
	if tr.ResponseHeaderTimeout != DefaultResponseHeaderTimeout {
		t.Fatalf("默认响应头超时 = %s, want 300s", tr.ResponseHeaderTimeout)
	}
}

func TestTransportUsesConfiguredResponseHeaderTimeout(t *testing.T) {
	tr := TransportWithResponseHeaderTimeout("", 7*time.Minute)
	if tr.ResponseHeaderTimeout != 7*time.Minute {
		t.Fatalf("自定义响应头超时 = %s, want 7m", tr.ResponseHeaderTimeout)
	}
}

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
