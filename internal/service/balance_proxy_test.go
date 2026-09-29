package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/werbenhu/one-proxy/internal/config"
)

// TestProviderBalanceUsesConfiguredProxy 验证「查询额度」走提供商生效的代理：
// 勾选 UseProxy 时余额请求必须经过全局代理发出（回归：开了代理的模型，额度也用代理查）。
func TestProviderBalanceUsesConfiguredProxy(t *testing.T) {
	// 假代理：http 直连代理场景下客户端会带绝对 URL 请求代理，记录后应答余额
	var proxiedURL string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedURL = r.URL.String()
		if r.URL.Path == "/user/balance" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":"12.50"}]}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxySrv.Close()

	store := config.NewStore(t.TempDir() + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.GlobalProxy = proxySrv.URL
	cfg.Providers = []config.ProviderAccount{{
		ID: "pv-ds", Name: "DeepSeek", Type: config.TypeOpenAICompat,
		BaseURL:    "https://api.deepseek.com",
		BalanceURL: "http://balance.upstream.example/user/balance", APIKey: "sk-test",
		BalanceKind: "deepseek", UseProxy: true, Enabled: true,
	}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil, nil)

	view, err := s.ProviderBalance("pv-ds")
	if err != nil {
		t.Fatal(err)
	}
	if !view.Supported {
		t.Fatalf("应识别余额接口: %+v", view)
	}
	if proxiedURL != "http://balance.upstream.example/user/balance" {
		t.Fatalf("余额请求应经代理访问上游，实际经代理的请求: %q", proxiedURL)
	}
}

// TestProviderBalanceUsesMalformedProxy 复现真实用户配置：globalProxy 写成
// "http:127.0.0.1:7897"（缺 //）。旧实现 url.Parse 得到空 Host 后静默回退直连，
// 导致勾了代理的提供商（如 Grok）额度查询直连失败；规范化后应仍走代理。
func TestProviderBalanceUsesMalformedProxy(t *testing.T) {
	var proxiedURL string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxiedURL = r.URL.String()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"balance_infos":[{"currency":"CNY","total_balance":"12.50"}]}`))
	}))
	defer proxySrv.Close()

	// 把代理地址写成缺 // 的形式（模拟手改配置/输入笔误）
	malformed := "http:" + strings.TrimPrefix(proxySrv.URL, "http://")

	store := config.NewStore(t.TempDir() + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.GlobalProxy = malformed
	cfg.Providers = []config.ProviderAccount{{
		ID: "pv-ds", Name: "DeepSeek", Type: config.TypeOpenAICompat,
		BaseURL:    "https://api.deepseek.com",
		BalanceURL: "http://balance.upstream.example/user/balance", APIKey: "sk-test",
		BalanceKind: "deepseek", UseProxy: true, Enabled: true,
	}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil, nil)

	if _, err := s.ProviderBalance("pv-ds"); err != nil {
		t.Fatal(err)
	}
	if proxiedURL != "http://balance.upstream.example/user/balance" {
		t.Fatalf("畸形代理地址应规范化后仍走代理，实际经代理的请求: %q", proxiedURL)
	}
}

// TestSaveSettingsNormalizesProxy 保存设置时应把可修复的代理拼写偏差规范入库。
func TestSaveSettingsNormalizesProxy(t *testing.T) {
	store := config.NewStore(t.TempDir() + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil, nil)

	view := SettingsView{ListenHost: "127.0.0.1", ListenPort: 8280, LocalKey: "k", RetainDays: 90, Theme: "light", Language: "zh",
		GlobalProxy: "http:127.0.0.1:7897"}
	if err := s.SaveSettings(view); err != nil {
		t.Fatal(err)
	}
	if got := store.Get().GlobalProxy; got != "http://127.0.0.1:7897" {
		t.Fatalf("保存后 GlobalProxy = %q, want http://127.0.0.1:7897", got)
	}

	view.GlobalProxy = "this is not a url"
	if err := s.SaveSettings(view); err == nil {
		t.Fatal("无法识别的代理格式应在保存时报错")
	}
}

// TestProviderBalanceIgnoresProxyWhenDisabled 未勾选「使用代理」时不强制走全局代理：
// 全局代理指向不可达地址，若误用则查询会失败；此处应直达端点成功返回。
func TestProviderBalanceIgnoresProxyWhenDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"total_credits":20,"total_usage":3.25}}`))
	}))
	defer srv.Close()

	store := config.NewStore(t.TempDir() + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.GlobalProxy = "http://127.0.0.1:1" // 不可达代理：若误用会查询失败
	cfg.Providers = []config.ProviderAccount{{
		ID: "pv-or", Name: "OpenRouter", Type: config.TypeOpenAICompat,
		BaseURL: "https://openrouter.ai/api/v1", APIKey: "sk-test",
		BalanceURL: srv.URL, BalanceKind: "openrouter", UseProxy: false, Enabled: true,
	}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil, nil)

	view, err := s.ProviderBalance("pv-or")
	if err != nil {
		t.Fatalf("未勾选代理时查询应直达端点: %v", err)
	}
	if view.Summary != "$16.7500" {
		t.Fatalf("unexpected summary: %+v", view)
	}
}
