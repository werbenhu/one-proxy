package usage

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestInsertAndSummary(t *testing.T) {
	s := newTestStore(t)
	base := time.Now().Add(-time.Hour)
	records := []Record{
		{CreatedAt: base, ChannelID: "ch-a", ChannelName: "Kimi A", ModelRequested: "claude-sonnet-4-6",
			ModelUpstream: "kimi-k3", Protocol: "anthropic", InputTokens: 100, OutputTokens: 50, Status: 200},
		{CreatedAt: base.Add(time.Minute), ChannelID: "ch-a", ChannelName: "Kimi A", ModelRequested: "claude-sonnet-4-6",
			ModelUpstream: "kimi-k3", Protocol: "anthropic", InputTokens: 30, OutputTokens: 20, CacheReadTokens: 10, Status: 200},
		{CreatedAt: base.Add(2 * time.Minute), ChannelID: "ch-b", ChannelName: "Kimi B", ModelRequested: "claude-sonnet-4-6",
			ModelUpstream: "kimi-k3", Protocol: "chat", InputTokens: 10, OutputTokens: 5, Status: 429, Error: "quota"},
	}
	for _, r := range records {
		if err := s.Insert(r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.Summary(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("聚合行数: %d", len(rows))
	}
	a := rows[0]
	if a.ChannelID != "ch-a" || a.Requests != 2 || a.InputTokens != 130 || a.OutputTokens != 70 || a.CacheRead != 10 {
		t.Fatalf("ch-a 聚合: %+v", a)
	}
	if a.ModelRequested != "claude-sonnet-4-6" || a.ModelUpstream != "kimi-k3" {
		t.Fatalf("模型维度: %+v", a)
	}
	b := rows[1]
	if b.Errors != 1 {
		t.Fatalf("ch-b 错误数: %+v", b)
	}
}

func TestSummarySince(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().Add(-48 * time.Hour)
	recent := time.Now().Add(-time.Hour)
	_ = s.Insert(Record{CreatedAt: old, ChannelID: "ch-a", ChannelName: "A", ModelRequested: "m", ModelUpstream: "m", InputTokens: 999})
	_ = s.Insert(Record{CreatedAt: recent, ChannelID: "ch-a", ChannelName: "A", ModelRequested: "m", ModelUpstream: "m", InputTokens: 1})
	rows, err := s.Summary(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].InputTokens != 1 {
		t.Fatalf("since 过滤: %+v", rows)
	}
}

func TestSeriesDay(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = s.Insert(Record{CreatedAt: now.Add(-time.Duration(i) * time.Hour), ChannelID: "ch-a", ChannelName: "A",
			ModelRequested: "m", ModelUpstream: "m", InputTokens: 10, OutputTokens: 5})
	}
	rows, err := s.Series("day", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Requests != 3 || rows[0].InputTokens != 30 {
		t.Fatalf("day 序列: %+v", rows)
	}
	if _, err := s.Series("bogus", time.Time{}); err == nil {
		t.Fatal("非法 bucket 应报错")
	}
}

func TestChannelToday(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)
	if yesterday.Day() == now.Day() {
		t.Skip("跨日边界场景跳过")
	}
	_ = s.Insert(Record{CreatedAt: yesterday, ChannelID: "ch-a", ChannelName: "A", ModelRequested: "m", ModelUpstream: "m", InputTokens: 1000})
	_ = s.Insert(Record{CreatedAt: now.Add(-time.Minute), ChannelID: "ch-a", ChannelName: "A", ModelRequested: "m", ModelUpstream: "m", InputTokens: 7, OutputTokens: 3})
	out, err := s.ChannelToday(now)
	if err != nil {
		t.Fatal(err)
	}
	if out["ch-a"] != 10 {
		t.Fatalf("今日 token: %d", out["ch-a"])
	}
}

func TestRecorderAsync(t *testing.T) {
	s := newTestStore(t)
	r := NewRecorder(s, 16)
	r.Record(Record{CreatedAt: time.Now(), ChannelID: "ch-x", ChannelName: "X", ModelRequested: "m", ModelUpstream: "m"})
	r.Close()
	rows, err := s.Summary(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ChannelID != "ch-x" {
		t.Fatalf("异步写入: %+v", rows)
	}
}

func TestCleanup(t *testing.T) {
	s := newTestStore(t)
	_ = s.Insert(Record{CreatedAt: time.Now().AddDate(0, 0, -100), ChannelID: "ch-old", ChannelName: "old", ModelRequested: "m", ModelUpstream: "m"})
	_ = s.Insert(Record{CreatedAt: time.Now(), ChannelID: "ch-new", ChannelName: "new", ModelRequested: "m", ModelUpstream: "m"})
	n, err := s.Cleanup(90)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("清理行数: %d", n)
	}
	rows, _ := s.Summary(time.Time{})
	if len(rows) != 1 || rows[0].ChannelID != "ch-new" {
		t.Fatalf("清理后: %+v", rows)
	}
}
