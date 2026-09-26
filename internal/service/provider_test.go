package service

import (
	"testing"

	"github.com/werbenhu/one-proxy/internal/config"
)

func TestReorderProviders(t *testing.T) {
	store := config.NewStore(t.TempDir() + "/config.json")
	cfg := config.Default()
	cfg.LocalKey = "k"
	cfg.Providers = []config.ProviderAccount{
		{ID: "pv-a", Name: "A", Type: config.TypeOpenAICompat, BaseURL: "https://a", APIKey: "a", Enabled: true},
		{ID: "pv-b", Name: "B", Type: config.TypeOpenAICompat, BaseURL: "https://b", APIKey: "b", Enabled: true},
		{ID: "pv-c", Name: "C", Type: config.TypeOpenAICompat, BaseURL: "https://c", APIKey: "c", Enabled: true},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	s := New(store, nil, nil)

	// 只给部分 ID：给出的按顺序前置，未给出的保持相对顺序排尾
	if err := s.ReorderProviders([]string{"pv-c", "pv-a"}); err != nil {
		t.Fatal(err)
	}
	got := store.Get().Providers
	if got[0].ID != "pv-c" || got[1].ID != "pv-a" || got[2].ID != "pv-b" {
		t.Fatalf("排序结果: %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
}
