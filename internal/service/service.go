// Package service 业务编排层：Wails bindings 与底层包之间的胶水。
package service

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/proxy"
	"github.com/werbenhu/one-proxy/internal/usage"
)

// ProviderView is the redacted upstream-account view.
type ProviderView struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Vendor         string `json:"vendor"`
	Type           string `json:"type"`
	BaseURL        string `json:"baseUrl"`
	APIKeyHint     string `json:"apiKeyHint"`
	HasExtra       bool   `json:"hasExtra"`
	AuthMode       string `json:"authMode,omitempty"` // grok：api_key | oauth
	BalanceKind    string `json:"balanceKind"`
	BalanceURL     string `json:"balanceUrl"`
	BalanceKeyHint string `json:"balanceKeyHint"`
	UseProxy       bool   `json:"useProxy"`
	Enabled        bool   `json:"enabled"`
	Status         string `json:"status"`
	CoolingUntil   string `json:"coolingUntil,omitempty"`
	FailReason     string `json:"failReason,omitempty"`
	TodayTokens    int64  `json:"todayTokens"`
	WeekTokens     int64  `json:"weekTokens"`
	MonthTokens    int64  `json:"monthTokens"`
}

type ChannelView struct {
	ID       string                 `json:"id"`
	Name     string                 `json:"name"`
	Model    string                 `json:"model"`
	Strategy string                 `json:"strategy"`
	Targets  []config.ChannelTarget `json:"targets"`
	Enabled  bool                   `json:"enabled"`
	Healthy  int                    `json:"healthy"`
	Total    int                    `json:"total"`
}

// SettingsView 设置视图（密钥脱敏）。
type SettingsView struct {
	ListenHost  string `json:"listenHost"`
	ListenPort  int    `json:"listenPort"`
	LocalKey    string `json:"localKey"` // 本地密钥需要展示给用户复制
	RetainDays  int    `json:"retainDays"`
	Theme       string `json:"theme"`       // dark | light
	Language    string `json:"language"`    // zh | en
	GlobalProxy string `json:"globalProxy"` // 全局 HTTP 代理，提供商勾选「使用代理」时生效
	Running     bool   `json:"running"`
}

