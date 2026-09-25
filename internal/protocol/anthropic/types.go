// Package anthropic 定义 canonical 协议类型（Anthropic Messages）。
//
// 保真约束：请求方向的 messages/system/tools 等内容字段保持 json.RawMessage
// 不深度解析（代理只改 model 字段，内容原样传输）；响应方向对已知 block
// 类型结构化、未知类型整块保留。所有顶层未知字段进 Extra 合并回写。
package anthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

// Request 是入口协议转换后的 canonical 请求。Header 携带白名单头
// （anthropic-beta / anthropic-version），由适配器转发给上游。
type Request struct {
	Model     string
	MaxTokens int64
	Stream    bool
	System    json.RawMessage
	Messages  json.RawMessage
	Header    http.Header `json:"-"`
	Extra     map[string]json.RawMessage
}

type requestJSON struct {
	Model     string                     `json:"model"`
	MaxTokens int64                      `json:"max_tokens,omitempty"`
	Stream    bool                       `json:"stream,omitempty"`
	System    json.RawMessage            `json:"system,omitempty"`
	Messages  json.RawMessage            `json:"messages"`
	Extra     map[string]json.RawMessage `json:"-"`
}

func ParseRequest(body []byte) (*Request, error) {
	var r requestJSON
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	req := &Request{
		Model: r.Model, MaxTokens: r.MaxTokens, Stream: r.Stream,
		System: r.System, Messages: r.Messages, Extra: map[string]json.RawMessage{},
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, err
	}
	for k, v := range all {
		switch k {
		case "model", "max_tokens", "stream", "system", "messages":
		default:
			req.Extra[k] = v
		}
	}
	return req, nil
}

func WriteRequestBytes(req *Request) ([]byte, error) {
	r := requestJSON{Model: req.Model, MaxTokens: req.MaxTokens, Stream: req.Stream, System: req.System, Messages: req.Messages}
	base, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, req.Extra, map[string]bool{"model": true, "max_tokens": true, "stream": true, "system": true, "messages": true})
}

// ContentBlock 是响应/事件中的内容块。已知字段结构化，其余进 Extra；
// 未识别 Type 时 Raw 保留整块原样输出。
type ContentBlock struct {
	Type     string
	ID       string
	Name     string
	Text     string
	Thinking string
	Input    json.RawMessage
	Source   json.RawMessage
	Extra    map[string]json.RawMessage
	Raw      json.RawMessage `json:"-"`
}

type contentBlockJSON struct {
	Type      string                     `json:"type"`
	ID        string                     `json:"id,omitempty"`
	Name      string                     `json:"name,omitempty"`
	Text      string                     `json:"text,omitempty"`
	Thinking  string                     `json:"thinking,omitempty"`
	Input     json.RawMessage            `json:"input,omitempty"`
	Source    json.RawMessage            `json:"source,omitempty"`
	Signature string                     `json:"signature,omitempty"`
	Extra     map[string]json.RawMessage `json:"-"`
}

var knownBlockFields = map[string]bool{
	"type": true, "id": true, "name": true, "text": true, "thinking": true, "input": true, "source": true,
}

func (c *ContentBlock) UnmarshalJSON(data []byte) error {
	c.Raw = append([]byte(nil), data...)
	var j contentBlockJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	c.Type, c.ID, c.Name, c.Text, c.Thinking, c.Input, c.Source = j.Type, j.ID, j.Name, j.Text, j.Thinking, j.Input, j.Source
	c.Extra = map[string]json.RawMessage{}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	for k, v := range all {
		if !knownBlockFields[k] {
			c.Extra[k] = v
		}
	}
	return nil
}

func (c ContentBlock) MarshalJSON() ([]byte, error) {
	if c.Raw != nil && c.Type == "" {
		return c.Raw, nil
	}
	switch c.Type {
	case "text":
		out := map[string]json.RawMessage{"type": mustJSON("text")}
		if c.Text != "" {
			out["text"] = mustJSON(c.Text)
		}
		for k, v := range c.Extra {
			out[k] = v
		}
		return json.Marshal(out)
	case "thinking":
		out := map[string]json.RawMessage{"type": mustJSON("thinking")}
		if c.Thinking != "" {
			out["thinking"] = mustJSON(c.Thinking)
		}
		for k, v := range c.Extra {
			out[k] = v
		}
		return json.Marshal(out)
	case "tool_use":
		out := map[string]json.RawMessage{"type": mustJSON("tool_use"), "id": mustJSON(c.ID), "name": mustJSON(c.Name)}
		if c.Input != nil {
			out["input"] = c.Input
		}
		for k, v := range c.Extra {
			out[k] = v
		}
		return json.Marshal(out)
	case "image":
		out := map[string]json.RawMessage{"type": mustJSON("image")}
		if c.Source != nil {
			out["source"] = c.Source
		}
		for k, v := range c.Extra {
			out[k] = v
		}
		return json.Marshal(out)
	default:
		if c.Raw != nil {
			return c.Raw, nil
		}
		out := map[string]json.RawMessage{}
		if c.Type != "" {
			out["type"] = mustJSON(c.Type)
		}
		for k, v := range c.Extra {
			out[k] = v
		}
		return json.Marshal(out)
	}
}

