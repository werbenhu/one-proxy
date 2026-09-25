package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	// LocalKeyLength 本地代理密钥默认长度。
	LocalKeyLength = 16

	TypeAnthropicCompat = "anthropic-compat"
	TypeOpenAICompat    = "openai-compat"
	TypeGrok            = "grok"
)

const localKeyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// Channel 是一条具体上游线路（One API Channel 规范）。
// ID 必须带 ch- 前缀（路由直连语法依赖该前缀区分 OpenRouter 的 org/model）。
type Channel struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	BaseURL      string            `json:"baseUrl"`
	APIKey       string            `json:"apiKey,omitempty"`
	Extra        []byte            `json:"extra,omitempty"`
	Models       []string          `json:"models"`
	ModelMapping map[string]string `json:"modelMapping,omitempty"`
	Priority     int               `json:"priority"`
	Enabled      bool              `json:"enabled"`
}

type Config struct {
	ListenHost string    `json:"listenHost"`
	ListenPort int       `json:"listenPort"`
	LocalKey   string    `json:"localKey"`
	RetainDays int       `json:"retainDays"`
	Channels   []Channel `json:"channels"`
}

func Default() Config {
	return Config{
		ListenHost: "127.0.0.1",
		ListenPort: 8280,
		LocalKey:   GenerateLocalKey(LocalKeyLength),
		RetainDays: 90,
		Channels:   []Channel{},
	}
}

func GenerateLocalKey(length int) string {
	if length <= 0 {
		length = LocalKeyLength
	}
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%016x", nanoFallback())[:length]
	}
	out := make([]byte, length)
	for i, b := range buf {
		out[i] = localKeyAlphabet[int(b)%len(localKeyAlphabet)]
	}
	return string(out)
}

func nanoFallback() int64 { return time.Now().UnixNano() }

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

func ValidType(t string) bool {
	switch t {
	case TypeAnthropicCompat, TypeOpenAICompat, TypeGrok:
		return true
	}
	return false
}

func Validate(c Config) error {
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
	if c.RetainDays < 1 {
		c.RetainDays = 90
	}
	seen := map[string]bool{}
	for i, ch := range c.Channels {
		if !strings.HasPrefix(ch.ID, "ch-") || len(ch.ID) <= len("ch-") {
			return fmt.Errorf("渠道 %d ID 必须以 ch- 开头且非空", i+1)
		}
		if seen[ch.ID] {
			return fmt.Errorf("渠道 ID %q 重复", ch.ID)
		}
		seen[ch.ID] = true
		if !ValidType(ch.Type) {
			return fmt.Errorf("渠道 %s 类型无效: %q", ch.ID, ch.Type)
		}
		if strings.TrimSpace(ch.BaseURL) == "" {
			return fmt.Errorf("渠道 %s 缺少 BaseURL", ch.ID)
		}
		if !ch.Enabled {
			continue
		}
		if ch.Type != TypeGrok && strings.TrimSpace(ch.APIKey) == "" {
			return fmt.Errorf("渠道 %s 已启用但缺少 API Key", ch.ID)
		}
		if len(ch.Models) == 0 {
			return fmt.Errorf("渠道 %s 未声明任何对外模型", ch.ID)
		}
	}
	return nil
}

// MaskKey 返回打码后的密钥（UI 展示用）。
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
