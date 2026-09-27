// Package convert 提供 openai-chat ↔ anthropic 双向转换（纯函数）。
//
// 请求方向 messages 深度解析（角色/多模态/工具映射）；未知字段尽力保留进
// canonical Extra。响应方向 content blocks 结构化映射，未知 block 降级为
// JSON 文本附注，不丢弃。
package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/protocol/openaichat"
)

// ChatToAnthropic 入口 chat 请求 → canonical。
func ChatToAnthropic(body []byte) (*anthropic.Request, error) {
	chat, err := openaichat.ParseChatRequest(body)
	if err != nil {
		return nil, fmt.Errorf("parse chat request: %w", err)
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(chat.Messages, &msgs); err != nil {
		return nil, fmt.Errorf("parse messages: %w", err)
	}
	sysParts := []string{}
	var outMsgs []map[string]json.RawMessage
	for _, raw := range msgs {
		var m struct {
			Role       string          `json:"role"`
			Content    json.RawMessage `json:"content"`
			ToolCalls  json.RawMessage `json:"tool_calls"`
			ToolCallID string          `json:"tool_call_id"`
			Name       string          `json:"name"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("parse message: %w", err)
		}
		switch m.Role {
		case "system", "developer":
			sysParts = append(sysParts, contentToText(m.Content))
		case "tool":
			outMsgs = append(outMsgs, map[string]json.RawMessage{
				"role":    mustJSON("user"),
				"content": mustJSON([]map[string]any{{"type": "tool_result", "tool_use_id": m.ToolCallID, "content": contentToText(m.Content)}}),
			})
		case "user", "assistant":
		default:
			// 未知角色：透传为 user
			m.Role = "user"
		}
		if m.Role == "user" {
			blocks, err := chatContentToBlocks(m.Content)
			if err != nil {
				return nil, err
			}
			outMsgs = append(outMsgs, map[string]json.RawMessage{"role": mustJSON("user"), "content": mustJSON(blocks)})
		}
		if m.Role == "assistant" {
			blocks := []map[string]any{}
			if txt := contentToText(m.Content); txt != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": txt})
			}
			if len(m.ToolCalls) > 0 {
				var calls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				}
				if err := json.Unmarshal(m.ToolCalls, &calls); err != nil {
					return nil, fmt.Errorf("parse tool_calls: %w", err)
				}
				for _, c := range calls {
					var input any = map[string]any{}
					_ = json.Unmarshal([]byte(c.Function.Arguments), &input)
					blocks = append(blocks, map[string]any{"type": "tool_use", "id": c.ID, "name": c.Function.Name, "input": input})
				}
			}
			if len(blocks) == 0 {
				blocks = append(blocks, map[string]any{"type": "text", "text": ""})
			}
			outMsgs = append(outMsgs, map[string]json.RawMessage{"role": mustJSON("assistant"), "content": mustJSON(blocks)})
		}
	}
	req := &anthropic.Request{
		Model: chat.Model, Stream: chat.Stream,
		Messages: mustJSON(outMsgs), Extra: map[string]json.RawMessage{},
	}
	if len(sysParts) > 0 {
		req.System = mustJSON(strings.Join(sysParts, "\n\n"))
	}
	if chat.MaxTokens > 0 {
		req.MaxTokens = chat.MaxTokens
	} else {
		req.MaxTokens = 4096
	}
	if chat.Tools != nil {
		tools, err := chatToolsToAnthropic(chat.Tools)
		if err != nil {
			return nil, err
		}
		req.Extra["tools"] = tools
	}
	if chat.ResponseFormat != nil {
		req.Extra["response_format"] = chat.ResponseFormat
	}
	for k, v := range chat.Extra {
		switch k {
		case "stream_options", "n", "user":
			// chat 专有字段不转发
		default:
			req.Extra[k] = v
		}
	}
	return req, nil
}

func contentToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []map[string]json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		var sb strings.Builder
		for _, p := range parts {
			var pt string
			_ = json.Unmarshal(p["text"], &pt)
			sb.WriteString(pt)
		}
		return sb.String()
	}
	return string(raw)
}

// chatContentToBlocks：text part → text block；image_url → image block（url 或 base64）。
func chatContentToBlocks(raw json.RawMessage) ([]map[string]any, error) {
	if len(raw) == 0 {
		return []map[string]any{{"type": "text", "text": ""}}, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []map[string]any{{"type": "text", "text": s}}, nil
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil, fmt.Errorf("parse content parts: %w", err)
	}
	blocks := []map[string]any{}
	for _, p := range parts {
		var ptype string
		_ = json.Unmarshal(p["type"], &ptype)
		switch ptype {
		case "text":
			var txt string
			_ = json.Unmarshal(p["text"], &txt)
			blocks = append(blocks, map[string]any{"type": "text", "text": txt})
		case "image_url":
			var iu struct {
				URL    string `json:"url"`
				Detail string `json:"detail"`
			}
			if err := json.Unmarshal(p["image_url"], &iu); err != nil {
				return nil, err
			}
			src := map[string]any{}
			if strings.HasPrefix(iu.URL, "data:") {
				// data:image/png;base64,xxx
				media, data, ok := strings.Cut(strings.TrimPrefix(iu.URL, "data:"), ";base64,")
				if ok {
					src = map[string]any{"type": "base64", "media_type": media, "data": data}
				}
			} else {
				src = map[string]any{"type": "url", "url": iu.URL}
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

func chatToolsToAnthropic(raw json.RawMessage) (json.RawMessage, error) {
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("parse tools: %w", err)
	}
	out := []map[string]any{}
	for _, t := range tools {
		params := t.Function.Parameters
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		out = append(out, map[string]any{
			"name": t.Function.Name, "description": t.Function.Description, "input_schema": params,
		})
	}
	return mustJSON(out), nil
}

// AnthropicToChatResponse canonical 响应 → chat 响应。
func AnthropicToChatResponse(resp *anthropic.Response) *openaichat.ChatResponse {
	out := &openaichat.ChatResponse{
		ID: resp.ID, Object: "chat.completion", Created: 0, Model: resp.Model,
		Usage: &openaichat.Usage{
			PromptTokens:     resp.Usage.InputTokens + resp.Usage.CacheReadInputTokens + resp.Usage.CacheCreationInputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
		},
	}
	out.Usage.TotalTokens = out.Usage.PromptTokens + out.Usage.CompletionTokens
	var text strings.Builder
	for _, b := range resp.Content {
		switch b.Type {
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			args := string(b.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			out.ToolCalls = append(out.ToolCalls, openaichat.ToolCall{
				ID: b.ID, Type: "function",
				Function: openaichat.FunctionCall{Name: b.Name, Arguments: args},
			})
		default:
			// thinking 等非输出块不进 chat content
		}
	}
	out.Content = text.String()
	out.FinishReason = stopReasonToFinish(resp.StopReason)
	if out.FinishReason == "" {
		out.FinishReason = "stop"
	}
	if len(out.ToolCalls) > 0 && out.FinishReason == "stop" {
		out.FinishReason = "tool_calls"
	}
	return out
}

func stopReasonToFinish(sr string) string {
	switch sr {
	case "end_turn", "stop_sequence":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "":
		return ""
	default:
		return "stop"
	}
}

// AnthropicToChatChunk canonical 事件 → chat chunk。返回 nil 表示该事件
// 不产生输出（如 ping）。
func AnthropicToChatChunk(e anthropic.Event) *openaichat.Chunk {
	switch e.Type {
	case "message_start":
		var probe struct {
			Message struct {
				ID    string `json:"id"`
				Model string `json:"model"`
				Role  string `json:"role"`
				Usage *struct {
					InputTokens int64 `json:"input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		c := &openaichat.Chunk{ID: probe.Message.ID, Object: "chat.completion.chunk", Model: probe.Message.Model}
		c.Delta = mustJSON(map[string]string{"role": "assistant", "content": ""})
		if probe.Message.Usage != nil {
			c.Usage = &openaichat.Usage{PromptTokens: probe.Message.Usage.InputTokens}
		}
		return c
	case "content_block_delta":
		var probe struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		_ = json.Unmarshal(e.Raw, &probe)
		c := &openaichat.Chunk{Object: "chat.completion.chunk"}
		switch probe.Delta.Type {
		case "text_delta":
			c.Delta = mustJSON(map[string]any{"content": probe.Delta.Text})
			return c
		case "input_json_delta":
			// tool 参数增量无法精确对齐 chat 的 index 语义，聚合到第一个 tool_call
			c.Delta = mustJSON(map[string]any{"tool_calls": []map[string]any{{
				"index": 0, "function": map[string]string{"arguments": probe.Delta.PartialJSON},
			}}})
			return c
		case "thinking_delta":
			return nil // chat 协议无对应，丢弃（不产生输出块）
		default:
			return nil
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
		if probe.ContentBlock.Type != "tool_use" {
			return nil
		}
		c := &openaichat.Chunk{Object: "chat.completion.chunk"}
		c.Delta = mustJSON(map[string]any{"tool_calls": []map[string]any{{
			"index": 0, "id": probe.ContentBlock.ID, "type": "function",
			"function": map[string]string{"name": probe.ContentBlock.Name, "arguments": ""},
		}}})
		return c
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
		c := &openaichat.Chunk{Object: "chat.completion.chunk"}
		finish := stopReasonToFinish(probe.Delta.StopReason)
		c.Finish = &finish
		if probe.Usage != nil {
			c.Usage = &openaichat.Usage{CompletionTokens: probe.Usage.OutputTokens}
		}
		return c
	default:
		return nil
	}
}

// AnthropicToChatRequest canonical → chat 请求（openai-compat 适配器上行用）。
func AnthropicToChatRequest(req *anthropic.Request) ([]byte, error) {
	out := map[string]json.RawMessage{
		"model": mustJSON(req.Model),
	}
	if req.Stream {
		out["stream"] = mustJSON(true)
		// 让支持的上游在流末尾回传 usage（OpenAI/GLM/Kimi 均支持 stream_options）
		out["stream_options"] = mustJSON(map[string]bool{"include_usage": true})
	}
	if req.MaxTokens > 0 {
		out["max_tokens"] = mustJSON(req.MaxTokens)
	}
	// system → 首条 system message；anthropic messages 数组直接沿用
	// （user/assistant/text/image/tool_use/tool_result 语义两边对齐）
	var msgs []json.RawMessage
	if req.System != nil {
		sysText := systemToText(req.System)
		if sysText != "" {
			msgs = append(msgs, mustJSON(map[string]any{"role": "system", "content": sysText}))
		}
	}
	if req.Messages != nil {
		var rest []json.RawMessage
		if err := json.Unmarshal(req.Messages, &rest); err != nil {
			return nil, fmt.Errorf("parse canonical messages: %w", err)
		}
		msgs = append(msgs, rest...)
	}
	out["messages"] = mustJSON(msgs)
	// anthropic content blocks → chat content parts（tool_use → tool_calls 等）
	converted, err := anthropicMessagesToChat(msgs)
	if err != nil {
		return nil, err
	}
	out["messages"] = mustJSON(converted)
	for k, v := range req.Extra {
		switch k {
		case "tools":
			tools, err := anthropicToolsToChat(v)
			if err != nil {
				return nil, err
			}
			if tools != nil {
				out["tools"] = tools
			}
		default:
			out[k] = v
		}
	}
	return json.Marshal(out)
}

func systemToText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			var txt string
			_ = json.Unmarshal(b["text"], &txt)
			sb.WriteString(txt)
		}
		return sb.String()
	}
	return ""
}

