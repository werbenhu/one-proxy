// responses_anthropic.go 提供 openai-responses ↔ anthropic 双向转换（纯函数）。
//
// 上游原生支持 Responses 时 proxy 层走原样透传（见 handlers_responses.go），
// 本文件只服务不支持 Responses 的上游：请求方向 input items → canonical
// messages（instructions/system/developer 进 system，function_call/output 映射
// tool_use/tool_result）；响应方向 content blocks → output items；流式方向
// canonical 事件 → Responses SSE 事件序列（有状态，见 ResponsesStream）。
package convert

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
)

// ResponsesToAnthropic 入口 Responses 请求 → canonical。
func ResponsesToAnthropic(body []byte) (*anthropic.Request, error) {
	var rr struct {
		Model           string          `json:"model"`
		Instructions    string          `json:"instructions"`
		Input           json.RawMessage `json:"input"`
		MaxOutputTokens int64           `json:"max_output_tokens"`
		Stream          bool            `json:"stream"`
		Temperature     json.RawMessage `json:"temperature"`
		TopP            json.RawMessage `json:"top_p"`
		Tools           json.RawMessage `json:"tools"`
		ToolChoice      json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal(body, &rr); err != nil {
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	req := &anthropic.Request{
		Model: rr.Model, Stream: rr.Stream,
		MaxTokens: rr.MaxOutputTokens, Extra: map[string]json.RawMessage{},
	}
	if req.MaxTokens <= 0 {
		req.MaxTokens = 4096
	}
	var sysParts []string
	if strings.TrimSpace(rr.Instructions) != "" {
		sysParts = append(sysParts, rr.Instructions)
	}
	msgs, sys, err := responsesInputToMessages(rr.Input)
	if err != nil {
		return nil, err
	}
	sysParts = append(sysParts, sys...)
	if len(sysParts) > 0 {
		req.System = mustJSON(strings.Join(sysParts, "\n\n"))
	}
	if len(msgs) == 0 {
		msgs = []map[string]json.RawMessage{{
			"role": mustJSON("user"), "content": mustJSON(""),
		}}
	}
	req.Messages = mustJSON(msgs)
	if rr.Tools != nil {
		tools, err := responsesToolsToAnthropic(rr.Tools)
		if err != nil {
			return nil, err
		}
		if tools != nil {
			req.Extra["tools"] = tools
		}
	}
	if tc := responsesToolChoiceToAnthropic(rr.ToolChoice); tc != nil {
		req.Extra["tool_choice"] = tc
	}
	// 其余字段只放行 anthropic 侧存在的采样参数；store/previous_response_id/
	// reasoning/text/truncation 等 Responses 专有字段不转发
	for k, v := range map[string]json.RawMessage{"temperature": rr.Temperature, "top_p": rr.TopP} {
		if len(v) > 0 {
			req.Extra[k] = v
		}
	}
	return req, nil
}

// responsesInputToMessages input（字符串或 item 数组）→ canonical messages +
// 从 message item 里拆出的 system 文本。
func responsesInputToMessages(raw json.RawMessage) ([]map[string]json.RawMessage, []string, error) {
	if len(raw) == 0 {
		return nil, nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []map[string]json.RawMessage{{
			"role":    mustJSON("user"),
			"content": mustJSON([]map[string]any{{"type": "text", "text": s}}),
		}}, nil, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, nil, fmt.Errorf("parse input: %w", err)
	}
	var msgs []map[string]json.RawMessage
	var sysParts []string
	for _, item := range items {
		var it struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			CallID    string          `json:"call_id"`
			Name      string          `json:"name"`
			Arguments string          `json:"arguments"`
			Output    json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(item, &it); err != nil {
			return nil, nil, fmt.Errorf("parse input item: %w", err)
		}
		switch it.Type {
		case "function_call":
			var input any = map[string]any{}
			_ = json.Unmarshal([]byte(it.Arguments), &input)
			msgs = append(msgs, map[string]json.RawMessage{
				"role": mustJSON("assistant"),
				"content": mustJSON([]map[string]any{{
					"type": "tool_use", "id": it.CallID, "name": it.Name, "input": input,
				}}),
			})
		case "function_call_output":
			msgs = append(msgs, map[string]json.RawMessage{
				"role": mustJSON("user"),
				"content": mustJSON([]map[string]any{{
					"type": "tool_result", "tool_use_id": it.CallID, "content": contentToText(it.Output),
				}}),
			})
		case "reasoning", "item_reference":
			// 推理链/引用对上游无意义，丢弃
		default:
			// "message" 或省略 type 的 {role, content} 简写
			switch it.Role {
			case "system", "developer":
				if txt := contentToText(it.Content); txt != "" {
					sysParts = append(sysParts, txt)
				}
			case "assistant":
				msgs = append(msgs, map[string]json.RawMessage{
					"role": mustJSON("assistant"), "content": mustJSON(responsesAssistantBlocks(it.Content)),
				})
			default:
				blocks, err := responsesUserBlocks(it.Content)
				if err != nil {
					return nil, nil, err
				}
				msgs = append(msgs, map[string]json.RawMessage{
					"role": mustJSON("user"), "content": mustJSON(blocks),
				})
			}
		}
	}
	return msgs, sysParts, nil
}

// responsesUserBlocks input_text/input_image parts → canonical blocks。
func responsesUserBlocks(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return []map[string]any{{"type": "text", "text": ""}}, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []map[string]any{{"type": "text", "text": s}}, nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("parse message content: %w", err)
	}
	blocks := []map[string]any{}
	for _, p := range parts {
		var ptype string
		_ = json.Unmarshal(p["type"], &ptype)
		switch ptype {
		case "input_text", "output_text", "text":
			var txt string
			_ = json.Unmarshal(p["text"], &txt)
			blocks = append(blocks, map[string]any{"type": "text", "text": txt})
		case "input_image":
			var imageURL string
			_ = json.Unmarshal(p["image_url"], &imageURL)
			src := map[string]any{}
			if media, data, ok := strings.Cut(strings.TrimPrefix(imageURL, "data:"), ";base64,"); ok && media != imageURL {
				src = map[string]any{"type": "base64", "media_type": media, "data": data}
			} else {
				src = map[string]any{"type": "url", "url": imageURL}
			}
			blocks = append(blocks, map[string]any{"type": "image", "source": src})
		default:
			// 未知 part 类型：保留原始 JSON 进 text block（不丢弃）
			blocks = append(blocks, map[string]any{"type": "text", "text": mustJSON(p)})
		}
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return blocks, nil
}

// responsesAssistantBlocks output_text/refusal parts → canonical text blocks。
func responsesAssistantBlocks(raw json.RawMessage) []map[string]any {
	if len(raw) == 0 {
		return []map[string]any{{"type": "text", "text": ""}}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []map[string]any{{"type": "text", "text": s}}
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return []map[string]any{{"type": "text", "text": contentToText(raw)}}
	}
	blocks := []map[string]any{}
	for _, p := range parts {
		var ptype string
		_ = json.Unmarshal(p["type"], &ptype)
		var txt string
		switch ptype {
		case "output_text":
			_ = json.Unmarshal(p["text"], &txt)
		case "refusal":
			_ = json.Unmarshal(p["refusal"], &txt)
		}
		blocks = append(blocks, map[string]any{"type": "text", "text": txt})
	}
	if len(blocks) == 0 {
		blocks = append(blocks, map[string]any{"type": "text", "text": ""})
	}
	return blocks
}

// responsesToolsToAnthropic function 工具 → canonical tools；内置工具
// （web_search 等）上游不支持，跳过。
func responsesToolsToAnthropic(raw json.RawMessage) (json.RawMessage, error) {
	var tools []struct {
		Type        string          `json:"type"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	out := []map[string]any{}
	for _, t := range tools {
		if t.Type != "function" {
			continue
		}
		params := t.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		var paramsAny any
		_ = json.Unmarshal(params, &paramsAny)
		out = append(out, map[string]any{
			"name": t.Name, "description": t.Description, "input_schema": paramsAny,
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return mustJSON(out), nil
}

func responsesToolChoiceToAnthropic(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			return mustJSON(map[string]string{"type": "auto"})
		case "none":
			return mustJSON(map[string]string{"type": "none"})
		case "required":
			return mustJSON(map[string]string{"type": "any"})
		}
		return nil
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &tc) == nil && tc.Type == "function" && tc.Name != "" {
		return mustJSON(map[string]string{"type": "tool", "name": tc.Name})
	}
	return nil
}

// AnthropicToResponses canonical 响应 → Responses 响应体。
func AnthropicToResponses(resp *anthropic.Response) map[string]any {
	id := resp.ID
	if id == "" {
		id = randID("resp_")
	}
	var items []map[string]any
	var textParts []map[string]any
	flushText := func() {
		if len(textParts) == 0 {
			return
		}
		items = append(items, map[string]any{
			"type": "message", "id": randID("msg_"), "status": "completed",
			"role": "assistant", "content": textParts,
		})
		textParts = nil
	}
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			textParts = append(textParts, map[string]any{
				"type": "output_text", "text": b.Text, "annotations": []any{},
			})
		case "tool_use":
			flushText()
			args := string(b.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			items = append(items, map[string]any{
				"type": "function_call", "id": randID("fc_"), "call_id": b.ID,
				"name": b.Name, "arguments": args, "status": "completed",
			})
		default:
			// thinking 等非输出块不进 responses output
		}
	}
	flushText()
	status := "completed"
	out := map[string]any{
		"id": id, "object": "response", "created_at": time.Now().Unix(),
		"status": status, "model": resp.Model, "output": items,
		"usage": responsesUsageFromCanonical(resp.Usage),
	}
	if resp.StopReason == "max_tokens" {
		out["status"] = "incomplete"
		out["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
	}
	return out
}

func responsesUsageFromCanonical(u anthropic.Usage) map[string]any {
	input := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	return map[string]any{
		"input_tokens":          input,
		"output_tokens":         u.OutputTokens,
		"total_tokens":          input + u.OutputTokens,
		"input_tokens_details":  map[string]any{"cached_tokens": u.CacheReadInputTokens},
		"output_tokens_details": map[string]any{"reasoning_tokens": 0},
	}
}

// ResponsesEvent 一个 Responses SSE 事件（event: Type + data: Data）。
type ResponsesEvent struct {
	Type string
	Data json.RawMessage
}

// responsesStreamBlock 流式期间一个 canonical content block 的聚合状态。
type responsesStreamBlock struct {
	kind        string // text | tool | other
	outputIndex int
	itemID      string
	callID      string
	name        string
	text        strings.Builder
	args        strings.Builder
}

// ResponsesStream canonical 事件 → Responses SSE 事件的有状态转换器。
// Handle 返回该事件产生的 SSE 事件（可为空）；message_stop 时聚合出
// 带完整 output 与 usage 的 response.completed。
type ResponsesStream struct {
	respID      string
	model       string
	created     bool
	blocks      map[int]*responsesStreamBlock
	order       []int
	outputCount int
	usage       anthropic.Usage
	stop        string
}

func NewResponsesStream() *ResponsesStream {
	return &ResponsesStream{blocks: map[int]*responsesStreamBlock{}}
}

func (s *ResponsesStream) Handle(e anthropic.Event) []ResponsesEvent {
	switch e.Type {
	case "message_start":
		var probe struct {
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Usage *struct {
					InputTokens              int64 `json:"input_tokens"`
					CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		s.respID = probe.Message.ID
		if s.respID == "" {
			s.respID = randID("resp_")
		}
		s.model = probe.Message.Model
		if probe.Message.Usage != nil {
			s.usage.InputTokens = probe.Message.Usage.InputTokens
			s.usage.CacheReadInputTokens = probe.Message.Usage.CacheReadInputTokens
			s.usage.CacheCreationInputTokens = probe.Message.Usage.CacheCreationInputTokens
		}
		s.created = true
		return []ResponsesEvent{
			s.event("response.created", map[string]any{
				"response": s.responseShell("in_progress", nil),
			}),
			s.event("response.in_progress", map[string]any{
				"response": s.responseShell("in_progress", nil),
			}),
		}
	case "content_block_start":
		var probe struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		b := &responsesStreamBlock{}
		s.blocks[probe.Index] = b
		switch probe.ContentBlock.Type {
		case "text":
			b.kind, b.itemID = "text", randID("msg_")
			b.outputIndex = s.outputCount
			s.outputCount++
			s.order = append(s.order, probe.Index)
			return []ResponsesEvent{
				s.event("response.output_item.added", map[string]any{
					"output_index": b.outputIndex,
					"item":         map[string]any{"type": "message", "id": b.itemID, "status": "in_progress", "role": "assistant", "content": []any{}},
				}),
				s.event("response.content_part.added", map[string]any{
					"item_id": b.itemID, "output_index": b.outputIndex, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
				}),
			}
		case "tool_use":
			b.kind, b.itemID = "tool", randID("fc_")
			b.callID, b.name = probe.ContentBlock.ID, probe.ContentBlock.Name
			b.outputIndex = s.outputCount
			s.outputCount++
			s.order = append(s.order, probe.Index)
			return []ResponsesEvent{s.event("response.output_item.added", map[string]any{
				"output_index": b.outputIndex,
				"item":         map[string]any{"type": "function_call", "id": b.itemID, "call_id": b.callID, "name": b.name, "arguments": ""},
			})}
		default:
			// thinking 等不产出的块：占住 canonical index 以吞掉后续 delta，但不消耗 output_index
			b.kind = "other"
			return nil
		}
	case "content_block_delta":
		var probe struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		b := s.blocks[probe.Index]
		if b == nil {
			return nil
		}
		switch {
		case b.kind == "text" && probe.Delta.Type == "text_delta":
			b.text.WriteString(probe.Delta.Text)
			return []ResponsesEvent{s.event("response.output_text.delta", map[string]any{
				"item_id": b.itemID, "output_index": b.outputIndex, "content_index": 0,
				"delta": probe.Delta.Text, "logprobs": []any{},
			})}
		case b.kind == "tool" && probe.Delta.Type == "input_json_delta":
			b.args.WriteString(probe.Delta.PartialJSON)
			return []ResponsesEvent{s.event("response.function_call_arguments.delta", map[string]any{
				"output_index": b.outputIndex, "item_id": b.itemID, "delta": probe.Delta.PartialJSON,
			})}
		}
		return nil
	case "content_block_stop":
		var probe struct {
			Index int `json:"index"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		b := s.blocks[probe.Index]
		if b == nil {
			return nil
		}
		switch b.kind {
		case "text":
			full := b.text.String()
			return []ResponsesEvent{
				s.event("response.output_text.done", map[string]any{
					"item_id": b.itemID, "output_index": b.outputIndex, "content_index": 0,
					"text": full, "logprobs": []any{},
				}),
				s.event("response.content_part.done", map[string]any{
					"item_id": b.itemID, "output_index": b.outputIndex, "content_index": 0,
					"part": map[string]any{"type": "output_text", "text": full, "annotations": []any{}},
				}),
				s.event("response.output_item.done", map[string]any{
					"output_index": b.outputIndex,
					"item": map[string]any{
						"type": "message", "id": b.itemID, "status": "completed", "role": "assistant",
						"content": []map[string]any{{"type": "output_text", "text": full, "annotations": []any{}}},
					},
				}),
			}
		case "tool":
			args := b.args.String()
			if args == "" {
				args = "{}"
			}
			return []ResponsesEvent{
				s.event("response.function_call_arguments.done", map[string]any{
					"output_index": b.outputIndex, "item_id": b.itemID, "arguments": args,
				}),
				s.event("response.output_item.done", map[string]any{
					"output_index": b.outputIndex,
					"item": map[string]any{
						"type": "function_call", "id": b.itemID, "call_id": b.callID,
						"name": b.name, "arguments": args, "status": "completed",
					},
				}),
			}
		}
		return nil
	case "message_delta":
		var probe struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage *struct {
				OutputTokens int64 `json:"output_tokens"`
			} `json:"usage"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		s.stop = probe.Delta.StopReason
		if probe.Usage != nil {
			s.usage.OutputTokens = probe.Usage.OutputTokens
		}
		return nil
	case "message_stop":
		if !s.created {
			s.respID = randID("resp_")
		}
		return []ResponsesEvent{s.event("response.completed", map[string]any{
			"response": s.responseShell(s.finalStatus(), s.finalOutput()),
		})}
	case "error":
		var probe struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		return []ResponsesEvent{s.event("error", map[string]any{
			"code": probe.Error.Type, "message": probe.Error.Message,
		})}
	default:
		return nil
	}
}

func (s *ResponsesStream) finalStatus() string {
	if s.stop == "max_tokens" {
		return "incomplete"
	}
	return "completed"
}

// finalOutput 按出现顺序把聚合的 block 还原成 output items。
func (s *ResponsesStream) finalOutput() []map[string]any {
	var items []map[string]any
	for _, idx := range s.order {
		b := s.blocks[idx]
		switch b.kind {
		case "text":
			items = append(items, map[string]any{
				"type": "message", "id": b.itemID, "status": "completed", "role": "assistant",
				"content": []map[string]any{{"type": "output_text", "text": b.text.String(), "annotations": []any{}}},
			})
		case "tool":
			args := b.args.String()
			if args == "" {
				args = "{}"
			}
			items = append(items, map[string]any{
				"type": "function_call", "id": b.itemID, "call_id": b.callID,
				"name": b.name, "arguments": args, "status": "completed",
			})
		}
	}
	return items
}

// responseShell 组装 response 对象（created 时 output 为 nil → []）。
func (s *ResponsesStream) responseShell(status string, output any) map[string]any {
	if output == nil {
		output = []any{}
	}
	resp := map[string]any{
		"id": s.respID, "object": "response", "created_at": time.Now().Unix(),
		"status": status, "model": s.model, "output": output,
		"usage": responsesUsageFromCanonical(s.usage),
	}
	if status == "incomplete" {
		resp["incomplete_details"] = map[string]string{"reason": "max_output_tokens"}
	}
	return resp
}

func (s *ResponsesStream) event(typ string, payload map[string]any) ResponsesEvent {
	payload["type"] = typ
	data, _ := json.Marshal(payload)
	return ResponsesEvent{Type: typ, Data: data}
}

func randID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}
