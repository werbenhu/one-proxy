package main

import (
	"log"

	"github.com/werbenhu/one-proxy/internal/adapters/anthropiccompat"
	"github.com/werbenhu/one-proxy/internal/config"
	"github.com/werbenhu/one-proxy/internal/provider"
	"github.com/werbenhu/one-proxy/internal/proxy"
)

// main M1 CLI 骨架：读取配置、注册渠道适配器并启动本地代理。
// Wails 桌面壳在 M2 接入（app.go）。
func main() {
	path, err := config.DefaultPath("config.json")
	if err != nil {
		log.Fatal(err)
	}
	store := config.NewStore(path)
	cfg, err := store.Load()
	if err != nil {
		log.Fatalf("配置无效（%s）: %v", path, err)
	}
	registry := provider.NewRegistry()
	for _, ch := range cfg.Channels {
		if !ch.Enabled || ch.Type != config.TypeAnthropicCompat {
			continue
		}
		registry.Register(ch.ID, anthropiccompat.New(ch.BaseURL, ch.APIKey))
	}
	srv := proxy.NewServer(store, registry)
	log.Printf("one-proxy 监听 %s（渠道 %d 个）", cfg.Address(), len(cfg.Channels))
	log.Fatal(srv.ListenAndServe())
}
