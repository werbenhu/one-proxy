package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.ListenHost != "127.0.0.1" || cfg.ListenPort != 8280 || cfg.RetainDays != 90 {
		t.Fatalf("默认值错误: %+v", cfg)
	}
	if len(cfg.LocalKey) != LocalKeyLength {
		t.Fatalf("LocalKey 长度错误: %d", len(cfg.LocalKey))
	}
}

func TestGenerateLocalKeyUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		k := GenerateLocalKey(LocalKeyLength)
		if seen[k] {
			t.Fatalf("密钥重复: %s", k)
		}
		seen[k] = true
	}
}

func TestValidateErrors(t *testing.T) {
	base := func() Config {
		c := Default()
		c.Channels = []Channel{{
			ID: "ch-kimi1", Name: "Kimi A", Type: TypeAnthropicCompat,
			BaseURL: "https://api.moonshot.cn/anthropic", APIKey: "k-1",
			Models: []string{"claude-sonnet-4-6"}, Enabled: true,
		}}
		return c
	}
	cases := []struct {
		name string
		mut  func(*Config)
		want string
	}{
		{"空 host", func(c *Config) { c.ListenHost = "" }, "监听地址"},
		{"非法端口", func(c *Config) { c.ListenPort = 0 }, "端口"},
		{"空密钥", func(c *Config) { c.LocalKey = "" }, "密钥"},
		{"渠道ID含斜杠", func(c *Config) { c.Channels[0].ID = "a/b" }, "不能包含"},
		{"渠道ID重复", func(c *Config) {
			c.Channels = append(c.Channels, c.Channels[0])
		}, "重复"},
		{"类型无效", func(c *Config) { c.Channels[0].Type = "xxx" }, "类型"},
		{"缺 BaseURL", func(c *Config) { c.Channels[0].BaseURL = "" }, "BaseURL"},
		{"启用缺 Key", func(c *Config) { c.Channels[0].APIKey = "" }, "API Key"},
		{"启用无模型", func(c *Config) { c.Channels[0].Models = nil }, "模型"},
		{"禁用可缺 Key", func(c *Config) {
			c.Channels[0].APIKey = ""
			c.Channels[0].Enabled = false
		}, ""},
	}
	for _, c := range cases {
		cfg := base()
		c.mut(&cfg)
		err := Validate(cfg)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: 不应报错, got %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: 期望含 %q, got %v", c.name, c.want, err)
		}
	}
}

func TestStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store := NewStore(path)
	if _, err := store.Load(); err != nil {
		t.Fatalf("首次 Load: %v", err)
	}
	cfg := store.Get()
	cfg.Channels = append(cfg.Channels, Channel{
		ID: "ch-zai", Name: "z.ai", Type: TypeAnthropicCompat,
		BaseURL: "https://api.z.ai/api/anthropic", APIKey: "sk-x",
		Models:       []string{"claude-sonnet-4-6", "glm"},
		ModelMapping: map[string]string{"claude-sonnet-4-6": "glm-5.3"},
		Priority:     10, Enabled: true,
	})
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	store2 := NewStore(path)
	loaded, err := store2.Load()
	if err != nil {
		t.Fatalf("重新 Load: %v", err)
	}
	if len(loaded.Providers) != 1 || loaded.Providers[0].ID != "ch-zai" {
		t.Fatalf("旧配置未迁移提供商: %+v", loaded.Providers)
	}
	if len(loaded.Channels) != 2 || loaded.Channels[0].Targets[0].UpstreamModel != "glm-5.3" {
		t.Fatalf("旧配置未迁移渠道: %+v", loaded.Channels)
	}
	if loaded.Channels[0].Targets[0].Priority != 10 {
		t.Fatalf("目标优先级丢失")
	}
}

func TestValidateModernProviderAndChannel(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderAccount{{ID: "pv-a", Name: "A", Type: TypeOpenAICompat, BaseURL: "https://example.com/v1", APIKey: "k", Enabled: true}}
	cfg.Channels = []Channel{{ID: "ch-public", Name: "Public", Model: "coding", Strategy: StrategyRoundRobin, Enabled: true, Targets: []ChannelTarget{{ProviderID: "pv-a", UpstreamModel: "model-a", Weight: 1, Enabled: true}}}}
	if err := Validate(cfg); err != nil { t.Fatal(err) }
	cfg.Channels[0].Targets[0].ProviderID = "missing"
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "不存在") { t.Fatalf("应拒绝悬空绑定: %v", err) }
}

// 不同渠道允许暴露相同的对外模型名。
func TestValidateDuplicateChannelModelAllowed(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderAccount{{ID: "pv-a", Name: "A", Type: TypeOpenAICompat, BaseURL: "https://example.com/v1", APIKey: "k", Enabled: true}}
	cfg.Channels = []Channel{
		{ID: "ch-a", Name: "A", Model: "kimi", Strategy: StrategyPriority, Enabled: true, Targets: []ChannelTarget{{ProviderID: "pv-a", Enabled: true}}},
		{ID: "ch-b", Name: "B", Model: "kimi", Strategy: StrategyPriority, Enabled: true, Targets: []ChannelTarget{{ProviderID: "pv-a", Enabled: true}}},
	}
	if err := Validate(cfg); err != nil {
		t.Fatalf("同名渠道应允许: %v", err)
	}
}

// 对外模型名留空 = 通配渠道，校验放行。
func TestValidateWildcardChannel(t *testing.T) {
	cfg := Default()
	cfg.Providers = []ProviderAccount{{ID: "pv-a", Name: "A", Type: TypeOpenAICompat, BaseURL: "https://example.com/v1", APIKey: "k", Enabled: true}}
	cfg.Channels = []Channel{{ID: "ch-any", Name: "Any", Model: "", Strategy: StrategyPriority, Enabled: true, Targets: []ChannelTarget{{ProviderID: "pv-a", Enabled: true}}}}
	if err := Validate(cfg); err != nil {
		t.Fatalf("通配渠道应允许: %v", err)
	}
}

func TestStoreCorruptRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewStore(path)
	if _, err := store.Load(); err == nil {
		t.Fatal("损坏配置应报错")
	}
	backup, err := store.BackupInvalidAndReset()
	if err != nil {
		t.Fatalf("BackupInvalidAndReset: %v", err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("重置后 Load 应成功: %v", err)
	}
}

func TestMaskKey(t *testing.T) {
	if MaskKey("short") != "••••" {
		t.Fatal("短密钥打码错误")
	}
	if MaskKey("sk-1234567890abcdef") != "sk-1••••cdef" {
		t.Fatalf("长密钥打码错误: %s", MaskKey("sk-1234567890abcdef"))
	}
	if MaskKey("") != "" {
		t.Fatal("空密钥")
	}
}
