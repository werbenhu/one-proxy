package anthropic

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

// 请求方向的保真：未知顶层字段、未知 block 类型、cache_control 原样往返。
// 比较采用语义等价（解析为 any 再比较），不比较空白字节。
func TestRequestRoundtripFidelity(t *testing.T) {
	raw := []byte(`{
		"model": "kimi-k3",
		"max_tokens": 1024,
		"top_k": 7,
		"stream": true,
		"system": [{"type": "text", "text": "You are helpful", "cache_control": {"type": "ephemeral"}}],
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "hi"},
				{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "aGk="}},
				{"type": "server_tool_use", "id": "toolu_1", "name": "future_tool", "input": {"q": 1}},
				{"type": "future_block_kind", "x": 1}
			]}
		],
		"tools": [{"name": "get_weather", "description": "d", "input_schema": {"type": "object"}}],
		"thinking": {"type": "enabled", "budget_tokens": 2048}
	}`)
	req, err := ParseRequest(raw)
	if err != nil {
		t.Fatalf("ParseRequest: %v", err)
	}
	if req.Model != "kimi-k3" || !req.Stream {
		t.Fatalf("model/stream 解析错误: %s %v", req.Model, req.Stream)
	}
	out, err := WriteRequestBytes(req)
	if err != nil {
		t.Fatalf("WriteRequestBytes: %v", err)
	}
	var in, outMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out, &outMap); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"top_k", "system", "thinking", "tools"} {
		if !jsonEqual(in[key], outMap[key]) {
			t.Errorf("字段 %s 往返不一致:\n in=%s\nout=%s", key, in[key], outMap[key])
		}
	}
	// messages 数组语义等价（content 不深度解析的保真证明）
	if !jsonEqual(in["messages"], outMap["messages"]) {
		t.Errorf("messages 往返不一致:\n in=%s\nout=%s", in["messages"], outMap["messages"])
	}
}

// jsonEqual 比较两段 JSON 的语义等价性（忽略空白与键顺序）。
func jsonEqual(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	aj, _ := json.Marshal(av)
	bj, _ := json.Marshal(bv)
	return bytes.Equal(aj, bj)
}

// 简单字符串 system 与字符串 content 也要原样保留形态。
func TestRequestScalarContentFidelity(t *testing.T) {
	raw := []byte(`{"model":"m","max_tokens":8,"system":"be brief","messages":[{"role":"user","content":"hello"}]}`)
	req, err := ParseRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := WriteRequestBytes(req)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if string(m["system"]) != `"be brief"` {
		t.Errorf("system 标量保真失败: %s", m["system"])
	}
	if string(m["messages"]) != `[{"role":"user","content":"hello"}]` {
		t.Errorf("messages 标量保真失败: %s", m["messages"])
	}
}

// 响应方向：未知 block 类型原样输出、usage 解析。
func TestResponseRoundtripFidelity(t *testing.T) {
	raw := []byte(`{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "kimi-k3",
		"content": [
			{"type": "thinking", "thinking": "hmm", "signature": "sig1"},
			{"type": "text", "text": "answer", "citations": [{"cited_text": "x"}]},
			{"type": "tool_use", "id": "toolu_1", "name": "f", "input": {"a": 1}},
			{"type": "redacted_thinking", "data": "xxx"}
		],
		"stop_reason": "tool_use",
		"usage": {"input_tokens": 10, "output_tokens": 20, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 4},
		"new_response_field": {"a": [1]}
	}`)
	resp, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 20 ||
		resp.Usage.CacheCreationInputTokens != 3 || resp.Usage.CacheReadInputTokens != 4 {
		t.Fatalf("usage 解析错误: %+v", resp.Usage)
	}
	out, err := WriteResponseBytes(resp)
	if err != nil {
		t.Fatal(err)
	}
	var in, outMap map[string]json.RawMessage
	_ = json.Unmarshal(raw, &in)
	_ = json.Unmarshal(out, &outMap)
	if !jsonEqual(in["new_response_field"], outMap["new_response_field"]) {
		t.Errorf("未知顶层字段丢失: %s vs %s", in["new_response_field"], outMap["new_response_field"])
	}
	var contents []map[string]json.RawMessage
	_ = json.Unmarshal(outMap["content"], &contents)
	if len(contents) != 4 {
		t.Fatalf("content block 数量错误: %d", len(contents))
	}
	if string(contents[0]["thinking"]) != `"hmm"` || string(contents[0]["signature"]) != `"sig1"` {
		t.Errorf("thinking block 保真失败: %v", contents[0])
	}
	if string(contents[2]["input"]) != `{"a":1}` {
		t.Errorf("tool_use input 保真失败: %s", contents[2]["input"])
	}
	if string(contents[3]["type"]) != `"redacted_thinking"` || string(contents[3]["data"]) != `"xxx"` {
		t.Errorf("redacted_thinking 保真失败: %v", contents[3])
	}
}

func TestContentBlockKnownTypes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"text", `{"type":"text","text":"hi","citations":[{"cited_text":"x"}]}`},
		{"thinking", `{"type":"thinking","thinking":"h","signature":"s"}`},
		{"tool_use", `{"type":"tool_use","id":"t1","name":"n","input":{"a":1}}`},
		{"image", `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}`},
	}
	for _, c := range cases {
		var cb ContentBlock
		if err := json.Unmarshal([]byte(c.raw), &cb); err != nil {
			t.Fatalf("%s unmarshal: %v", c.name, err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(mustMarshal(t, &cb), &m); err != nil {
			t.Fatalf("%s remarshal: %v", c.name, err)
		}
		var want map[string]json.RawMessage
		_ = json.Unmarshal([]byte(c.raw), &want)
		for k, v := range want {
			got := m[k]
			var gv, wv any
			_ = json.Unmarshal(got, &gv)
			_ = json.Unmarshal(v, &wv)
			gj, _ := json.Marshal(gv)
			wj, _ := json.Marshal(wv)
			if !bytes.Equal(gj, wj) {
				t.Errorf("%s block 字段 %s 不一致: %s vs %s", c.name, k, gj, wj)
			}
		}
	}
}

func TestHeaderBag(t *testing.T) {
	req := &Request{Model: "m", MaxTokens: 8}
	req.Header = http.Header{}
	req.Header.Set("anthropic-beta", "context-1m-2025-08-07")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("authorization", "must-not-leak")
	if req.Header.Get("anthropic-beta") == "" {
		t.Fatal("header bag 未保存")
	}
}

func TestEventString(t *testing.T) {
	e := Event{Type: "content_block_delta", Raw: json.RawMessage(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"a"}}`)}
	if e.Type != "content_block_delta" {
		t.Fatalf("event type: %s", e.Type)
	}
}

func TestUsageExtractFromEvent(t *testing.T) {
	start := Event{Type: "message_start", Raw: json.RawMessage(`{"type":"message_start","message":{"usage":{"input_tokens":5,"cache_read_input_tokens":2}}}`)}
	delta := Event{Type: "message_delta", Raw: json.RawMessage(`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`)}
	u := Usage{}
	u.MergeEvent(start)
	u.MergeEvent(delta)
	if u.InputTokens != 5 || u.CacheReadInputTokens != 2 || u.OutputTokens != 7 {
		t.Fatalf("MergeEvent 结果错误: %+v", u)
	}
}