// PresetView 提供商预设（添加向导用）。
type PresetView struct {
	Key         string            `json:"key"`
	DisplayName string            `json:"displayName"`
	Type        string            `json:"type"`
	BaseURL     string            `json:"baseUrl"`
	Vendor      string            `json:"vendor"`
	BalanceKind string            `json:"balanceKind,omitempty"`
	BalanceURL  string            `json:"balanceUrl,omitempty"`
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
		{Key: "zai-coding", DisplayName: "z.ai Coding Plan", Vendor: "zai", BalanceKind: "zai-coding", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.z.ai/api/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "glm-5.3"}, DocsURL: "https://docs.z.ai/devpack/overview"},
		{Key: "bigmodel-coding", DisplayName: "智谱 BigModel Coding Plan", Vendor: "bigmodel", BalanceKind: "zai-coding", BalanceURL: "https://bigmodel.cn/api/monitor/usage/quota/limit", Type: config.TypeAnthropicCompat,
			BaseURL: "https://open.bigmodel.cn/api/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "glm-5.3"}, DocsURL: "https://docs.bigmodel.cn/cn/coding-plan/quick-start"},
		{Key: "kimi-code-plan", DisplayName: "Kimi Coding 套餐", Vendor: "kimi", BalanceKind: "kimi-coding", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.kimi.com/coding", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "kimi-for-coding"}, DocsURL: "https://www.kimi.com/code/docs"},
		{Key: "kimi-coding", DisplayName: "Kimi 开放平台（Moonshot）", Vendor: "kimi", BalanceKind: "moonshot", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.moonshot.cn/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}, DocsURL: "https://platform.kimi.com/docs/guide/claude-code-kimi"},
		{Key: "kimi-intl", DisplayName: "Kimi 国际站", Vendor: "kimi", BalanceKind: "moonshot", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.moonshot.ai/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "kimi-k3"}},
		{Key: "minimax-token", DisplayName: "MiniMax Token Plan", Vendor: "minimax", BalanceKind: "minimax-coding", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.minimax.io/anthropic", Models: []string{"claude-sonnet-4-6"},
			Mapping: map[string]string{"claude-sonnet-4-6": "MiniMax-M3"}, DocsURL: "https://platform.minimax.io/docs/token-plan/intro"},
		{Key: "deepseek", DisplayName: "DeepSeek", Vendor: "deepseek", BalanceKind: "deepseek", Type: config.TypeOpenAICompat,
			BaseURL: "https://api.deepseek.com", Models: []string{"deepseek-chat"}, DocsURL: "https://api-docs.deepseek.com"},
		{Key: "openrouter", DisplayName: "OpenRouter", Vendor: "openrouter", BalanceKind: "openrouter", Type: config.TypeOpenAICompat,
			BaseURL: "https://openrouter.ai/api/v1", Models: []string{"openai/gpt-5.2"}, DocsURL: "https://openrouter.ai/docs"},
		{Key: "opencode-zen", DisplayName: "OpenCode Zen", Vendor: "opencode", Type: config.TypeOpenAICompat,
			BaseURL: "https://opencode.ai/zen/v1", Models: []string{}, DocsURL: "https://opencode.ai/docs/zen"},
		{Key: "commandcode-provider", DisplayName: "Command Code（OpenAI）", Vendor: "commandcode", BalanceKind: "commandcode", Type: config.TypeOpenAICompat,
			BaseURL: "https://api.commandcode.ai/provider/v1", Models: []string{}, DocsURL: "https://commandcode.ai/docs/provider"},
		{Key: "commandcode-provider-anthropic", DisplayName: "Command Code（Anthropic）", Vendor: "commandcode", BalanceKind: "commandcode", Type: config.TypeAnthropicCompat,
			BaseURL: "https://api.commandcode.ai/provider", Models: []string{}, DocsURL: "https://commandcode.ai/docs/provider"},
		{Key: "ollama-cloud", DisplayName: "Ollama Cloud", Vendor: "ollama", Type: config.TypeOpenAICompat,
			BaseURL: "https://ollama.com/v1", Models: []string{}, DocsURL: "https://docs.ollama.com/api/openai-compatibility"},
		{Key: "grok", DisplayName: "Grok (xAI)", Vendor: "xai", Type: config.TypeGrok, BalanceKind: "grok",
			BaseURL: "", Models: []string{"grok-4.5"}, DocsURL: "https://x.ai"},
		{Key: "custom-anthropic", DisplayName: "自定义 Anthropic 兼容", Type: config.TypeAnthropicCompat,
			BaseURL: "", Models: []string{}},
		{Key: "custom-openai", DisplayName: "自定义 OpenAI 兼容", Type: config.TypeOpenAICompat,
			BaseURL: "", Models: []string{}},
	}
}