// Response 是 canonical 完整响应。
type Response struct {
	ID         string
	Type       string
	Role       string
	Model      string
	Content    []ContentBlock
	StopReason string
	Usage      Usage
	Extra      map[string]json.RawMessage
}

type responseJSON struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type,omitempty"`
	Role       string                     `json:"role"`
	Model      string                     `json:"model,omitempty"`
	Content    []ContentBlock             `json:"content"`
	StopReason string                     `json:"stop_reason,omitempty"`
	Usage      *Usage                     `json:"usage,omitempty"`
	Extra      map[string]json.RawMessage `json:"-"`
}

func ParseResponse(body []byte) (*Response, error) {
	var r responseJSON
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	resp := &Response{
		ID: r.ID, Type: r.Type, Role: r.Role, Model: r.Model,
		Content: r.Content, StopReason: r.StopReason, Extra: map[string]json.RawMessage{},
	}
	if r.Usage != nil {
		resp.Usage = *r.Usage
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, err
	}
	for k, v := range all {
		switch k {
		case "id", "type", "role", "model", "content", "stop_reason", "usage":
		default:
			resp.Extra[k] = v
		}
	}
	return resp, nil
}

func WriteResponseBytes(resp *Response) ([]byte, error) {
	r := responseJSON{ID: resp.ID, Type: resp.Type, Role: resp.Role, Model: resp.Model, Content: resp.Content, StopReason: resp.StopReason}
	hasUsage := resp.Usage != (Usage{})
	if hasUsage {
		r.Usage = &resp.Usage
	}
	base, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return mergeExtra(base, resp.Extra, map[string]bool{
		"id": true, "type": true, "role": true, "model": true, "content": true, "stop_reason": true, "usage": true,
	})
}

type Usage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// MergeEvent 从流式事件里累计 usage（message_start 给输入侧，message_delta 给输出侧）。
func (u *Usage) MergeEvent(e Event) {
	extract := func(raw json.RawMessage) {
		var probe struct {
			Usage   *Usage `json:"usage"`
			Message *struct {
				Usage *Usage `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(raw, &probe) != nil {
			return
		}
		if probe.Usage != nil {
			if probe.Usage.OutputTokens > u.OutputTokens {
				u.OutputTokens = probe.Usage.OutputTokens
			}
			u.InputTokens += probe.Usage.InputTokens
			u.CacheCreationInputTokens += probe.Usage.CacheCreationInputTokens
			u.CacheReadInputTokens += probe.Usage.CacheReadInputTokens
		}
		if probe.Message != nil && probe.Message.Usage != nil {
			mu := probe.Message.Usage
			if mu.InputTokens > u.InputTokens {
				u.InputTokens = mu.InputTokens
			}
			if mu.CacheCreationInputTokens > u.CacheCreationInputTokens {
				u.CacheCreationInputTokens = mu.CacheCreationInputTokens
			}
			if mu.CacheReadInputTokens > u.CacheReadInputTokens {
				u.CacheReadInputTokens = mu.CacheReadInputTokens
			}
		}
	}
	extract(e.Raw)
}

// Event 是归一化的 canonical SSE 事件；未知事件类型 Type 原样、Raw 保留。
type Event struct {
	Type string
	Raw  json.RawMessage
}

// SSEWrite 按 Anthropic SSE 规范写出一个事件。
func SSEWrite(w io.Writer, e Event) error {
	var buf bytes.Buffer
	buf.WriteString("event: ")
	buf.WriteString(e.Type)
	buf.WriteString("\ndata: ")
	buf.Write(e.Raw)
	buf.WriteString("\n\n")
	_, err := w.Write(buf.Bytes())
	return err
}

// ParseSSEEvent 从一行 data: 载荷解析事件类型。
func ParseSSEEvent(data []byte) Event {
	var probe struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data, &probe)
	return Event{Type: probe.Type, Raw: append(json.RawMessage(nil), data...)}
}

func mergeExtra(base []byte, extra map[string]json.RawMessage, own map[string]bool) ([]byte, error) {
	if len(extra) == 0 {
		return base, nil
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(base, &out); err != nil {
		return nil, err
	}
	for k, v := range extra {
		if !own[k] {
			out[k] = v
		}
	}
	return json.Marshal(out)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
