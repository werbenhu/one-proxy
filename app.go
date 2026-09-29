package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/werbenhu/one-proxy/internal/adapters/anthropiccompat"
	grokadapter "github.com/werbenhu/one-proxy/internal/adapters/grok"
	openaicompat "github.com/werbenhu/one-proxy/internal/adapters/openaicompat"
	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/proxy"
	"github.com/werbenhu/one-proxy/internal/service"
	"github.com/werbenhu/one-proxy/internal/usage"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed tray.ico
var trayIcon []byte

// App Wails 应用生命周期与绑定方法。
type App struct {
	ctx           context.Context
	store         *config.Store
	registry      *provider.Registry
	server        *proxy.Server
	svc           *service.Service
	configWarning string
	usageWarning  string
	quitting      atomic.Bool
}

// NewApp 构建 Wails 应用。configWarning / usageWarning 会在启动时以界面警告形式呈现。
func NewApp() (*App, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("get user config dir: %w", err)
	}
	store := config.NewStore(filepath.Join(directory, "OneProxy", "config.json"))
	configWarning := ""
	if _, err := store.Load(); err != nil {
		backup, recoverErr := store.BackupInvalidAndReset()
		if recoverErr != nil {
			return nil, fmt.Errorf("%w; restoring default config also failed: %w", err, recoverErr)
		}
		configWarning = fmt.Sprintf("原配置无效，已备份到 %s 并恢复默认设置", backup)
	}
	registry := provider.NewRegistry()
	bindExtraAccess(store)
	rebuildAdapters(store.Get(), registry)
	server := proxy.NewServer(store, registry)
	usageWarning := ""
	if dbPath, err := config.DefaultPath("usage.db"); err != nil {
		usageWarning = fmt.Sprintf("用量记账不可用：%v", err)
	} else if err := server.AttachUsage(dbPath, "api"); err != nil {
		// 不阻塞启动，但必须让用户知道（否则用量统计静默全为 0）。
		usageWarning = fmt.Sprintf("用量记账启动失败（用量统计将为 0）：%v", err)
	}
	svc := service.New(store, registry, server)
	return &App{store: store, registry: registry, server: server, svc: svc,
		configWarning: configWarning, usageWarning: usageWarning}, nil
}

// rebuildAdapters 按当前配置重建渠道适配器（配置变更后调用）。
func rebuildAdapters(cfg config.Config, registry *provider.Registry) {
	registry.Reset()
	for _, p := range cfg.Providers {
		if !p.Enabled {
			continue
		}
		proxyURL := p.EffectiveProxyURL(cfg.GlobalProxy)
		responseHeaderTimeout := p.EffectiveResponseHeaderTimeout()
		switch p.Type {
		case config.TypeAnthropicCompat:
			registry.Register(p.ID, anthropiccompat.New(p.BaseURL, p.APIKey, proxyURL, responseHeaderTimeout))
		case config.TypeOpenAICompat:
			registry.Register(p.ID, openaicompat.New(p.BaseURL, p.APIKey, proxyURL, responseHeaderTimeout))
		case config.TypeGrok:
			registerGrok(p, cfg.GlobalProxy, registry)
		}
	}
}

// grokOAuthExtra 当前渠道的 OAuth extra 快照（供适配器读）。
func registerGrok(ch config.ProviderAccount, globalProxy string, registry *provider.Registry) {
	proxyURL := ch.EffectiveProxyURL(globalProxy)
	responseHeaderTimeout := ch.EffectiveResponseHeaderTimeout()
	var extra struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(ch.Extra, &extra)
	if extra.Mode == "oauth" {
		adapter := grokadapter.NewOAuth(ch.BaseURL, proxyURL,
			func() []byte { return currentExtra(ch.ID) },
			func(data []byte) error { return saveExtra(ch.ID, data) },
			nil, responseHeaderTimeout)
		registry.Register(ch.ID, adapter)
		return
	}
	registry.Register(ch.ID, grokadapter.New(ch.APIKey, ch.BaseURL, proxyURL, responseHeaderTimeout))
}

// currentExtra/saveExtra 由 App 注入的配置访问器（启动时装配）。
var (
	currentExtra func(channelID string) []byte
	saveExtra    func(channelID string, data []byte) error
)

func bindExtraAccess(store *config.Store) {
	currentExtra = func(channelID string) []byte {
		ch, ok := store.Get().Provider(channelID)
		if !ok {
			return nil
		}
		return ch.Extra
	}
	saveExtra = func(channelID string, data []byte) error {
		return store.Update(func(c *config.Config) {
			for i := range c.Providers {
				if c.Providers[i].ID == channelID {
					c.Providers[i].Extra = data
					return
				}
			}
		})
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.configWarning != "" {
		runtime.LogWarning(ctx, a.configWarning)
	}
	if a.usageWarning != "" {
		runtime.LogWarning(ctx, a.usageWarning)
	}
	a.initSystray()
	go func() {
		if err := a.server.ListenAndServe(); err != nil {
			runtime.LogFatal(a.ctx, fmt.Sprintf("代理启动失败: %v", err))
		}
	}()
}

func (a *App) beforeClose(ctx context.Context) bool {
	if a.quitting.Load() {
		return false
	}
	runtime.WindowHide(ctx)
	return true
}

func (a *App) initSystray() {
	go systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("OneProxy")
		systray.SetOnClick(func(systray.IMenu) { a.showMainWindow() })
		mShow := systray.AddMenuItem("打开主界面", "")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "")
		mShow.Click(func() { a.showMainWindow() })
		mQuit.Click(func() {
			a.quitting.Store(true)
			systray.Quit()
			runtime.Quit(a.ctx)
		})
	}, nil)
}

