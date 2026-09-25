package provider

import (
	"net/http"
	"net/url"
	"time"
)

// Transport 构造出站 Transport：proxyURL 非空且合法时走该代理（http/https/socks5），
// 否则回退到环境变量代理。供各上游适配器与余额查询共用。
func Transport(proxyURL string) *http.Transport {
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	if u, err := url.Parse(proxyURL); err == nil && u.Scheme != "" && u.Host != "" {
		t.Proxy = http.ProxyURL(u)
	}
	return t
}

// Client 返回使用指定代理的 HTTP 客户端（proxyURL 语义同 Transport）。
func Client(proxyURL string) *http.Client {
	return &http.Client{Transport: Transport(proxyURL)}
}
