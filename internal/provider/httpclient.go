package provider

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultResponseHeaderTimeout 给大模型上游留出足够的排队和首 token 时间。
// 旧值 60 秒会把仍在正常推理的长上下文请求误判为网络故障。
const DefaultResponseHeaderTimeout = 300 * time.Second

// NormalizeProxyURL 规范化用户输入的代理地址，修复可识别的拼写偏差：
// 缺斜杠的 http:/host:port、缺协议的 //host:port、裸 host:port。
// 无法识别的格式原样返回（由调用方校验或忽略）。
func NormalizeProxyURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return value
	}
	if scheme, rest, ok := strings.Cut(value, ":"); ok {
		switch strings.ToLower(scheme) {
		case "http", "https", "socks5":
			if strings.HasPrefix(rest, "//") {
				return value // 已是规范形式
			}
			// http:/host:port / http:host:port → http://host:port
			return scheme + "://" + strings.TrimPrefix(rest, "/")
		}
	}
	if strings.HasPrefix(value, "//") {
		return "http:" + value
	}
	if host, port, err := net.SplitHostPort(value); err == nil && host != "" && port != "" {
		return "http://" + value
	}
	return value
}

// parseProxyURL 解析代理地址并要求得到 scheme+host；不规范输入返回 nil。
func parseProxyURL(raw string) *url.URL {
	u, err := url.Parse(NormalizeProxyURL(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil
	}
	return u
}

// ValidProxyURL 判断（经规范化后）能否解析出 scheme+host 的代理地址，
// 供保存设置时校验用户输入。
func ValidProxyURL(raw string) bool {
	return parseProxyURL(raw) != nil
}

// Transport 构造出站 Transport：proxyURL 非空且合法时走该代理（http/https/socks5），
// 否则回退到环境变量代理。供各上游适配器与余额查询共用。
func Transport(proxyURL string) *http.Transport {
	return TransportWithResponseHeaderTimeout(proxyURL, DefaultResponseHeaderTimeout)
}

// TransportWithResponseHeaderTimeout 与 Transport 相同，但允许提供商覆盖等待响应头的时长。
// 非正值回退到适合 LLM 请求的默认值。
func TransportWithResponseHeaderTimeout(proxyURL string, responseHeaderTimeout time.Duration) *http.Transport {
	if responseHeaderTimeout <= 0 {
		responseHeaderTimeout = DefaultResponseHeaderTimeout
	}
	t := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
	if u := parseProxyURL(proxyURL); u != nil {
		t.Proxy = http.ProxyURL(u)
	}
	return t
}

// Client 返回使用指定代理的 HTTP 客户端（proxyURL 语义同 Transport）。
func Client(proxyURL string) *http.Client {
	return &http.Client{Transport: Transport(proxyURL)}
}

// ClientWithResponseHeaderTimeout 创建带可配置响应头超时的客户端。
func ClientWithResponseHeaderTimeout(proxyURL string, responseHeaderTimeout time.Duration) *http.Client {
	return &http.Client{Transport: TransportWithResponseHeaderTimeout(proxyURL, responseHeaderTimeout)}
}
