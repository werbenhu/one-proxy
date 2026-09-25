package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"hash/fnv"
	"net"
	"strings"
	"time"
)

const (
	LocalKeyLength      = 16
	TypeAnthropicCompat = "anthropic-compat"
	TypeOpenAICompat    = "openai-compat"
	TypeGrok            = "grok"
	StrategyPriority    = "priority"
	StrategyRoundRobin  = "round-robin"
)

const localKeyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// ProviderAccount is one upstream account. Credentials and balance capability
// belong here, never on a public channel.
type ProviderAccount struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Vendor      string `json:"vendor,omitempty"`
	Type        string `json:"type"`
	BaseURL     string `json:"baseUrl"`
	APIKey      string `json:"apiKey,omitempty"`
	Extra       []byte `json:"extra,omitempty"`
	AuthMode    string `json:"authMode,omitempty"` // grok 专用：api_key | oauth（保存时落入 Extra，不单独持久化）
	BalanceKind string `json:"balanceKind,omitempty"`
	BalanceURL  string `json:"balanceUrl,omitempty"`
	BalanceKey  string `json:"balanceKey,omitempty"`
	ProxyURL    string `json:"proxyUrl,omitempty"` // 该提供商专用 HTTP 代理，空则走系统环境代理
	Enabled     bool   `json:"enabled"`
}

type ChannelTarget struct {
	ProviderID    string `json:"providerId"`
	UpstreamModel string `json:"upstreamModel"`
	Priority      int    `json:"priority"`
	Weight        int    `json:"weight,omitempty"`
	Enabled       bool   `json:"enabled"`
}

// Channel is the public routing surface. One channel exposes one model and can
// fan in to any number of provider accounts.
type Channel struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Model    string          `json:"model"`
	Strategy string          `json:"strategy"`
	Targets  []ChannelTarget `json:"targets"`
	Enabled  bool            `json:"enabled"`

	// Legacy fields are only used to migrate the original physical-channel model.
	Type         string            `json:"type,omitempty"`
	BaseURL      string            `json:"baseUrl,omitempty"`
	APIKey       string            `json:"apiKey,omitempty"`
	Extra        []byte            `json:"extra,omitempty"`
	Models       []string          `json:"models,omitempty"`
	ModelMapping map[string]string `json:"modelMapping,omitempty"`
	Priority     int               `json:"priority,omitempty"`
}

type Config struct {
	ListenHost string            `json:"listenHost"`
	ListenPort int               `json:"listenPort"`
	LocalKey   string            `json:"localKey"`
	RetainDays int               `json:"retainDays"`
	Theme      string            `json:"theme,omitempty"`    // dark | light（默认 dark）
	Language   string            `json:"language,omitempty"` // zh | en（默认 zh）
	Providers  []ProviderAccount `json:"providers"`
	Channels   []Channel         `json:"channels"`
}

func Default() Config {
	return Config{ListenHost: "127.0.0.1", ListenPort: 8280, LocalKey: GenerateLocalKey(LocalKeyLength), RetainDays: 90, Providers: []ProviderAccount{}, Channels: []Channel{}}
}

func GenerateLocalKey(length int) string {
	if length <= 0 {
		length = LocalKeyLength
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())[:length]
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = localKeyAlphabet[int(b)%len(localKeyAlphabet)]
	}
	return string(out)
}

func (c Config) Address() string {
	return net.JoinHostPort(strings.TrimSpace(c.ListenHost), fmt.Sprint(c.ListenPort))
}

func (c Config) Channel(id string) (Channel, bool) {
	for _, ch := range c.Channels {
		if ch.ID == id {
			return ch, true
		}
	}
	return Channel{}, false
}

func (c Config) Provider(id string) (ProviderAccount, bool) {
	for _, p := range c.Providers {
		if p.ID == id {
			return p, true
		}
	}
	return ProviderAccount{}, false
}

func ValidType(t string) bool {
	switch t {
	case TypeAnthropicCompat, TypeOpenAICompat, TypeGrok:
		return true
	}
	return false
}

