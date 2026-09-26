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

func (a *Adapter) ForwardRaw(ctx context.Context, protocol provider.Protocol, body []byte, _ http.Header, stream bool) (*provider.RawResult, error) {
	if protocol != provider.ProtocolResponses {
		return nil, fmt.Errorf("OpenAI-compatible provider does not support passthrough protocol %q", protocol)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	req.Header.Set("Content-Type", "application/json")
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
