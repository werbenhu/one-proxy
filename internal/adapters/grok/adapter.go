// Package grokadapter xAI 适配器：OAuth 自动刷新 + Responses 上游 +
// 三入口一跳直通（复用移植自 grok-proxy 的 conversation 转换器）。
package grokadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/werbenhu/one-proxy/internal/grok/auth"
	"github.com/werbenhu/one-proxy/internal/grok/conversation"
	"github.com/werbenhu/one-proxy/internal/grok/upstream"
	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/provider"
)

// ExtraJSON is the private OAuth state stored on ProviderAccount.Extra.
type ExtraJSON struct {
	Mode         string `json:"mode"` // api_key | oauth
	AccessToken  string `json:"accessToken,omitempty"`
	RefreshToken string `json:"refreshToken,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"` // RFC3339
}

// tokenStore 适配 config 渠道 Extra 的 TokenStore 实现。
type tokenStore struct {
	mu        sync.Mutex
	getExtra  func() []byte
	saveExtra func([]byte) error
}

func (t *tokenStore) OAuth() auth.OAuth {
	var e ExtraJSON
	_ = json.Unmarshal(t.getExtra(), &e)
	out := auth.OAuth{AccessToken: e.AccessToken, RefreshToken: e.RefreshToken}
	if e.ExpiresAt != "" {
		_ = out.ExpiresAt.UnmarshalText([]byte(e.ExpiresAt))
	}
	return out
}

func (t *tokenStore) SaveOAuth(o auth.OAuth) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var e ExtraJSON
	_ = json.Unmarshal(t.getExtra(), &e)
	e.AccessToken, e.RefreshToken = o.AccessToken, o.RefreshToken
	e.ExpiresAt = o.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return t.saveExtra(data)
}

func (t *tokenStore) InvalidateOAuth() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	var e ExtraJSON
	_ = json.Unmarshal(t.getExtra(), &e)
	e.AccessToken, e.RefreshToken, e.ExpiresAt = "", "", ""
	data, _ := json.Marshal(e)
	return t.saveExtra(data)
}

// Adapter grok 渠道适配器。
type Adapter struct {
	http       *http.Client
	creds      upstream.CredentialSource
	apiKey     string
	client     *upstream.Client
	oauthStore *tokenStore
	onRefresh  func() // 测试钩子
}

// New apiKey 模式。baseURL 为空用官方默认（测试注入假上游）；
// proxyURL 为该提供商专用 HTTP 代理（空走系统环境代理）。
func New(apiKey, baseURL, proxyURL string) *Adapter {
	a := &Adapter{apiKey: apiKey}
	a.http = provider.Client(proxyURL)
	a.creds = staticCredential{token: apiKey}
	a.client = upstream.NewClient(a.http, a.creds)
	a.client.SetBaseURLs(baseURL, baseURL)
	return a
}

// NewOAuth OAuth 模式：extra 读写由回调提供（配置层注入，token 刷新落配置）。
func NewOAuth(baseURL, proxyURL string, getExtra func() []byte, saveExtra func([]byte) error, onStatus func(error)) *Adapter {
	a := &Adapter{}
	a.http = provider.Client(proxyURL)
	store := &tokenStore{getExtra: getExtra, saveExtra: saveExtra}
	a.oauthStore = store
	source := auth.NewSource(oauthRefresher{client: a.http}, store)
	a.creds = oauthCredential{source: source, onStatus: onStatus}
	a.client = upstream.NewClient(a.http, a.creds)
	a.client.SetBaseURLs(baseURL, baseURL)
	return a
}

type staticCredential struct{ token string }

func (s staticCredential) Authorization(ctx context.Context) (upstream.Authorization, error) {
	return upstream.Authorization{Mode: upstream.ModeAPIKey, Token: s.token}, nil
}

type oauthCredential struct {
	source   *auth.Source
	onStatus func(error)
}

func (o oauthCredential) Authorization(ctx context.Context) (upstream.Authorization, error) {
	token, err := o.source.AccessToken(ctx)
	if err != nil {
		if o.onStatus != nil {
			o.onStatus(err)
		}
		return upstream.Authorization{}, err
	}
	return upstream.Authorization{Mode: upstream.ModeOAuth, Token: token}, nil
}

type oauthRefresher struct{ client *http.Client }

func (r oauthRefresher) Refresh(ctx context.Context, refreshToken string) (auth.Token, error) {
	client := auth.NewOAuthClient(r.client)
	return client.Refresh(ctx, refreshToken)
}

// ForwardRaw 直通：入口协议原文 → conversation 转换 → Responses 上游。
// 返回上游原始响应（proxy 层按入口协议转回后写回客户端）。
func (a *Adapter) ForwardRaw(ctx context.Context, protocol provider.Protocol, body []byte, header http.Header, stream bool) (*provider.RawResult, error) {
	var operation string
	switch protocol {
	case provider.ProtocolChat:
		operation = conversation.OperationChat
	case provider.ProtocolResponses:
		operation = "" // Responses → Responses 只改 model
	case provider.ProtocolAnthropic:
		operation = conversation.OperationMessages
	default:
		return nil, fmt.Errorf("grok passthrough does not support protocol %q", protocol)
	}
	converted, _, err := conversation.ConvertRequestWithOptions(body, "", operation)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Responses(ctx, converted, stream)
	if err != nil {
		return nil, a.normalize(err)
	}
	return &provider.RawResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body}, nil
}

func (a *Adapter) SupportedProtocols() []provider.Protocol {
	return []provider.Protocol{provider.ProtocolChat, provider.ProtocolResponses, provider.ProtocolAnthropic}
}