func (s *Service) Providers() []ProviderView {
	cfg := s.store.Get()
	states := s.registry.Snapshot()
	var today, week, month map[string]int64
	if us := s.server.UsageStore(); us != nil {
		today, week, month, _ = us.ProviderTokens(time.Now())
	}
	out := make([]ProviderView, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		v := ProviderView{
			ID: p.ID, Name: p.Name, Vendor: p.Vendor, Type: p.Type, BaseURL: p.BaseURL,
			APIKeyHint: config.MaskKey(p.APIKey), HasExtra: len(p.Extra) > 0,
			BalanceKind: p.BalanceKind, BalanceURL: p.BalanceURL, BalanceKeyHint: config.MaskKey(p.BalanceKey), Enabled: p.Enabled,
			UseProxy:    p.UseProxy,
			TodayTokens: today[p.ID], WeekTokens: week[p.ID], MonthTokens: month[p.ID],
		}
		if p.Type == config.TypeGrok {
			var extra struct {
				Mode string `json:"mode"`
			}
			_ = json.Unmarshal(p.Extra, &extra)
			v.AuthMode = extra.Mode
		}
		if st, ok := states[p.ID]; ok {
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

func (s *Service) Channels() []ChannelView {
	cfg := s.store.Get()
	states := s.registry.Snapshot()
	out := make([]ChannelView, 0, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		v := ChannelView{ID: ch.ID, Name: ch.Name, Model: ch.Model, Strategy: ch.Strategy, Targets: ch.Targets, Enabled: ch.Enabled, Total: len(ch.Targets)}
		for _, target := range ch.Targets {
			p, ok := cfg.Provider(target.ProviderID)
			if !ok || !p.Enabled || !target.Enabled {
				continue
			}
			if st, exists := states[p.ID]; !exists || st.Status == provider.StatusOK {
				v.Healthy++
			}
		}
		out = append(out, v)
	}
	return out
}

// ProviderKeysView 编辑提供商时用于查看已保存的密钥原文。
type ProviderKeysView struct {
	APIKey     string `json:"apiKey"`
	BalanceKey string `json:"balanceKey"`
}

func (s *Service) ProviderKeys(id string) (ProviderKeysView, error) {
	p, ok := s.store.Get().Provider(id)
	if !ok {
		return ProviderKeysView{}, fmt.Errorf("provider %s does not exist", id)
	}
	return ProviderKeysView{APIKey: p.APIKey, BalanceKey: p.BalanceKey}, nil
}

// ExportProviders 导出全部提供商（含 API Key 与 OAuth 凭据，用于备份迁移）。
func (s *Service) ExportProviders() ([]byte, error) {
	return json.MarshalIndent(s.store.Get().Providers, "", "  ")
}

// ImportProviders 合并导入提供商：ID 为空、已存在或协议类型无效的跳过。
// 返回新增与跳过数量。
func (s *Service) ImportProviders(data []byte) (added, skipped int, err error) {
	var incoming []config.ProviderAccount
	if err := json.Unmarshal(data, &incoming); err != nil {
		return 0, 0, fmt.Errorf("import file is not valid provider JSON: %w", err)
	}
	err = s.store.Update(func(c *config.Config) {
		existing := map[string]bool{}
		for _, p := range c.Providers {
			existing[p.ID] = true
		}
		for _, p := range incoming {
			if p.ID == "" || existing[p.ID] || !config.ValidType(p.Type) {
				skipped++
				continue
			}
			existing[p.ID] = true
			c.Providers = append(c.Providers, p)
			added++
		}
	})
	if err != nil {
		return 0, 0, err
	}
	return added, skipped, nil
}

func (s *Service) SaveProvider(p config.ProviderAccount) error {
	if p.ID == "" {
		p.ID = fmt.Sprintf("pv-%s", randomID(6))
	}
	return s.store.Update(func(c *config.Config) {
		for i := range c.Providers {
			if c.Providers[i].ID != p.ID {
				continue
			}
			if p.APIKey == "" {
				p.APIKey = c.Providers[i].APIKey
			}
			if p.BalanceKey == "" {
				p.BalanceKey = c.Providers[i].BalanceKey
			}
			if len(p.Extra) == 0 {
				p.Extra = c.Providers[i].Extra
			}
			applyGrokAuthMode(&p)
			c.Providers[i] = p
			return
		}
		applyGrokAuthMode(&p)
		c.Providers = append(c.Providers, p)
	})
}

// applyGrokAuthMode 把 UI 选择的 grok 授权方式落入 Extra.Mode；未显式选择时
// 有 API Key 用 api_key，否则默认 oauth。AuthMode 是瞬态字段，不入库。
func applyGrokAuthMode(p *config.ProviderAccount) {
	if p.Type != config.TypeGrok {
		return
	}
	mode := p.AuthMode
	p.AuthMode = ""
	if mode == "" {
		var extra struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal(p.Extra, &extra)
		mode = extra.Mode
	}
	if mode == "" {
		if p.APIKey != "" {
			mode = "api_key"
		} else {
			mode = "oauth"
		}
	}
	var extra map[string]any
	_ = json.Unmarshal(p.Extra, &extra)
	if extra == nil {
		extra = map[string]any{}
	}
	extra["mode"] = mode
	p.Extra, _ = json.Marshal(extra)
}

func (s *Service) DeleteProvider(id string) error {
	for _, ch := range s.store.Get().Channels {
		for _, target := range ch.Targets {
			if target.ProviderID == id {
				return fmt.Errorf("provider is still used by channel %s; remove the binding first", ch.Name)
			}
		}
	}
	return s.store.Update(func(c *config.Config) {
		out := c.Providers[:0]
		for _, p := range c.Providers {
			if p.ID != id {
				out = append(out, p)
			}
		}
		c.Providers = out
	})
}

// ReorderProviders 按给定 ID 顺序重排提供商（列表拖拽排序）；未出现在 ids
// 里的保持相对顺序排在末尾。
func (s *Service) ReorderProviders(ids []string) error {
	return s.store.Update(func(c *config.Config) {
		pos := make(map[string]int, len(ids))
		for i, id := range ids {
			pos[id] = i
		}
		sort.SliceStable(c.Providers, func(i, j int) bool {
			pi, iok := pos[c.Providers[i].ID]
			pj, jok := pos[c.Providers[j].ID]
			if iok != jok {
				return iok
			}
			return pi < pj
		})
	})
}

// SaveChannel 保存渠道。originalID 非空且与 ch.ID 不同表示改名：移除旧条目。
func (s *Service) SaveChannel(ch config.Channel, originalID string) error {
	ch.ID = strings.TrimSpace(ch.ID)
	if ch.ID == "" {
		ch.ID = fmt.Sprintf("ch-%s", randomID(6))
	}
	if originalID != "" && originalID != ch.ID {
		for _, old := range s.store.Get().Channels {
			if old.ID == ch.ID {
				return fmt.Errorf("channel ID %q already exists", ch.ID)
			}
		}
	}
	if ch.Strategy == "" {
		ch.Strategy = config.StrategyPriority
	}
	return s.store.Update(func(c *config.Config) {
		if originalID != "" && originalID != ch.ID {
			out := c.Channels[:0]
			for _, old := range c.Channels {
				if old.ID != originalID {
					out = append(out, old)
				}
			}
			c.Channels = out
		}
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
		Theme: cfg.Theme, Language: cfg.Language,
		GlobalProxy: cfg.GlobalProxy,
	}
}

func (s *Service) SaveSettings(v SettingsView) error {
	return s.store.Update(func(c *config.Config) {
		c.ListenHost, c.ListenPort = v.ListenHost, v.ListenPort
		c.LocalKey, c.RetainDays = v.LocalKey, v.RetainDays
		c.GlobalProxy = strings.TrimSpace(v.GlobalProxy)
		switch v.Theme {
		case "dark", "light":
			c.Theme = v.Theme
		}
		switch v.Language {
		case "zh", "en":
			c.Language = v.Language
		}
	})
}

// UsageSummary 用量聚合（rangeKey: today|7d|30d|all）。
func (s *Service) UsageSummary(rangeKey string) ([]usage.AggRow, error) {
	us := s.server.UsageStore()
	if us == nil {
		return []usage.AggRow{}, nil
	}
	since, err := usageSince(rangeKey)
	if err != nil {
		return nil, err
	}
	return us.Summary(since)
}

// UsageDaily 全部提供商逐日 × 上游模型 token（用量页趋势图）。
func (s *Service) UsageDaily(rangeKey string) ([]usage.ModelDayTokens, error) {
	us := s.server.UsageStore()
	if us == nil {
		return []usage.ModelDayTokens{}, nil
	}
	since, err := usageSince(rangeKey)
	if err != nil {
		return nil, err
	}
	return us.DailyByModel(since)
}

// UsageHourlyToday 今日逐小时 × 上游模型 token（今日分布图用）。
func (s *Service) UsageHourlyToday() ([]usage.ModelHourTokens, error) {
	us := s.server.UsageStore()
	if us == nil {
		return []usage.ModelHourTokens{}, nil
	}
	now := time.Now()
	since := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return us.HourlyByModel(since)
}

// usageSince 解析用量页时间范围（today/7d/30d/all）。
func usageSince(rangeKey string) (time.Time, error) {
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
		return since, fmt.Errorf("invalid time range %q", rangeKey)
	}
	return since, nil
}

// ProviderUsageView 单提供商用量详情（列表「详情」弹窗）。
type ProviderUsageView struct {
	TodayTokens int64                  `json:"todayTokens"`
	WeekTokens  int64                  `json:"weekTokens"`
	MonthTokens int64                  `json:"monthTokens"`
	TotalTokens int64                  `json:"totalTokens"` // 累计（日志保留期内）
	Daily       []usage.DayTokens      `json:"daily"`       // 近 12 个月逐日，热力图用
	ModelDaily  []usage.ModelDayTokens `json:"modelDaily"`  // 近 30 天逐日×上游模型，趋势图用
	Models      []usage.ModelStat      `json:"models"`      // 按上游模型的累计消耗
}

// ProviderUsage 汇总某提供商的用量详情；用量存储不可用时返回空视图。
func (s *Service) ProviderUsage(id string) ProviderUsageView {
	v := ProviderUsageView{Daily: []usage.DayTokens{}, ModelDaily: []usage.ModelDayTokens{}, Models: []usage.ModelStat{}}
	us := s.server.UsageStore()
	if us == nil {
		return v
	}
	now := time.Now()
	today, week, month, _ := us.ProviderTokens(now)
	v.TodayTokens, v.WeekTokens, v.MonthTokens = today[id], week[id], month[id]
	if daily, err := us.ProviderDaily(id, now.AddDate(-1, 0, 0)); err == nil {
		v.Daily = daily
	}
	if modelDaily, err := us.ProviderDailyByModel(id, now.AddDate(0, 0, -30)); err == nil {
		v.ModelDaily = modelDaily
	}
	if models, err := us.ProviderModelStats(id, time.Time{}); err == nil {
		v.Models = models
		for _, m := range models {
			v.TotalTokens += m.TotalTokens
		}
	}
	return v
}

func (s *Service) ProviderModels(id string) ([]provider.ModelInfo, error) {
	adapter, ok := s.registry.Get(id)
	if !ok {
		return nil, fmt.Errorf("provider %s is not registered (may be disabled)", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return adapter.Models(ctx)
}

func (s *Service) TestProvider(id string) error { _, err := s.ProviderModels(id); return err }

// TestChannel is kept as a compatibility alias for old Wails clients.
func (s *Service) TestChannel(id string) error { return s.TestProvider(id) }

// StartGrokDeviceAuth starts OAuth for a provider account.
func (s *Service) StartGrokDeviceAuth(id string) (provider.DeviceAuthInfo, error) {
	adapter, ok := s.registry.Get(id)
	if !ok {
		return provider.DeviceAuthInfo{}, fmt.Errorf("provider %s is not registered", id)
	}
	oc, ok := adapter.(provider.OAuthCapable)
	if !ok {
		return provider.DeviceAuthInfo{}, fmt.Errorf("provider %s does not support OAuth", id)
	}
	return oc.StartDeviceAuth(context.Background())
}

// CompleteGrokDeviceAuth 轮询完成授权（成功后 token 由适配器写回配置）。
func (s *Service) CompleteGrokDeviceAuth(id, deviceCode string) error {
	adapter, ok := s.registry.Get(id)
	if !ok {
		return fmt.Errorf("provider %s is not registered", id)
	}
	oc, ok := adapter.(provider.OAuthCapable)
	if !ok {
		return fmt.Errorf("provider %s does not support OAuth", id)
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