func (a *App) showMainWindow() {
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

func (a *App) shutdown(ctx context.Context) { _ = a.server.Close() }

// ===== Wails bindings（前端调用）=====

// Ping 心跳探活（前端 WebView2 冻结自愈用）：能返回即代表绑定通道正常。
func (a *App) Ping() {}

func (a *App) GetProviders() []service.ProviderView { return a.svc.Providers() }

func (a *App) GetProviderKeys(id string) (service.ProviderKeysView, error) {
	return a.svc.ProviderKeys(id)
}

var jsonFileFilter = []runtime.FileFilter{{DisplayName: "JSON 文件", Pattern: "*.json"}}

// ExportProviders 弹出保存对话框，把全部提供商（含凭证）写成 JSON；取消返回空串。
func (a *App) ExportProviders() (string, error) {
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "导出提供商",
		DefaultFilename: "oneproxy-providers.json",
		Filters:         jsonFileFilter,
	})
	if err != nil || path == "" {
		return "", err
	}
	data, err := a.svc.ExportProviders()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// ImportProviders 弹出打开对话框，合并导入提供商；取消返回空串。
func (a *App) ImportProviders() (string, error) {
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "导入提供商", Filters: jsonFileFilter})
	if err != nil || path == "" {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	added, skipped, err := a.svc.ImportProviders(data)
	if err != nil {
		return "", err
	}
	rebuildAdapters(a.store.Get(), a.registry)
	return fmt.Sprintf("导入完成：新增 %d 个，跳过 %d 个（ID 重复或无效）", added, skipped), nil
}

func (a *App) SaveProvider(p config.ProviderAccount) error {
	if err := a.svc.SaveProvider(p); err != nil {
		return err
	}
	rebuildAdapters(a.store.Get(), a.registry)
	return nil
}

func (a *App) DeleteProvider(id string) error {
	if err := a.svc.DeleteProvider(id); err != nil {
		return err
	}
	rebuildAdapters(a.store.Get(), a.registry)
	return nil
}

// ReorderProviders 按前端拖拽结果重排提供商（顺序不影响适配器，无需重建）。
func (a *App) ReorderProviders(ids []string) error { return a.svc.ReorderProviders(ids) }

func (a *App) GetProviderBalance(id string) (service.BalanceView, error) {
	return a.svc.ProviderBalance(id)
}

func (a *App) GetProviderModels(id string) ([]provider.ModelInfo, error) {
	return a.svc.ProviderModels(id)
}

func (a *App) GetChannels() []service.ChannelView { return a.svc.Channels() }

func (a *App) SaveChannel(ch config.Channel, originalID string) error {
	return a.svc.SaveChannel(ch, originalID)
}

func (a *App) DeleteChannel(id string) error { return a.svc.DeleteChannel(id) }

func (a *App) GetPresets() []service.PresetView { return service.Presets() }

func (a *App) GetSettings() service.SettingsView { return a.svc.Settings() }

func (a *App) SaveSettings(v service.SettingsView) error {
	if err := a.svc.SaveSettings(v); err != nil {
		return err
	}
	rebuildAdapters(a.store.Get(), a.registry)
	return nil
}

func (a *App) GetUsageSummary(rangeKey string) ([]usage.AggRow, error) {
	return a.svc.UsageSummary(rangeKey)
}

func (a *App) GetUsageDaily(rangeKey string) ([]usage.ModelDayTokens, error) {
	return a.svc.UsageDaily(rangeKey)
}

func (a *App) GetUsageHourlyToday() ([]usage.ModelHourTokens, error) {
	return a.svc.UsageHourlyToday()
}

func (a *App) GetProviderUsage(id string) service.ProviderUsageView {
	return a.svc.ProviderUsage(id)
}

func (a *App) TestChannel(id string) error { return a.svc.TestChannel(id) }

func (a *App) TestProvider(id string) error { return a.svc.TestProvider(id) }

func (a *App) StartGrokDeviceAuth(id string) (provider.DeviceAuthInfo, error) {
	return a.svc.StartGrokDeviceAuth(id)
}

func (a *App) CompleteGrokDeviceAuth(id, deviceCode string) error {
	return a.svc.CompleteGrokDeviceAuth(id, deviceCode)
}

func wailsRun() error {
	app, err := NewApp()
	if err != nil {
		return err
	}
	return wails.Run(&options.App{
		Title:             "OneProxy",
		Width:             1024,
		Height:            680,
		AssetServer:       &assetserver.Options{Assets: assets},
		BackgroundColour:  &options.RGBA{R: 11, G: 15, B: 15, A: 1},
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		OnBeforeClose:     app.beforeClose,
		HideWindowOnClose: true,
		Bind:              []any{app},
	})
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "cli" {
		// 纯 CLI 模式（调试用，不启 GUI）
		runCLI()
		return
	}
	if err := wailsRun(); err != nil {
		log.Fatal(err)
	}
}

func runCLI() {
	path, err := config.DefaultPath("config.json")
	if err != nil {
		log.Fatal(err)
	}
	store := config.NewStore(path)
	if _, err := store.Load(); err != nil {
		log.Fatalf("配置无效（%s）: %v", path, err)
	}
	registry := provider.NewRegistry()
	bindExtraAccess(store)
	rebuildAdapters(store.Get(), registry)
	srv := proxy.NewServer(store, registry)
	if dbPath, err := config.DefaultPath("usage.db"); err != nil {
		log.Printf("用量记账不可用: %v", err)
	} else if err := srv.AttachUsage(dbPath, "api"); err != nil {
		log.Printf("用量记账启动失败（用量统计将为 0）: %v", err)
	}
	log.Printf("one-proxy 监听中（配置 %s）", path)
	log.Fatal(srv.ListenAndServe())
}
