// Package service 业务编排层：Wails bindings 与底层包之间的胶水。
package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/proxy"
	"github.com/werbenhu/one-proxy/internal/usage"
)

// ChannelView 渠道展示视图（脱敏）。
type ChannelView struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	BaseURL      string            `json:"baseUrl"`
	APIKeyHint   string            `json:"apiKeyHint"`
	HasExtra     bool              `json:"hasExtra"`
	Models       []string          `json:"models"`
	ModelMapping map[string]string `json:"modelMapping"`
	Priority     int               `json:"priority"`
	Enabled      bool              `json:"enabled"`
	Status       string            `json:"status"`
	CoolingUntil string            `json:"coolingUntil,omitempty"`
	FailReason   string            `json:"failReason,omitempty"`
	TodayTokens  int64             `json:"todayTokens"`
}

// SettingsView 设置视图（密钥脱敏）。
type SettingsView struct {
	ListenHost string `json:"listenHost"`
	ListenPort int    `json:"listenPort"`
	LocalKey   string `json:"localKey"` // 本地密钥需要展示给用户复制
	RetainDays int    `json:"retainDays"`
	Running    bool   `json:"running"`
}

// PresetView 提供商预设（添加向导用）。
type PresetView struct {
	Key         string            `json:"key"`
	DisplayName string            `json:"displayName"`
	Type        string            `json:"type"`
	BaseURL     string            `json:"baseUrl"`
	Models      []string          `json:"models"`
	Mapping     map[string]string `json:"mapping,omitempty"`
	DocsURL     string            `json:"docsUrl,omitempty"`
}

type Service struct {
	store    *config.Store
	registry *provider.Registry
	server   *proxy.Server
}

func New(store *config.Store, registry *provider.Registry, server *proxy.Server) *Service {
	return &Service{store: store, registry: registry, server: server}
}

// Presets 内置提供商预设（数据而非代码；新增供应商改这里即可）。
func Presets() []PresetView {
	return []PresetView{
		{Key: "zai-coding", DisplayName: "z.ai Coding Plan", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.z.ai/api/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "glm-5.3"}, DocsURL: "https://docs.z.ai/devpack/overview"},
		{Key: "bigmodel-coding", DisplayName: "智谱 BigModel Coding Plan", Type: config.TypeAnthropicCompat,
			BaseURL: "https://open.bigmodel.cn/api/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "glm-5.3"}, DocsURL: "https://docs.bigmodel.cn/cn/coding-plan/quick-start"},
		{Key: "kimi-coding", DisplayName: "Kimi (Moonshot)", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.moonshot.cn/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}, DocsURL: "https://platform.kimi.com/docs/guide/claude-code-kimi"},
		{Key: "kimi-intl", DisplayName: "Kimi 国际站", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.moonshot.ai/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}},
		{Key: "minimax-token", DisplayName: "MiniMax Token Plan", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.minimax.io/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "MiniMax-M3"}, DocsURL: "https://platform.minimax.io/docs/token-plan/intro"},
		{Key: "deepseek", DisplayName: "DeepSeek", Type: config.TypeOpenAICompat,
			BaseURL: "https://api.deepseek.com", Models: []string{"deepseek-chat"}, DocsURL: "https://api-docs.deepseek.com"},
		{Key: "openrouter", DisplayName: "OpenRouter", Type: config.TypeOpenAICompat,
			BaseURL: "https://openrouter.ai/api/v1", Models: []string{"openai/gpt-5.2"}, DocsURL: "https://openrouter.ai/docs"},
		{Key: "opencode-zen", DisplayName: "OpenCode Zen", Type: config.TypeOpenAICompat,
			BaseURL: "https://opencode.ai/zen/v1", Models: []string{}, DocsURL: "https://opencode.ai/docs/zen"},
		{Key: "commandcode", DisplayName: "Command Code (GOAT)", Type: config.TypeOpenAICompat,
			BaseURL: "https://api.commandcode.ai/provider/v1", Models: []string{}, DocsURL: "https://commandcode.ai/docs/provider"},
		{Key: "grok", DisplayName: "Grok (xAI)", Type: config.TypeGrok,
			BaseURL: "", Models: []string{"grok-4.5"}, DocsURL: "https://x.ai"},
		{Key: "custom-anthropic", DisplayName: "自定义 Anthropic 兼容", Type: config.TypeAnthropicCompat,
			BaseURL: "", Models: []string{}},
		{Key: "custom-openai", DisplayName: "自定义 OpenAI 兼容", Type: config.TypeOpenAICompat,
			BaseURL: "", Models: []string{}},
	}
}

