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

func TestNormalizeProxyURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:7897":   "http://127.0.0.1:7897",   // 原样
		"  http://a.b:1  ":        "http://a.b:1",            // 去空白
		"http:127.0.0.1:7897":     "http://127.0.0.1:7897",   // 缺 //
		"http:/127.0.0.1:7897":    "http://127.0.0.1:7897",   // 缺一个 /
		"//127.0.0.1:7897":        "http://127.0.0.1:7897",   // 缺协议
		"127.0.0.1:7897":          "http://127.0.0.1:7897",   // 裸 host:port
		"socks5://127.0.0.1:1080": "socks5://127.0.0.1:1080", // 原样
	}
	for raw, want := range cases {
		if got := NormalizeProxyURL(raw); got != want {
			t.Errorf("NormalizeProxyURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTransportUsesNormalizedProxyURL(t *testing.T) {
	// 用户配置常见笔误：http:127.0.0.1:7897（url.Parse 得到空 Host，曾静默回退直连）
	tr := Transport("http:127.0.0.1:7897")
	req, err := http.NewRequest(http.MethodGet, "https://api.example.com/v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := tr.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.String() != "http://127.0.0.1:7897" {
		t.Fatalf("拼写偏差的代理应规范化后生效: %v", u)
	}
}

func TestValidProxyURL(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:7897", "socks5://h:1", "http:127.0.0.1:7897", "127.0.0.1:7897", "//h:1"} {
		if !ValidProxyURL(raw) {
			t.Errorf("ValidProxyURL(%q) = false, want true", raw)
		}
	}
	for _, raw := range []string{"", "   ", ":::bad", "not a url"} {
		if ValidProxyURL(raw) {
			t.Errorf("ValidProxyURL(%q) = true, want false", raw)
		}
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
