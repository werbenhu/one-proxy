// Package anthropiccompat 通用 Anthropic 兼容适配器：canonical 请求恒等
// 重序列化后转发（只改 model），响应/SSE 原样归一化为 canonical 事件。
package anthropiccompat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// New 工厂：由 service 按渠道配置构造；proxyURL 为该提供商专用 HTTP 代理（空走系统环境代理）。
func New(baseURL, apiKey, proxyURL string, responseHeaderTimeout ...time.Duration) *Adapter {
	timeout := provider.DefaultResponseHeaderTimeout
	if len(responseHeaderTimeout) > 0 && responseHeaderTimeout[0] > 0 {
		timeout = responseHeaderTimeout[0]
	}
	return &Adapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Transport: provider.TransportWithResponseHeaderTimeout(proxyURL, timeout)},
	}
}

type Adapter struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// forwardedHeaders 白名单：入口请求头随请求走（plan.md §5.1 header bag）。
// User-Agent 属于客户端身份头：透传，让上游看到真实调用方（中转不改变来源）。
var forwardedHeaders = []string{"anthropic-beta", "anthropic-version", "User-Agent"}

const maxErrorBodyBytes = 2 << 20

func (a *Adapter) endpoint() string { return a.baseURL + "/v1/messages" }

func (a *Adapter) buildRequest(ctx context.Context, req *anthropic.Request, body []byte, stream bool) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.endpoint(), strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if stream {
		httpReq.Header.Set("Accept", "text/event-stream")
	} else {
		httpReq.Header.Set("Accept", "application/json")
	}
	for _, h := range forwardedHeaders {
		if req != nil && req.Header != nil {
			if v := req.Header.Get(h); v != "" {
				httpReq.Header.Set(h, v)
			}
		}
	}
	return httpReq, nil
}

func (a *Adapter) Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	body, err := anthropic.WriteRequestBytes(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := a.buildRequest(ctx, req, body, false)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &provider.UpstreamError{
			Kind: classifyStatus(resp.StatusCode), StatusCode: resp.StatusCode,
			Status: resp.Status, Body: snippet(data),
		}
	}
	return anthropic.ParseResponse(data)
}

func (a *Adapter) Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error) {
	body, err := anthropic.WriteRequestBytes(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := a.buildRequest(ctx, req, body, true)
	if err != nil {
		return nil, err
	}
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
		return nil, &provider.UpstreamError{
			Kind: classifyStatus(resp.StatusCode), StatusCode: resp.StatusCode,
			Status: resp.Status, Body: snippet(data),
		}
	}
	events := make(chan anthropic.Event, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		if err := scanSSE(resp.Body, func(data []byte) {
			events <- anthropic.ParseSSEEvent(data)
		}); err != nil {
			events <- anthropic.Event{Type: "error", Raw: mustJSON(map[string]any{
				"type": "error", "error": map[string]string{"type": "api_error", "message": err.Error()},
			})}
		}
	}()
	return events, nil
}

func (a *Adapter) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &provider.UpstreamError{
			Kind: classifyStatus(resp.StatusCode), StatusCode: resp.StatusCode,
			Status: resp.Status, Body: snippet(data),
		}
	}
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse model list: %w", err)
	}
	out := make([]provider.ModelInfo, 0, len(envelope.Data))
	for _, m := range envelope.Data {
		out = append(out, provider.ModelInfo{ID: m.ID})
	}
	return out, nil
}

func (a *Adapter) NormalizeError(err error) provider.ErrorKind { return provider.KindOf(err) }

func classifyStatus(code int) provider.ErrorKind {
	switch {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return provider.ErrKindAuth
	case code == http.StatusTooManyRequests:
		return provider.ErrKindQuota
	case code >= 500:
		return provider.ErrKindUpstream
	case code >= 400:
		return provider.ErrKindBadRequest
	default:
		return provider.ErrKindUpstream
	}
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 512 {
		s = s[:512] + "…"
	}
	return s
}

// scanSSE 按行解析 text/event-stream，每个 data: 载荷回调一次。
func scanSSE(r io.Reader, onData func([]byte)) error {
	br := newLineReader(r)
	for {
		line, err := br.readLine()
		if len(line) > 0 && strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload != "" && payload != "[DONE]" {
				onData([]byte(payload))
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

type lineReader struct {
	r   io.Reader
	buf []byte
	eof bool
}

func newLineReader(r io.Reader) *lineReader { return &lineReader{r: r} }

func (l *lineReader) readLine() (string, error) {
	for {
		if i := indexByte(l.buf, '\n'); i >= 0 {
			line := string(l.buf[:i])
			l.buf = l.buf[i+1:]
			return strings.TrimRight(line, "\r"), nil
		}
		if l.eof {
			if len(l.buf) > 0 {
				line := string(l.buf)
				l.buf = nil
				return strings.TrimRight(line, "\r"), io.EOF
			}
			return "", io.EOF
		}
		chunk := make([]byte, 32*1024)
		n, err := l.r.Read(chunk)
		if n > 0 {
			l.buf = append(l.buf, chunk[:n]...)
		}
		if err != nil {
			l.eof = true
			if err != io.EOF {
				return "", err
			}
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := 0; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