// Normalize migrates v1 physical channels into provider accounts and groups
// their declared models into public routing channels.
func Normalize(c Config) Config {
	if c.RetainDays < 1 {
		c.RetainDays = 90
	}
	// 旧预设的冗长账户名迁移为短名（仅当用户未改过名时生效）
	legacyNames := map[string]string{
		"Command Code Provider API（OpenAI）":    "Command Code（OpenAI）",
		"Command Code Provider API（Anthropic）": "Command Code（Anthropic）",
	}
	for i, p := range c.Providers {
		if name, ok := legacyNames[p.Name]; ok {
			c.Providers[i].Name = name
		}
	}
	var legacy, modern []Channel
	for _, ch := range c.Channels {
		if ch.Type != "" {
			legacy = append(legacy, ch)
		} else {
			modern = append(modern, ch)
		}
	}
	if len(legacy) == 0 {
		return c
	}
	providers := append([]ProviderAccount(nil), c.Providers...)
	providerIDs := map[string]bool{}
	for _, p := range providers {
		providerIDs[p.ID] = true
	}
	byModel := map[string]int{}
	for i, ch := range modern {
		byModel[ch.Model] = i
	}
	for _, old := range legacy {
		providerID := old.ID
		if !providerIDs[providerID] {
			vendor, balanceKind, balanceURL := inferProviderMetadata(old.BaseURL)
			providers = append(providers, ProviderAccount{ID: providerID, Name: old.Name, Vendor: vendor, Type: old.Type, BaseURL: old.BaseURL, APIKey: old.APIKey, Extra: old.Extra, BalanceKind: balanceKind, BalanceURL: balanceURL, Enabled: old.Enabled})
			providerIDs[providerID] = true
		}
		for _, model := range old.Models {
			idx, exists := byModel[model]
			if !exists {
				modern = append(modern, Channel{ID: migratedChannelID(model), Name: model, Model: model, Strategy: StrategyPriority, Enabled: old.Enabled})
				idx = len(modern) - 1
				byModel[model] = idx
			}
			if old.Enabled {
				modern[idx].Enabled = true
			}
			upstream := model
			if mapped := old.ModelMapping[model]; mapped != "" {
				upstream = mapped
			}
			modern[idx].Targets = append(modern[idx].Targets, ChannelTarget{ProviderID: providerID, UpstreamModel: upstream, Priority: old.Priority, Weight: 1, Enabled: old.Enabled})
		}
	}
	c.Providers, c.Channels = providers, modern
	return c
}

func inferProviderMetadata(baseURL string) (vendor, balanceKind, balanceURL string) {
	host := strings.ToLower(baseURL)
	switch {
	case strings.Contains(host, "api.z.ai"):
		return "zai", "zai-coding", ""
	case strings.Contains(host, "bigmodel.cn"):
		return "bigmodel", "zai-coding", "https://bigmodel.cn/api/monitor/usage/quota/limit"
	case strings.Contains(host, "moonshot"):
		return "kimi", "moonshot", ""
	case strings.Contains(host, "minimax"):
		return "minimax", "minimax-coding", ""
	case strings.Contains(host, "deepseek"):
		return "deepseek", "deepseek", ""
	case strings.Contains(host, "openrouter"):
		return "openrouter", "openrouter", ""
	}
	return "", "", ""
}

func migratedChannelID(model string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(model))
	return fmt.Sprintf("ch-route-%08x", h.Sum32())
}

