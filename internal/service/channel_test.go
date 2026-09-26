package service

import (
	"testing"

	"github.com/werbenhu/one-proxy/internal/config"
)

func newChannelService(t *testing.T) *Service {
	t.Helper()
	store := config.NewStore(t.TempDir() + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true}}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return New(store, nil, nil)
}

func channel() config.Channel {
	return config.Channel{ID: "ch-old", Name: "A", Model: "kimi", Strategy: config.StrategyPriority, Enabled: true, Targets: []config.ChannelTarget{{ProviderID: "pv-a", Enabled: true}}}
}

func TestSaveChannelRename(t *testing.T) {
	s := newChannelService(t)
	if err := s.SaveChannel(channel(), ""); err != nil {
		t.Fatal(err)
	}
	renamed := channel()
	renamed.ID = "ch-new"
	if err := s.SaveChannel(renamed, "ch-old"); err != nil {
		t.Fatal(err)
	}
	channels := s.store.Get().Channels
	if len(channels) != 1 || channels[0].ID != "ch-new" {
		t.Fatalf("改名后应只剩 ch-new: %+v", channels)
	}
}

func TestSaveChannelRenameCollision(t *testing.T) {
	s := newChannelService(t)
	if err := s.SaveChannel(channel(), ""); err != nil {
		t.Fatal(err)
	}
	other := channel()
	other.ID = "ch-other"
	if err := s.SaveChannel(other, ""); err != nil {
		t.Fatal(err)
	}
	dup := channel()
	dup.ID = "ch-other"
	if err := s.SaveChannel(dup, "ch-old"); err == nil {
		t.Fatal("改名撞上已有渠道 ID 应报错")
	}
	channels := s.store.Get().Channels
	if len(channels) != 2 || channels[0].ID != "ch-old" || channels[1].ID != "ch-other" {
		t.Fatalf("校验失败不应改动配置: %+v", channels)
	}
}

func TestSaveChannelKeepsCustomID(t *testing.T) {
	s := newChannelService(t)
	ch := channel()
	ch.ID = "kimi-a"
	if err := s.SaveChannel(ch, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.store.Get().Channels[0].ID; got != "kimi-a" {
		t.Fatalf("自定义 ID 应原样保留: %s", got)
	}
}
