package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/protocol/convert"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/router"
)

// Responses forwards to an upstream adapter that supports the Responses API.
func (h *Handler) Responses(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeChatError(w, http.StatusUnauthorized, "invalid_api_key", "本地代理密钥校验失败")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败")
		return
	}
	if len(body) > MaxBodyBytes {
		writeChatError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "请求体过大")
		return
	}
	var probe struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.Model == "" {
		writeChatError(w, http.StatusBadRequest, "invalid_request_error", "缺少 model 字段")
		return
	}

	var targets []router.Target
	if direct := directTarget(r); direct != "" {
		targets, err = h.router.ResolveDirect(direct, probe.Model)
	} else {
		targets, err = h.router.Resolve(probe.Model)
	}
	if err != nil {
		h.writeChatUpstreamError(w, err)
		return
	}
	// failed 上报失败并决定是否切换下一候选；返回 false 表示错误已写回客户端。
	failed := func(t router.Target, start time.Time, callErr error, reasons *[]string) bool {
		h.applyDirectFailure(t, callErr)
		h.router.RecordUsage(router.RequestInfo{
			ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
			ModelRequested: probe.Model, ModelUpstream: t.UpstreamModel,
			Status: upstreamStatus(callErr), LatencyMs: time.Since(start).Milliseconds(), Error: callErr.Error(),
		})
		if !provider.MaySwitch(callErr) {
			h.writeChatUpstreamError(w, callErr)
			return false
		}
		*reasons = append(*reasons, t.Provider.Name+"："+callErr.Error())
		return true
	}
	var anthReq *anthropic.Request
	var reasons []string
	for _, t := range targets {
		if !h.registry.Available(t.Provider.ID) {
			reasons = append(reasons, t.Provider.Name+" 正在冷却或鉴权失败")
			continue
		}
		start := time.Now()
		if raw, ok := t.Adapter.(provider.RawForwardCapable); ok && supportsProtocol(raw, provider.ProtocolResponses) {
			fwdBody := body
			if t.UpstreamModel != probe.Model {
				fwdBody, err = rewriteModel(body, t.UpstreamModel)
				if err != nil {
					writeChatError(w, http.StatusBadRequest, "invalid_request_error", "重写 model 失败")
					return
				}
			}
			result, callErr := raw.ForwardRaw(r.Context(), provider.ProtocolResponses, fwdBody, clientHeaders(r), probe.Stream)
			if callErr != nil {
				if !failed(t, start, callErr, &reasons) {
					return
				}
				continue
			}
			ct := result.Header.Get("Content-Type")
			if ct == "" {
				ct = "application/json"
			}
			w.Header().Set("Content-Type", ct)
			w.WriteHeader(result.StatusCode)
			u := forwardResponsesBody(w, result, probe.Stream)
			h.router.RecordUsage(router.RequestInfo{
				ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
				ModelRequested: probe.Model, ModelUpstream: t.UpstreamModel,
				Usage: u, Status: result.StatusCode, LatencyMs: time.Since(start).Milliseconds(),
			})
			return
		}
		// 上游不支持 Responses：走 canonical 转换路径
		if anthReq == nil {
			anthReq, err = convert.ResponsesToAnthropic(body)
			if err != nil {
				writeChatError(w, http.StatusBadRequest, "invalid_request_error", "转换 Responses 请求失败: "+err.Error())
				return
			}
			anthReq.Model = probe.Model
		}
		attempt := *anthReq
		attempt.Model = t.UpstreamModel
		if probe.Stream {
			events, callErr := t.Adapter.Stream(r.Context(), &attempt)
			if callErr != nil {
				if !failed(t, start, callErr, &reasons) {
					return
				}
				continue
			}
			u := serveResponsesStream(w, events)
			h.router.RecordUsage(router.RequestInfo{
				ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
				ModelRequested: probe.Model, ModelUpstream: t.UpstreamModel,
				Usage: u, Status: http.StatusOK, LatencyMs: time.Since(start).Milliseconds(),
			})
			return
		}
		resp, callErr := t.Adapter.Invoke(r.Context(), &attempt)
		if callErr != nil {
			if !failed(t, start, callErr, &reasons) {
				return
			}
			continue
		}
		writeJSON(w, http.StatusOK, convert.AnthropicToResponses(resp))
		h.router.RecordUsage(router.RequestInfo{
			ChannelID: t.Channel.ID, ChannelName: t.Channel.Name, ProviderID: t.Provider.ID,
			ModelRequested: probe.Model, ModelUpstream: t.UpstreamModel,
			Usage: resp.Usage, Status: http.StatusOK, LatencyMs: time.Since(start).Milliseconds(),
		})
		return
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "没有可用的提供商")
	}
	writeChatError(w, http.StatusServiceUnavailable, "server_error", strings.Join(reasons, "；"))
}

