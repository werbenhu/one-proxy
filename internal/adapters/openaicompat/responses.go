package openaicompat

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/werbenhu/one-proxy/internal/provider"
)

// OpenAI-compatible upstreams may expose the Responses endpoint as well.
func (a *Adapter) SupportedProtocols() []provider.Protocol {
	return []provider.Protocol{provider.ProtocolResponses}
}

func (a *Adapter) ForwardRaw(ctx context.Context, protocol provider.Protocol, body []byte, header http.Header, stream bool) (*provider.RawResult, error) {
	if protocol != provider.ProtocolResponses {
		return nil, fmt.Errorf("OpenAI-compatible provider does not support passthrough protocol %q", protocol)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
	applyRawClientHeaders(req, header)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes+1))
		if readErr != nil {
			return nil, readErr
		}
		return nil, upstreamError(resp.StatusCode, resp.Status, data)
	}
	return &provider.RawResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}

// applyRawClientHeaders 直通路径的客户端身份头透传（与 applyClientHeaders 同一白名单）。
func applyRawClientHeaders(httpReq *http.Request, header http.Header) {
	if header == nil {
		return
	}
	for _, h := range []string{"User-Agent", "X-Api-Source", "X-Title", "Http-X-Title"} {
		if v := header.Get(h); v != "" {
			httpReq.Header.Set(h, v)
		}
	}
}
