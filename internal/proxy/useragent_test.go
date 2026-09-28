package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/werbenhu/one-proxy/internal/config"
)

// 客户端身份头（User-Agent）必须透传给上游：中转不改变来源，
// 上游日志应看到真实调用方（如 zcode），而非 Go-http-client/2.0。
func TestUserAgentForwardedToUpstream(t *testing.T) {
	cases := []struct {
		name    string
		proto   string // 入口路径
		pvType  string
		upPath  string // 上游收到的路径
		upResp  string // 上游响应体
		ct      string // 上游响应 Content-Type
	}{
		{
			name: "anthropic入口-anthropic上游", proto: "/v1/messages",
			pvType: config.TypeAnthropicCompat, upPath: "/v1/messages",
			upResp: `{"id":"msg_1","type":"message","role":"assistant","model":"m","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`,
			ct: "application/json",
		},
		{
			name: "chat入口-openai上游", proto: "/v1/chat/completions",
			pvType: config.TypeOpenAICompat, upPath: "/chat/completions",
			upResp: `{"id":"c1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
			ct: "application/json",
		},
		{
			name: "responses入口-grok上游直通", proto: "/v1/responses",
			pvType: config.TypeGrok, upPath: "/responses",
			upResp: `{"id":"resp_1","object":"response","model":"grok-4.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
			ct: "application/json",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotUA := "unset"
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != c.upPath {
					t.Errorf("上游路径: %s", r.URL.Path)
				}
				gotUA = r.Header.Get("User-Agent")
				w.Header().Set("Content-Type", c.ct)
				_, _ = io.WriteString(w, c.upResp)
			}))
			defer up.Close()
			ts, _ := newTestServer(t,
				[]config.ProviderAccount{{ID: "pv-1", Name: "P", Type: c.pvType, BaseURL: up.URL, APIKey: "k", Enabled: true}},
				[]config.Channel{{ID: "ch-1", Name: "C", Model: "my-model", Strategy: config.StrategyPriority, Enabled: true,
					Targets: []config.ChannelTarget{{ProviderID: "pv-1", UpstreamModel: "up-model", Enabled: true}}}})
			var body string
			switch c.proto {
			case "/v1/messages":
				body = `{"model":"my-model","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`
			case "/v1/chat/completions":
				body = `{"model":"my-model","messages":[{"role":"user","content":"hi"}]}`
			case "/v1/responses":
				body = `{"model":"my-model","input":"hi"}`
			}
			req, _ := http.NewRequest("POST", ts.URL+"/ch-1"+c.proto, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer testkey123")
			req.Header.Set("User-Agent", "zcode/1.0 test-client")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("状态: %d %s", resp.StatusCode, b)
			}
			if gotUA != "zcode/1.0 test-client" {
				t.Fatalf("User-Agent 未透传: %q", gotUA)
			}
		})
	}
}
