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
		{"渠道ID无前缀", func(c *Config) { c.Channels[0].ID = "kimi1" }, "ch-"},
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
	if len(loaded.Channels) != 1 || loaded.Channels[0].ModelMapping["claude-sonnet-4-6"] != "glm-5.3" {
		t.Fatalf("roundtrip 数据丢失: %+v", loaded.Channels)
	}
	if loaded.Channels[0].Priority != 10 {
		t.Fatalf("Priority 丢失")
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
