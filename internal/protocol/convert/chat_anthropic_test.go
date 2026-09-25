package convert

import (
	"encoding/json"
	"testing"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
	"github.com/werbenhu/one-proxy/internal/protocol/openaichat"
)

func jsonEq(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	aj, _ := json.Marshal(av)
	bj, _ := json.Marshal(bv)
	return string(aj) == string(bj)
}

// chat → canonical：system、图片、工具、未知字段。
func TestChatToAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "gpt-x",
		"max_tokens": 100,
		"stream": true,
		"temperature": 0.7,
		"messages": [
			{"role": "system", "content": "be brief"},
			{"role": "user", "content": [
				{"type": "text", "text": "look"},
				{"type": "image_url", "image_url": {"url": "data:image/png;base64,aGk="}}
			]},
			{"role": "assistant", "content": "", "tool_calls": [
				{"id": "call_1", "type": "function", "function": {"name": "get_weather", "arguments": "{\"city\":\"sz\"}"}}
			]},
			{"role": "tool", "tool_call_id": "call_1", "content": "sunny"}
		],
		"tools": [{"type": "function", "function": {"name": "get_weather", "description": "d", "parameters": {"type": "object"}}}]
	}`)
	req, err := ChatToAnthropic(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-x" || !req.Stream || req.MaxTokens != 100 {
		t.Fatalf("基础字段: %+v", req)
	}
	if string(req.System) != `"be brief"` {
		t.Fatalf("system: %s", req.System)
	}
	if string(req.Extra["temperature"]) != "0.7" {
		t.Fatalf("未知字段: %s", req.Extra["temperature"])
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(req.Messages, &msgs)
	if len(msgs) != 3 {
		t.Fatalf("消息数: %d %s", len(msgs), req.Messages)
	}
	// user 消息：text + image blocks
	var userBlocks []map[string]json.RawMessage
	_ = json.Unmarshal(msgs[0]["content"], &userBlocks)
	if len(userBlocks) != 2 || string(userBlocks[1]["type"]) != `"image"` {
		t.Fatalf("user blocks: %s", msgs[0]["content"])
	}
	var src map[string]json.RawMessage
	_ = json.Unmarshal(userBlocks[1]["source"], &src)
	if string(src["media_type"]) != `"image/png"` || string(src["data"]) != `"aGk="` {
		t.Fatalf("image source: %s", userBlocks[1]["source"])
	}
	// assistant 消息：tool_use block
	var asstBlocks []map[string]json.RawMessage
	_ = json.Unmarshal(msgs[1]["content"], &asstBlocks)
	if len(asstBlocks) != 1 || string(asstBlocks[0]["type"]) != `"tool_use"` || string(asstBlocks[0]["name"]) != `"get_weather"` {
		t.Fatalf("assistant blocks: %s", msgs[1]["content"])
	}
	// tool 消息 → user + tool_result block
	if string(msgs[2]["role"]) != `"user"` {
		t.Fatalf("tool 角色应转 user: %s", msgs[2]["role"])
	}
	var toolBlocks []map[string]json.RawMessage
	_ = json.Unmarshal(msgs[2]["content"], &toolBlocks)
	if string(toolBlocks[0]["type"]) != `"tool_result"` || string(toolBlocks[0]["tool_use_id"]) != `"call_1"` {
		t.Fatalf("tool_result: %s", msgs[2]["content"])
	}
	// tools 映射
	var tools []map[string]json.RawMessage
	_ = json.Unmarshal(req.Extra["tools"], &tools)
	if len(tools) != 1 || string(tools[0]["name"]) != `"get_weather"` {
		t.Fatalf("tools: %s", req.Extra["tools"])
	}
	if !jsonEq(tools[0]["input_schema"], json.RawMessage(`{"type":"object"}`)) {
		t.Fatalf("input_schema: %s", tools[0]["input_schema"])
	}
}

func TestChatToAnthropicDefaultMaxTokens(t *testing.T) {
	req, err := ChatToAnthropic([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens != 4096 {
		t.Fatalf("默认 max_tokens: %d", req.MaxTokens)
	}
}

// canonical → chat 响应：text + tool_use + usage 合并。
func TestAnthropicToChatResponse(t *testing.T) {
	raw := []byte(`{"id":"msg_1","type":"message","role":"assistant","model":"kimi-k3",
		"content":[{"type":"thinking","thinking":"h"},{"type":"text","text":"answer"},
			{"type":"tool_use","id":"t1","name":"f","input":{"a":1}}],
		"stop_reason":"tool_use",
		"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":5}}`)
	resp, err := anthropic.ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	chat := AnthropicToChatResponse(resp)
	if chat.Content != "answer" {
		t.Fatalf("content: %q", chat.Content)
	}
	if len(chat.ToolCalls) != 1 || chat.ToolCalls[0].Function.Name != "f" || chat.ToolCalls[0].Function.Arguments != `{"a":1}` {
		t.Fatalf("tool_calls: %+v", chat.ToolCalls)
	}
	if chat.FinishReason != "tool_calls" {
		t.Fatalf("finish: %s", chat.FinishReason)
	}
	if chat.Usage.PromptTokens != 15 || chat.Usage.CompletionTokens != 20 || chat.Usage.TotalTokens != 35 {
		t.Fatalf("usage: %+v", chat.Usage)
	}
}

// canonical 事件 → chat chunk 序列。
func TestAnthropicToChatChunk(t *testing.T) {
	cases := []struct {
		event anthropic.Event
		check func(*testing.T, *openaichat.Chunk)
	}{
		{anthropic.Event{Type: "message_start", Raw: json.RawMessage(`{"type":"message_start","message":{"id":"m1","model":"kimi-k3","role":"assistant","usage":{"input_tokens":5}}}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c == nil || c.ID != "m1" || string(c.Delta) == "" {
					t.Fatalf("message_start chunk: %+v", c)
				}
			}},
		{anthropic.Event{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你"}}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c == nil || !jsonEq(c.Delta, json.RawMessage(`{"content":"你"}`)) {
					t.Fatalf("text delta: %+v", c)
				}
			}},
		{anthropic.Event{Type: "content_block_start", Raw: json.RawMessage(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"t1","name":"f"}}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c == nil {
					t.Fatal("tool_use start 应产生 chunk")
				}
			}},
		{anthropic.Event{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":"}}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c == nil || !jsonContains(string(c.Delta), `"arguments":"{\"a\":"`) {
					t.Fatalf("input_json_delta: %+v", c)
				}
			}},
		{anthropic.Event{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"h"}}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c != nil {
					t.Fatalf("thinking_delta 应丢弃: %+v", c)
				}
			}},
		{anthropic.Event{Type: "message_delta", Raw: json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c == nil || c.Finish == nil || *c.Finish != "stop" || c.Usage.CompletionTokens != 7 {
					t.Fatalf("message_delta: %+v", c)
				}
			}},
		{anthropic.Event{Type: "ping", Raw: json.RawMessage(`{"type":"ping"}`)},
			func(t *testing.T, c *openaichat.Chunk) {
				if c != nil {
					t.Fatalf("ping 应丢弃")
				}
			}},
	}
	for i, c := range cases {
		got := AnthropicToChatChunk(c.event)
		c.check(t, got)
		_ = i
	}
}

func jsonContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// canonical → chat 请求（openai-compat 上行）。
func TestAnthropicToChatRequest(t *testing.T) {
	req, err := ChatToAnthropic([]byte(`{"model":"m","messages":[
		{"role":"user","content":"hi"}],"temperature":0.5,
		"tools":[{"type":"function","function":{"name":"f","description":"d","parameters":{"type":"object","properties":{"a":{"type":"string"}}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	body, err := AnthropicToChatRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]json.RawMessage
	_ = json.Unmarshal(body, &out)
	if string(out["model"]) != `"m"` || string(out["temperature"]) != "0.5" {
		t.Fatalf("基础字段: %s", body)
	}
	var tools []map[string]any
	_ = json.Unmarshal(out["tools"], &tools)
	if len(tools) != 1 {
		t.Fatalf("tools 往返: %s", out["tools"])
	}
	fn := tools[0]["function"].(map[string]any)
	if fn["name"] != "f" {
		t.Fatalf("tools name: %+v", fn)
	}
	var msgs []map[string]any
	_ = json.Unmarshal(out["messages"], &msgs)
	if len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Fatalf("messages: %s", out["messages"])
	}
}