// Invoke canonical（Anthropic）→ messages 转换 → Responses → messages 响应。
func (a *Adapter) Invoke(ctx context.Context, req *anthropic.Request) (*anthropic.Response, error) {
	body, err := anthropic.WriteRequestBytes(req)
	if err != nil {
		return nil, err
	}
	converted, _, err := conversation.ConvertRequestWithOptions(body, req.Model, conversation.OperationMessages)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Responses(ctx, converted, false)
	if err != nil {
		return nil, a.normalize(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out, err := conversation.ConvertResponseJSON(data, conversation.OperationMessages)
	if err != nil {
		return nil, err
	}
	return anthropic.ParseResponse(out)
}

// Stream canonical 流式：事件流以 canonical Anthropic 事件形态重建。
func (a *Adapter) Stream(ctx context.Context, req *anthropic.Request) (<-chan anthropic.Event, error) {
	body, err := anthropic.WriteRequestBytes(req)
	if err != nil {
		return nil, err
	}
	converted, _, err := conversation.ConvertRequestWithOptions(body, req.Model, conversation.OperationMessages)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Responses(ctx, converted, true)
	if err != nil {
		return nil, a.normalize(err)
	}
	events := make(chan anthropic.Event, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		// Responses SSE → messages SSE（grok-proxy 转换器），再解析为 canonical 事件
		converted := conversation.ConvertResponseStream(resp.Body, conversation.OperationMessages)
		scanSSE(converted, func(data []byte) {
			events <- anthropic.ParseSSEEvent(data)
		})
	}()
	return events, nil
}

func (a *Adapter) Models(ctx context.Context) ([]provider.ModelInfo, error) {
	resp, err := a.client.Models(ctx)
	if err != nil {
		return nil, a.normalize(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	var envelope struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("parse grok model list: %w", err)
	}
	out := make([]provider.ModelInfo, 0, len(envelope.Data))
	for _, m := range envelope.Data {
		out = append(out, provider.ModelInfo{ID: m.ID})
	}
	return out, nil
}

func (a *Adapter) NormalizeError(err error) provider.ErrorKind { return provider.KindOf(err) }

// normalize grok 上游错误 → UpstreamError（保留刷新失效语义）。
func (a *Adapter) normalize(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "重新授权") || strings.Contains(err.Error(), "reauthorization") {
		return &provider.UpstreamError{Kind: provider.ErrKindAuth, StatusCode: 401, Body: err.Error()}
	}
	var he *upstream.HTTPError
	if he2, ok := err.(*upstream.HTTPError); ok {
		he = he2
	}
	if he != nil {
		kind := provider.ErrKindUpstream
		switch {
		case he.StatusCode == 401 || he.StatusCode == 403:
			kind = provider.ErrKindAuth
		case he.StatusCode == 429:
			kind = provider.ErrKindQuota
		case he.StatusCode >= 400:
			kind = provider.ErrKindBadRequest
		}
		return &provider.UpstreamError{Kind: kind, StatusCode: he.StatusCode, Body: string(he.Body[:min(len(he.Body), 512)])}
	}
	return &provider.UpstreamError{Kind: provider.ErrKindUpstream, Body: err.Error()}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// AccessToken 返回当前 OAuth access token（自动刷新）；API Key 模式下报错。
// 供 service 层查询订阅额度（cli-chat-proxy /billing）。
func (a *Adapter) AccessToken(ctx context.Context) (string, error) {
	oc, ok := a.creds.(oauthCredential)
	if !ok {
		return "", fmt.Errorf("current Grok provider is in API key mode, no OAuth credentials")
	}
	return oc.source.AccessToken(ctx)
}

// StartDeviceAuth 发起设备授权。
func (a *Adapter) StartDeviceAuth(ctx context.Context) (provider.DeviceAuthInfo, error) {
	client := auth.NewOAuthClient(a.http)
	da, err := client.Start(ctx)
	if err != nil {
		return provider.DeviceAuthInfo{}, err
	}
	return provider.DeviceAuthInfo{
		DeviceCode: da.DeviceCode, UserCode: da.UserCode,
		VerificationURI: da.VerificationURI, VerificationURIComplete: da.VerificationURIComplete,
		ExpiresInSeconds: int(da.ExpiresIn.Seconds()), IntervalSeconds: da.IntervalSeconds,
	}, nil
}

// PollDeviceAuth 轮询授权结果（成功后写回 extra）。
func (a *Adapter) PollDeviceAuth(ctx context.Context, deviceCode string) error {
	if a.oauthStore == nil {
		return fmt.Errorf("current Grok provider is not configured for OAuth mode")
	}
	client := auth.NewOAuthClient(a.http)
	token, err := client.Poll(ctx, deviceCode)
	if err != nil {
		return err
	}
	return a.oauthStore.SaveOAuth(auth.OAuth{AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, ExpiresAt: token.ExpiresAt})
}

func errorEvent(err error) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type": "error", "error": map[string]string{"type": "api_error", "message": err.Error()},
	})
	return b
}

// scanSSE 与 anthropiccompat 相同的行解析（本地副本避免跨适配器依赖）。
func scanSSE(r io.Reader, onData func([]byte)) {
	buf := make([]byte, 0, 64*1024)
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			for {
				idx := strings.Index(string(buf), "\n")
				if idx < 0 {
					break
				}
				line := strings.TrimSpace(string(buf[:idx]))
				buf = buf[idx+1:]
				if strings.HasPrefix(line, "data:") {
					payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
					if payload != "" && payload != "[DONE]" {
						onData([]byte(payload))
					}
				}
			}
		}
		if err != nil {
			return
		}
	}
}
