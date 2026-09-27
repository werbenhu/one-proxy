package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/werbenhu/one-proxy/internal/protocol/anthropic"
)

// responses → canonical：instructions、items（message/function_call/output）、工具。
func TestResponsesToAnthropic(t *testing.T) {
	body := []byte(`{
		"model": "glm-5.3",
		"instructions": "be brief",
		"max_output_tokens": 512,
		"stream": true,
		"temperature": 0.5,
		"store": false,
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "weather?"}]},
			{"type": "function_call", "call_id": "call_1", "name": "get_weather", "arguments": "{\"city\":\"sz\"}"},
			{"type": "function_call_output", "call_id": "call_1", "output": "sunny"},
			{"type": "reasoning", "summary": []}
		],
		"tools": [{"type": "function", "name": "get_weather", "description": "d", "parameters": {"type": "object"}}, {"type": "web_search"}],
		"tool_choice": "required"
	}`)
	req, err := ResponsesToAnthropic(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "glm-5.3" || !req.Stream || req.MaxTokens != 512 {
		t.Fatalf("基础字段: %+v", req)
	}
	if string(req.System) != `"be brief"` {
		t.Fatalf("system: %s", req.System)
	}
	if _, ok := req.Extra["store"]; ok {
		t.Fatalf("responses 专有字段不应转发: %s", req.Extra)
	}
	if string(req.Extra["temperature"]) != "0.5" {
		t.Fatalf("temperature: %s", req.Extra["temperature"])
	}
	if !jsonEq(req.Extra["tool_choice"], json.RawMessage(`{"type":"any"}`)) {
		t.Fatalf("tool_choice: %s", req.Extra["tool_choice"])
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(req.Messages, &msgs)
	if len(msgs) != 3 {
		t.Fatalf("消息数（reasoning 应丢弃）: %d %s", len(msgs), req.Messages)
	}
	// function_call → assistant tool_use
	var blocks []map[string]json.RawMessage
	_ = json.Unmarshal(msgs[1]["content"], &blocks)
	var role string
	_ = json.Unmarshal(msgs[1]["role"], &role)
	if role != "assistant" || len(blocks) != 1 {
		t.Fatalf("function_call 映射: %s %s", msgs[1]["role"], msgs[1]["content"])
	}
	var btype, bid, bname string
	_ = json.Unmarshal(blocks[0]["type"], &btype)
	_ = json.Unmarshal(blocks[0]["id"], &bid)
	_ = json.Unmarshal(blocks[0]["name"], &bname)
	if btype != "tool_use" || bid != "call_1" || bname != "get_weather" {
		t.Fatalf("tool_use: %s", blocks[0])
	}
	// function_call_output → user tool_result
	_ = json.Unmarshal(msgs[2]["role"], &role)
	_ = json.Unmarshal(msgs[2]["content"], &blocks)
	var tid, content string
	_ = json.Unmarshal(blocks[0]["tool_use_id"], &tid)
	_ = json.Unmarshal(blocks[0]["content"], &content)
	if role != "user" || tid != "call_1" || content != "sunny" {
		t.Fatalf("tool_result: %s %s", msgs[2]["role"], msgs[2]["content"])
	}
	// 内置工具跳过，function 工具转 input_schema
	var tools []map[string]json.RawMessage
	_ = json.Unmarshal(req.Extra["tools"], &tools)
	if len(tools) != 1 {
		t.Fatalf("tools: %s", req.Extra["tools"])
	}
	if _, ok := tools[0]["input_schema"]; !ok {
		t.Fatalf("input_schema: %s", tools[0])
	}
}

// 字符串 input 简写 + 默认 max_tokens。
func TestResponsesToAnthropicStringInput(t *testing.T) {
	req, err := ResponsesToAnthropic([]byte(`{"model":"m","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTokens != 4096 {
		t.Fatalf("默认 max_tokens: %d", req.MaxTokens)
	}
	var msgs []map[string]json.RawMessage
	_ = json.Unmarshal(req.Messages, &msgs)
	if len(msgs) != 1 {
		t.Fatalf("消息数: %s", req.Messages)
	}
}

// canonical → responses 响应体：text + tool_use → message + function_call items。
func TestAnthropicToResponses(t *testing.T) {
	resp, err := anthropic.ParseResponse([]byte(`{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "glm-5.3",
		"content": [
			{"type": "thinking", "thinking": "hmm"},
			{"type": "text", "text": "hello"},
			{"type": "tool_use", "id": "toolu_1", "name": "get_weather", "input": {"city": "sz"}}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 10, "output_tokens": 4, "cache_read_input_tokens": 3}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	out := AnthropicToResponses(resp)
	if out["object"] != "response" || out["status"] != "completed" || out["id"] != "msg_1" {
		t.Fatalf("基础字段: %v", out)
	}
	items, _ := out["output"].([]map[string]any)
	if len(items) != 2 {
		data, _ := json.Marshal(out)
		t.Fatalf("output items（thinking 应丢弃）: %s", data)
	}
	if items[0]["type"] != "message" || items[1]["type"] != "function_call" {
		t.Fatalf("item 类型: %v %v", items[0]["type"], items[1]["type"])
	}
	content, _ := items[0]["content"].([]map[string]any)
	if content[0]["text"] != "hello" {
		t.Fatalf("output_text: %v", items[0])
	}
	var args map[string]string
	argsStr, _ := items[1]["arguments"].(string)
	_ = json.Unmarshal([]byte(argsStr), &args)
	if items[1]["call_id"] != "toolu_1" || args["city"] != "sz" {
		t.Fatalf("function_call: %v", items[1])
	}
	usage, _ := out["usage"].(map[string]any)
	if usage["input_tokens"] != int64(13) || usage["total_tokens"] != int64(17) {
		t.Fatalf("usage: %v", usage)
	}
}

// max_tokens 截断 → incomplete。
func TestAnthropicToResponsesIncomplete(t *testing.T) {
	resp, err := anthropic.ParseResponse([]byte(`{
		"id": "msg_2", "role": "assistant", "model": "m",
		"content": [{"type": "text", "text": "x"}], "stop_reason": "max_tokens",
		"usage": {"input_tokens": 1, "output_tokens": 2}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	out := AnthropicToResponses(resp)
	if out["status"] != "incomplete" {
		t.Fatalf("status: %v", out["status"])
	}
}

// 流式：canonical 事件序列 → Responses SSE 事件序列与 completed 聚合。
func TestResponsesStream(t *testing.T) {
	events := []anthropic.Event{
		{Type: "message_start", Raw: json.RawMessage(`{"type":"message_start","message":{"id":"msg_s","model":"glm-5.3","usage":{"input_tokens":11,"cache_read_input_tokens":2}}}`)},
		{Type: "content_block_start", Raw: json.RawMessage(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)},
		{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"he"}}`)},
		{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"llo"}}`)},
		{Type: "content_block_stop", Raw: json.RawMessage(`{"type":"content_block_stop","index":0}`)},
		{Type: "content_block_start", Raw: json.RawMessage(`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_9","name":"get_weather"}}`)},
		{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`)},
		{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"sz\"}"}}`)},
		{Type: "content_block_stop", Raw: json.RawMessage(`{"type":"content_block_stop","index":1}`)},
		{Type: "message_delta", Raw: json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}`)},
		{Type: "message_stop", Raw: json.RawMessage(`{"type":"message_stop"}`)},
	}
	conv := NewResponsesStream()
	var types []string
	var completed json.RawMessage
	for _, ev := range events {
		for _, out := range conv.Handle(ev) {
			types = append(types, out.Type)
			if out.Type == "response.completed" {
				completed = out.Data
			}
		}
	}
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.output_item.added",
		"response.function_call_arguments.delta", "response.function_call_arguments.delta",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.completed",
	}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("事件序列:\n%s", strings.Join(types, "\n"))
	}
	var final struct {
		Response struct {
			Status string           `json:"status"`
			Output []map[string]any `json:"output"`
			Usage  struct {
				InputTokens  int64 `json:"input_tokens"`
				OutputTokens int64 `json:"output_tokens"`
				TotalTokens  int64 `json:"total_tokens"`
			} `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(completed, &final); err != nil {
		t.Fatal(err)
	}
	if final.Response.Status != "completed" || len(final.Response.Output) != 2 {
		data, _ := json.Marshal(final)
		t.Fatalf("completed 聚合: %s", data)
	}
	if final.Response.Usage.InputTokens != 13 || final.Response.Usage.OutputTokens != 7 || final.Response.Usage.TotalTokens != 20 {
		t.Fatalf("completed usage: %+v", final.Response.Usage)
	}
	tool := final.Response.Output[1]
	if tool["type"] != "function_call" || tool["arguments"] != `{"city":"sz"}` {
		t.Fatalf("completed tool 参数聚合: %v", tool)
	}
}

// 回归：thinking 块（index 0）不应消耗 output_index；文本事件必须带 item_id。
func TestResponsesStreamThinkingBlock(t *testing.T) {
	events := []anthropic.Event{
		{Type: "message_start", Raw: json.RawMessage(`{"type":"message_start","message":{"id":"msg_s","model":"glm-5.3"}}`)},
		{Type: "content_block_start", Raw: json.RawMessage(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)},
		{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"hmm"}}`)},
		{Type: "content_block_stop", Raw: json.RawMessage(`{"type":"content_block_stop","index":0}`)},
		{Type: "content_block_start", Raw: json.RawMessage(`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`)},
		{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hi"}}`)},
		{Type: "content_block_stop", Raw: json.RawMessage(`{"type":"content_block_stop","index":1}`)},
		{Type: "message_delta", Raw: json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}`)},
		{Type: "message_stop", Raw: json.RawMessage(`{"type":"message_stop"}`)},
	}
	conv := NewResponsesStream()
	var completed json.RawMessage
	for _, ev := range events {
		for _, out := range conv.Handle(ev) {
			var probe struct {
				OutputIndex *int   `json:"output_index"`
				ItemID      string `json:"item_id"`
			}
			_ = json.Unmarshal(out.Data, &probe)
			switch out.Type {
			case "response.output_item.added", "response.content_part.added",
				"response.output_text.delta", "response.output_text.done",
				"response.content_part.done", "response.output_item.done":
				if probe.OutputIndex == nil || *probe.OutputIndex != 0 {
					t.Fatalf("%s 的 output_index 应为 0: %s", out.Type, out.Data)
				}
				if out.Type != "response.output_item.added" && out.Type != "response.output_item.done" && probe.ItemID == "" {
					t.Fatalf("%s 缺 item_id: %s", out.Type, out.Data)
				}
			}
			if out.Type == "response.completed" {
				completed = out.Data
			}
		}
	}
	var final struct {
		Response struct {
			Output []map[string]any `json:"output"`
		} `json:"response"`
	}
	if err := json.Unmarshal(completed, &final); err != nil {
		t.Fatal(err)
	}
	if len(final.Response.Output) != 1 || final.Response.Output[0]["type"] != "message" {
		data, _ := json.Marshal(final)
		t.Fatalf("completed 应只含文本 item（thinking 丢弃）: %s", data)
	}
}