// serveResponsesStream 把 canonical 事件流写成 Responses SSE，返回累计用量。
func serveResponsesStream(w http.ResponseWriter, events <-chan anthropic.Event) anthropic.Usage {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fl, _ := w.(http.Flusher)
	conv := convert.NewResponsesStream()
	u := anthropic.Usage{}
	for ev := range events {
		u.MergeEvent(ev)
		for _, out := range conv.Handle(ev) {
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", out.Type, out.Data); err != nil {
				return u
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}
	return u
}

func supportsProtocol(a provider.RawForwardCapable, p provider.Protocol) bool {
	for _, sp := range a.SupportedProtocols() {
		if sp == p {
			return true
		}
	}
	return false
}

// clientHeaders 客户端身份头（直通路径转发给上游）。
func clientHeaders(r *http.Request) http.Header {
	out := http.Header{}
	for _, name := range forwardedClientHeaders {
		if v := r.Header.Get(name); v != "" {
			out.Set(name, v)
		}
	}
	return out
}

func (h *Handler) applyDirectFailure(t router.Target, err error) {
	switch provider.KindOf(err) {
	case provider.ErrKindQuota:
		h.registry.SetCooling(t.Provider.ID, time.Now().Add(5*time.Minute), "429 配额/限速")
	case provider.ErrKindAuth:
		h.registry.SetAuthFailed(t.Provider.ID, "上游 401/403")
	}
}

// rewriteModel 替换 raw JSON 的 model 字段（直通路径的模型重写）。
func rewriteModel(body []byte, model string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(model)
	if err != nil {
		return nil, err
	}
	m["model"] = encoded
	return json.Marshal(m)
}

// forwardResponsesBody 把上游响应写给客户端并统计 token 用量（tee 旁路）。
// 非流式解析响应 JSON 的 usage；流式缓存 SSE（上限 8MB），从
// response.completed/incomplete/failed 终态事件里取 response.usage。
func forwardResponsesBody(w http.ResponseWriter, result *provider.RawResult, stream bool) anthropic.Usage {
	defer result.Body.Close()
	if !stream {
		data, err := io.ReadAll(result.Body)
		if err != nil {
			return anthropic.Usage{}
		}
		_, _ = w.Write(data)
		var parsed struct {
			Usage responsesUsage `json:"usage"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return anthropic.Usage{}
		}
		return parsed.Usage.canonical()
	}
	sniff := &cappedBuffer{max: 8 << 20}
	_, _ = io.Copy(w, io.TeeReader(result.Body, sniff))
	return sniff.sseUsage()
}

// responsesUsage OpenAI Responses 的 usage 结构。
type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

func (u responsesUsage) canonical() anthropic.Usage {
	// OpenAI/GLM 口径：input_tokens 为全量输入（含 cached），cached_tokens 是其子集。
	// 内部记账沿用 Anthropic 口径（互斥），故输入侧要减去缓存读，避免合计时重复计算。
	input := u.InputTokens - u.InputTokensDetails.CachedTokens
	if input < 0 {
		input = 0
	}
	return anthropic.Usage{
		InputTokens:          int64(input),
		OutputTokens:         int64(u.OutputTokens),
		CacheReadInputTokens: int64(u.InputTokensDetails.CachedTokens),
	}
}

// cappedBuffer tee 旁路缓冲：写满 max 后丢弃，但始终报告全部写入。
type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if remain := c.max - c.buf.Len(); remain > 0 {
		if remain > len(p) {
			remain = len(p)
		}
		c.buf.Write(p[:remain])
	}
	return len(p), nil
}

func (c *cappedBuffer) sseUsage() anthropic.Usage {
	var u anthropic.Usage
	for _, line := range bytes.Split(c.buf.Bytes(), []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		var ev struct {
			Type     string `json:"type"`
			Response struct {
				Usage responsesUsage `json:"usage"`
			} `json:"response"`
		}
		if err := json.Unmarshal(line[len("data: "):], &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "response.completed", "response.incomplete", "response.failed":
			if ev.Response.Usage.InputTokens > 0 || ev.Response.Usage.OutputTokens > 0 {
				u = ev.Response.Usage.canonical()
			}
		}
	}
	return u
}

func upstreamStatus(err error) int {
	var ue *provider.UpstreamError
	if errors.As(err, &ue) {
		return ue.StatusCode
	}
	return 0
}