func Validate(c Config) error {
	if err := validateLegacy(c.Channels); err != nil {
		return err
	}
	c = Normalize(c)
	host := strings.TrimSpace(c.ListenHost)
	if host == "" {
		return errors.New("监听地址不能为空")
	}
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil && !strings.EqualFold(host, "localhost") {
		return fmt.Errorf("监听地址 %q 无效", c.ListenHost)
	}
	if c.ListenPort < 1 || c.ListenPort > 65535 {
		return errors.New("监听端口必须在 1 到 65535 之间")
	}
	if strings.TrimSpace(c.LocalKey) == "" {
		return errors.New("本地代理密钥不能为空")
	}

	providerIDs := map[string]bool{}
	for i, p := range c.Providers {
		if strings.TrimSpace(p.ID) == "" {
			return fmt.Errorf("提供商 %d ID 不能为空", i+1)
		}
		if providerIDs[p.ID] {
			return fmt.Errorf("提供商 ID %q 重复", p.ID)
		}
		providerIDs[p.ID] = true
		if !ValidType(p.Type) {
			return fmt.Errorf("提供商 %s 类型无效: %q", p.ID, p.Type)
		}
		if p.Type != TypeGrok && strings.TrimSpace(p.BaseURL) == "" {
			return fmt.Errorf("提供商 %s 缺少 BaseURL", p.ID)
		}
		if p.Enabled && p.Type != TypeGrok && strings.TrimSpace(p.APIKey) == "" {
			return fmt.Errorf("提供商 %s 已启用但缺少 API Key", p.ID)
		}
	}
	channelIDs, models := map[string]bool{}, map[string]bool{}
	for i, ch := range c.Channels {
		if !strings.HasPrefix(ch.ID, "ch-") || len(ch.ID) <= 3 {
			return fmt.Errorf("渠道 %d ID 必须以 ch- 开头且非空", i+1)
		}
		if channelIDs[ch.ID] {
			return fmt.Errorf("渠道 ID %q 重复", ch.ID)
		}
		channelIDs[ch.ID] = true
		if strings.TrimSpace(ch.Model) == "" {
			return fmt.Errorf("渠道 %s 缺少对外模型", ch.ID)
		}
		if ch.Enabled && models[ch.Model] {
			return fmt.Errorf("对外模型 %q 被多个启用渠道重复暴露", ch.Model)
		}
		if ch.Enabled {
			models[ch.Model] = true
		}
		if ch.Strategy != StrategyPriority && ch.Strategy != StrategyRoundRobin {
			return fmt.Errorf("渠道 %s 调度策略无效", ch.ID)
		}
		if ch.Enabled && len(ch.Targets) == 0 {
			return fmt.Errorf("渠道 %s 没有内部目标", ch.ID)
		}
		enabledTargets := 0
		targetIDs := map[string]bool{}
		for _, target := range ch.Targets {
			if !providerIDs[target.ProviderID] {
				return fmt.Errorf("渠道 %s 引用了不存在的提供商 %s", ch.ID, target.ProviderID)
			}
			if targetIDs[target.ProviderID] {
				return fmt.Errorf("渠道 %s 重复绑定提供商 %s", ch.ID, target.ProviderID)
			}
			targetIDs[target.ProviderID] = true
			if strings.TrimSpace(target.UpstreamModel) == "" {
				return fmt.Errorf("渠道 %s 的上游模型不能为空", ch.ID)
			}
			if target.Weight < 0 || target.Weight > 100 {
				return fmt.Errorf("渠道 %s 的权重必须在 0 到 100 之间", ch.ID)
			}
			if target.Enabled {
				enabledTargets++
			}
		}
		if ch.Enabled && enabledTargets == 0 {
			return fmt.Errorf("渠道 %s 没有已启用的内部目标", ch.ID)
		}
	}
	return nil
}

func validateLegacy(channels []Channel) error {
	seen := map[string]bool{}
	for i, ch := range channels {
		if ch.Type == "" {
			continue
		}
		if !strings.HasPrefix(ch.ID, "ch-") || len(ch.ID) <= 3 {
			return fmt.Errorf("渠道 %d ID 必须以 ch- 开头且非空", i+1)
		}
		if seen[ch.ID] {
			return fmt.Errorf("渠道 ID %q 重复", ch.ID)
		}
		seen[ch.ID] = true
		if !ValidType(ch.Type) {
			return fmt.Errorf("渠道 %s 类型无效: %q", ch.ID, ch.Type)
		}
		if ch.Type != TypeGrok && strings.TrimSpace(ch.BaseURL) == "" {
			return fmt.Errorf("渠道 %s 缺少 BaseURL", ch.ID)
		}
		if ch.Enabled && ch.Type != TypeGrok && strings.TrimSpace(ch.APIKey) == "" {
			return fmt.Errorf("渠道 %s 已启用但缺少 API Key", ch.ID)
		}
		if ch.Enabled && len(ch.Models) == 0 {
			return fmt.Errorf("渠道 %s 未声明任何对外模型", ch.ID)
		}
	}
	return nil
}

func MaskKey(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if len(v) <= 8 {
		return "••••"
	}
	return v[:4] + "••••" + v[len(v)-4:]
}
