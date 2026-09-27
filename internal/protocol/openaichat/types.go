// Package openaichat 定义 OpenAI Chat Completions 协议类型（带未知字段保留）。
package openaichat

import "encoding/json"

// ChatRequest 入口请求；Messages 保持 RawMessage 以保留未知 content part 类型。
type ChatRequest struct {
	Model          string
	MaxTokens      int64
	Stream         bool
	Messages       json.RawMessage
	Tools          json.RawMessage
	ResponseFormat json.RawMessage
	Extra          map[string]json.RawMessage
}

type chatRequestJSON struct {
	Model          string                     `json:"model"`
	MaxTokens      *int64                     `json:"max_tokens,omitempty"`
	Stream         bool                       `json:"stream,omitempty"`
	Messages       json.RawMessage            `json:"messages"`
	Tools          json.RawMessage            `json:"tools,omitempty"`
	ResponseFormat json.RawMessage            `json:"response_format,omitempty"`
	Extra          map[string]json.RawMessage `json:"-"`
}

func ParseChatRequest(body []byte) (*ChatRequest, error) {
	var j chatRequestJSON
	if err := json.Unmarshal(body, &j); err != nil {
		return nil, err
	}
	req := &ChatRequest{
		Model: j.Model, MaxTokens: 0, Stream: j.Stream,
		Messages: j.Messages, Tools: j.Tools, ResponseFormat: j.ResponseFormat,
		Extra: map[string]json.RawMessage{},
	}
	if j.MaxTokens != nil {
		req.MaxTokens = *j.MaxTokens
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(body, &all); err != nil {
		return nil, err
	}
	for k, v := range all {
		switch k {
		case "model", "max_tokens", "stream", "messages", "tools", "response_format":
		default:
			req.Extra[k] = v
		}
	}
	return req, nil
}

// ChatResponse 完整响应（content 序列化后输出）。
type ChatResponse struct {
	ID           string                     `json:"id"`
	Object       string                     `json:"object"`
	Created      int64                      `json:"created"`
	Model        string                     `json:"model"`
	Content      string                     `json:"content"`
	ToolCalls    []ToolCall                 `json:"tool_calls,omitempty"`
	FinishReason string                     `json:"finish_reason"`
	Usage        *Usage                     `json:"usage,omitempty"`
	Extra        map[string]json.RawMessage `json:"-"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// Chunk SSE 流式片段。内部存顶层 Delta/Finish 便于各转换器构造;
// 序列化时包装为标准 OpenAI choices[0] 格式。
type Chunk struct {
	ID      string          `json:"id,omitempty"`
	Object  string          `json:"object,omitempty"`
	Created int64           `json:"created,omitempty"`
	Model   string          `json:"model,omitempty"`
	Delta   json.RawMessage `json:"delta"`
	Finish  *string         `json:"finish_reason,omitempty"`
	Usage   *Usage          `json:"usage,omitempty"`
}

// MarshalJSON 序列化为标准 OpenAI chat.completion.chunk 格式:
// delta 和 finish_reason 包在 choices[0] 里,而不是顶层字段。
func (c Chunk) MarshalJSON() ([]byte, error) {
	type choiceJSON struct {
		Index        int             `json:"index"`
		Delta        json.RawMessage `json:"delta"`
		FinishReason *string         `json:"finish_reason"`
	}
	type chunkJSON struct {
		ID      string       `json:"id,omitempty"`
		Object  string       `json:"object,omitempty"`
		Created int64        `json:"created,omitempty"`
		Model   string       `json:"model,omitempty"`
		Choices []choiceJSON `json:"choices"`
		Usage   *Usage       `json:"usage,omitempty"`
	}
	delta := c.Delta
	if len(delta) == 0 {
		delta = json.RawMessage(`{}`)
	}
	return json.Marshal(chunkJSON{
		ID: c.ID, Object: c.Object, Created: c.Created, Model: c.Model,
		Choices: []choiceJSON{{Index: 0, Delta: delta, FinishReason: c.Finish}},
		Usage:   c.Usage,
	})
}