// anthropicMessagesToChat 把 canonical 消息数组转换为 chat 消息数组。
func anthropicMessagesToChat(msgs []json.RawMessage) ([]map[string]any, error) {
	out := []map[string]any{}
	for _, raw := range msgs {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, err
		}
		switch m.Role {
		case "assistant":
			var blocks []map[string]json.RawMessage
			if err := json.Unmarshal(m.Content, &blocks); err != nil {
				out = append(out, map[string]any{"role": "assistant", "content": contentToText(m.Content)})
				continue
			}
			var text strings.Builder
			var toolCalls []map[string]any
			for _, b := range blocks {
				var btype string
				_ = json.Unmarshal(b["type"], &btype)
				switch btype {
				case "text":
					var txt string
					_ = json.Unmarshal(b["text"], &txt)
					text.WriteString(txt)
				case "tool_use":
					var id, name string
					_ = json.Unmarshal(b["id"], &id)
					_ = json.Unmarshal(b["name"], &name)
					args := "{}"
					if len(b["input"]) > 0 {
						args = string(b["input"])
					}
					toolCalls = append(toolCalls, map[string]any{
						"id": id, "type": "function",
						"function": map[string]string{"name": name, "arguments": args},
					})
				}
			}
			msg := map[string]any{"role": "assistant", "content": text.String()}
			if len(toolCalls) > 0 {
				msg["tool_calls"] = toolCalls
			}
			out = append(out, msg)
		case "user":
			var blocks []map[string]json.RawMessage
			if err := json.Unmarshal(m.Content, &blocks); err != nil {
				out = append(out, map[string]any{"role": "user", "content": contentToText(m.Content)})
				continue
			}
			parts := []map[string]any{}
			pendingToolResults := []map[string]any{}
			for _, b := range blocks {
				var btype string
				_ = json.Unmarshal(b["type"], &btype)
				switch btype {
				case "text":
					var txt string
					_ = json.Unmarshal(b["text"], &txt)
					parts = append(parts, map[string]any{"type": "text", "text": txt})
				case "image":
					var src struct {
						Type      string `json:"type"`
						MediaType string `json:"media_type"`
						Data      string `json:"data"`
						URL       string `json:"url"`
					}
					_ = json.Unmarshal(b["source"], &src)
					url := src.URL
					if src.Type == "base64" {
						url = "data:" + src.MediaType + ";base64," + src.Data
					}
					parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": url}})
				case "tool_result":
					var id string
					_ = json.Unmarshal(b["tool_use_id"], &id)
					content := contentToText(b["content"])
					if content == "" && len(b["content"]) > 0 {
						content = string(b["content"])
					}
					pendingToolResults = append(pendingToolResults, map[string]any{
						"role": "tool", "tool_call_id": id, "content": content,
					})
				}
			}
			// tool_result 单独成 tool 消息（chat 协议要求）
			out = append(out, pendingToolResults...)
			if len(parts) > 0 {
				out = append(out, map[string]any{"role": "user", "content": parts})
			}
		default:
			out = append(out, map[string]any{"role": m.Role, "content": contentToText(m.Content)})
		}
	}
	return out, nil
}

func anthropicToolsToChat(raw json.RawMessage) (json.RawMessage, error) {
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		return nil, fmt.Errorf("parse anthropic tools: %w", err)
	}
	out := []map[string]any{}
	for _, t := range tools {
		var name, desc string
		_ = json.Unmarshal(t["name"], &name)
		_ = json.Unmarshal(t["description"], &desc)
		params := t["input_schema"]
		if len(params) == 0 {
			params = json.RawMessage(`{"type":"object"}`)
		}
		var paramsAny any
		_ = json.Unmarshal(params, &paramsAny)
		out = append(out, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": name, "description": desc, "parameters": paramsAny},
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return mustJSON(out), nil
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