func (s *Service) Channels() []ChannelView {
	cfg := s.store.Get()
	states := s.registry.Snapshot()
	var today map[string]int64
	if us := s.server.UsageStore(); us != nil {
		today, _ = us.ChannelToday(time.Now())
	}
	out := make([]ChannelView, 0, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		v := ChannelView{
			ID: ch.ID, Name: ch.Name, Type: ch.Type, BaseURL: ch.BaseURL,
			APIKeyHint: config.MaskKey(ch.APIKey), HasExtra: len(ch.Extra) > 0,
			Models: ch.Models, ModelMapping: ch.ModelMapping,
			Priority: ch.Priority, Enabled: ch.Enabled,
			TodayTokens: today[ch.ID],
		}
		if st, ok := states[ch.ID]; ok {
			v.Status = st.Status
			if st.Status == provider.StatusCooling {
				v.CoolingUntil = st.CoolingUntil.Format("15:04:05")
			}
			v.FailReason = st.FailReason
		} else {
			v.Status = provider.StatusOK
		}
		out = append(out, v)
	}
	return out
}

// SaveChannel 新增或更新渠道（ID 冲突时更新）。
func (s *Service) SaveChannel(ch config.Channel) error {
	if ch.ID == "" {
		ch.ID = fmt.Sprintf("ch-%s", randomID(6))
	}
	return s.store.Update(func(c *config.Config) {
		replaced := false
		for i := range c.Channels {
			if c.Channels[i].ID == ch.ID {
				c.Channels[i] = ch
				replaced = true
				break
			}
		}
		if !replaced {
			c.Channels = append(c.Channels, ch)
		}
	})
}

func (s *Service) DeleteChannel(id string) error {
	return s.store.Update(func(c *config.Config) {
		out := c.Channels[:0]
		for _, ch := range c.Channels {
			if ch.ID != id {
				out = append(out, ch)
			}
		}
		c.Channels = out
	})
}

func (s *Service) Settings() SettingsView {
	cfg := s.store.Get()
	return SettingsView{
		ListenHost: cfg.ListenHost, ListenPort: cfg.ListenPort,
		LocalKey: cfg.LocalKey, RetainDays: cfg.RetainDays,
	}
}

func (s *Service) SaveSettings(v SettingsView) error {
	return s.store.Update(func(c *config.Config) {
		c.ListenHost, c.ListenPort = v.ListenHost, v.ListenPort
		c.LocalKey, c.RetainDays = v.LocalKey, v.RetainDays
	})
}

// UsageSummary 用量聚合（rangeKey: today|7d|30d|all）。
func (s *Service) UsageSummary(rangeKey string) ([]usage.AggRow, error) {
	us := s.server.UsageStore()
	if us == nil {
		return []usage.AggRow{}, nil
	}
	var since time.Time
	switch rangeKey {
	case "today":
		now := time.Now()
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case "7d":
		since = time.Now().AddDate(0, 0, -7)
	case "30d":
		since = time.Now().AddDate(0, 0, -30)
	case "all", "":
	default:
		return nil, fmt.Errorf("无效时间范围 %q", rangeKey)
	}
	return us.Summary(since)
}

// TestChannel 连通性测试（发一个最小请求）。
func (s *Service) TestChannel(id string) error {
	adapter, ok := s.registry.Get(id)
	if !ok {
		return fmt.Errorf("渠道 %s 未注册（可能未启用）", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := adapter.Models(ctx)
	return err
}

// StartGrokDeviceAuth 发起 Grok 设备授权。
func (s *Service) StartGrokDeviceAuth(id string) (provider.DeviceAuthInfo, error) {
	adapter, ok := s.registry.Get(id)
	if !ok {
		return provider.DeviceAuthInfo{}, fmt.Errorf("渠道 %s 未注册", id)
	}
	oc, ok := adapter.(provider.OAuthCapable)
	if !ok {
		return provider.DeviceAuthInfo{}, fmt.Errorf("渠道 %s 不支持 OAuth", id)
	}
	return oc.StartDeviceAuth(context.Background())
}

// CompleteGrokDeviceAuth 轮询完成授权（成功后 token 由适配器写回配置）。
func (s *Service) CompleteGrokDeviceAuth(id, deviceCode string) error {
	adapter, ok := s.registry.Get(id)
	if !ok {
		return fmt.Errorf("渠道 %s 未注册", id)
	}
	oc, ok := adapter.(provider.OAuthCapable)
	if !ok {
		return fmt.Errorf("渠道 %s 不支持 OAuth", id)
	}
	return oc.PollDeviceAuth(context.Background(), deviceCode)
}

func randomID(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())[:n]
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}
