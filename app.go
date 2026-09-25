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
	configWarning string
	quitting      atomic.Bool
}

func NewApp() (*App, error) {
	directory, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户配置目录: %w", err)
	}
	store := config.NewStore(filepath.Join(directory, "OneProxy", "config.json"))
	configWarning := ""
	if _, err := store.Load(); err != nil {
		backup, recoverErr := store.BackupInvalidAndReset()
		if recoverErr != nil {
			return nil, fmt.Errorf("%w；恢复默认配置也失败: %w", err, recoverErr)
		}
		configWarning = fmt.Sprintf("原配置无效，已备份到 %s 并恢复默认设置", backup)
	}
	registry := provider.NewRegistry()
	bindExtraAccess(store)
	rebuildAdapters(store.Get(), registry)
	server := proxy.NewServer(store, registry)
	if dbPath, err := config.DefaultPath("usage.db"); err == nil {
		// 用量记账失败不阻塞启动（可无记账运行）
		_ = server.AttachUsage(dbPath, "api")
	}
	return &App{store: store, registry: registry, server: server, configWarning: configWarning}, nil
}

// rebuildAdapters 按当前配置重建渠道适配器（配置变更后调用）。
func rebuildAdapters(cfg config.Config, registry *provider.Registry) {
	for _, ch := range cfg.Channels {
		if !ch.Enabled {
			continue
		}
		switch ch.Type {
		case config.TypeAnthropicCompat:
			registry.Register(ch.ID, anthropiccompat.New(ch.BaseURL, ch.APIKey))
		case config.TypeOpenAICompat:
			registry.Register(ch.ID, openaicompat.New(ch.BaseURL, ch.APIKey))
		case config.TypeGrok:
			registerGrok(ch, registry)
		}
	}
}

// grokOAuthExtra 当前渠道的 OAuth extra 快照（供适配器读）。
func registerGrok(ch config.Channel, registry *provider.Registry) {
	var extra struct {
		Mode string `json:"mode"`
	}
	_ = json.Unmarshal(ch.Extra, &extra)
	if extra.Mode == "oauth" {
		adapter := grokadapter.NewOAuth(ch.BaseURL,
			func() []byte { return currentExtra(ch.ID) },
			func(data []byte) error { return saveExtra(ch.ID, data) },
			nil)
		registry.Register(ch.ID, adapter)
		return
	}
	registry.Register(ch.ID, grokadapter.New(ch.APIKey, ch.BaseURL))
}

// currentExtra/saveExtra 由 App 注入的配置访问器（启动时装配）。
var (
	currentExtra func(channelID string) []byte
	saveExtra    func(channelID string, data []byte) error
)

func bindExtraAccess(store *config.Store) {
	currentExtra = func(channelID string) []byte {
		ch, ok := store.Get().Channel(channelID)
		if !ok {
			return nil
		}
		return ch.Extra
	}
	saveExtra = func(channelID string, data []byte) error {
		return store.Update(func(c *config.Config) {
			for i := range c.Channels {
				if c.Channels[i].ID == channelID {
					c.Channels[i].Extra = data
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
		mShow := systray.AddMenuItem("打开主界面", "")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "")
		mShow.Click(func() {
			runtime.WindowShow(a.ctx)
			runtime.WindowUnminimise(a.ctx)
		})
		mQuit.Click(func() {
			a.quitting.Store(true)
			systray.Quit()
			runtime.Quit(a.ctx)
		})
	}, nil)
}

func (a *App) shutdown(ctx context.Context) { _ = a.server.Close() }

// Ping 前端连通性检查（M6 前的最小绑定）。
func (a *App) Ping() string { return "pong" }

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
	srv, err := proxy.NewDefaultServer()
	if err != nil {
		log.Fatal(err)
	}
	cfg, _ := config.DefaultPath("config.json")
	log.Printf("one-proxy 监听中（配置 %s）", cfg)
	log.Fatal(srv.ListenAndServe())
}
