package usage

import (
	"database/sql"
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

func TestProviderTodayAcrossChannels(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	for _, rec := range []Record{
		{CreatedAt: now, ChannelID: "ch-a", ChannelName: "A", ProviderID: "pv-one", InputTokens: 7, OutputTokens: 3},
		{CreatedAt: now, ChannelID: "ch-b", ChannelName: "B", ProviderID: "pv-one", InputTokens: 5},
		{CreatedAt: now, ChannelID: "ch-a", ChannelName: "A", ProviderID: "pv-two", InputTokens: 9},
	} {
		if err := s.Insert(rec); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.ProviderToday(now)
	if err != nil {
		t.Fatal(err)
	}
	if out["pv-one"] != 15 || out["pv-two"] != 9 {
		t.Fatalf("按提供商汇总错误: %+v", out)
	}
}

func TestProviderDailyAndByModel(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	yesterday := now.AddDate(0, 0, -1)
	for _, rec := range []Record{
		{CreatedAt: now, ProviderID: "pv-one", ChannelID: "ch-a", ChannelName: "A", ModelUpstream: "glm-5.3", InputTokens: 7, OutputTokens: 3},
		{CreatedAt: now, ProviderID: "pv-one", ChannelID: "ch-a", ChannelName: "A", ModelUpstream: "grok-4.6", InputTokens: 5},
		{CreatedAt: yesterday, ProviderID: "pv-one", ChannelID: "ch-a", ChannelName: "A", ModelUpstream: "glm-5.3", InputTokens: 2, CacheReadTokens: 4},
		{CreatedAt: now, ProviderID: "pv-two", ChannelID: "ch-b", ChannelName: "B", ModelUpstream: "glm-5.3", InputTokens: 100},
	} {
		if err := s.Insert(rec); err != nil {
			t.Fatal(err)
		}
	}
	daily, err := s.ProviderDaily("pv-one", now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 2 {
		t.Fatalf("逐日行数: %+v", daily)
	}
	byDay := map[string]int64{}
	for _, d := range daily {
		byDay[d.Day] = d.Tokens
	}
	todayKey := now.Format("2006-01-02")
	yesterdayKey := yesterday.Format("2006-01-02")
	if byDay[todayKey] != 15 || byDay[yesterdayKey] != 6 {
		t.Fatalf("逐日汇总错误: %+v", byDay)
	}
	byModel, err := s.ProviderDailyByModel("pv-one", now.AddDate(0, 0, -7))
	if err != nil {
		t.Fatal(err)
	}
	if len(byModel) != 3 {
		t.Fatalf("逐日模型行数: %+v", byModel)
	}
	totals := map[string]int64{}
	for _, r := range byModel {
		totals[r.Model] += r.Tokens
	}
	if totals["glm-5.3"] != 16 || totals["grok-4.6"] != 5 {
		t.Fatalf("按模型汇总错误: %+v", totals)
	}
	stats, err := s.ProviderModelStats("pv-one", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("模型统计行数: %+v", stats)
	}
	if stats[0].Model != "glm-5.3" || stats[0].TotalTokens != 16 || stats[0].Requests != 2 ||
		stats[0].InputTokens != 9 || stats[0].OutputTokens != 3 || stats[0].CacheRead != 4 {
		t.Fatalf("模型统计错误: %+v", stats[0])
	}
	if stats[1].Model != "grok-4.6" || stats[1].TotalTokens != 5 {
		t.Fatalf("模型统计排序错误: %+v", stats)
	}
}

func TestMigrateLegacyUsageProviderID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE request_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT, created_at INTEGER NOT NULL,
		channel_id TEXT NOT NULL, channel_name TEXT NOT NULL,
		model_requested TEXT NOT NULL, model_upstream TEXT NOT NULL, protocol TEXT NOT NULL,
		input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0, cache_write_tokens INTEGER NOT NULL DEFAULT 0,
		status INTEGER NOT NULL DEFAULT 0, latency_ms INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '');
		INSERT INTO request_log (created_at,channel_id,channel_name,model_requested,model_upstream,protocol,input_tokens)
		VALUES (strftime('%s','now'),'ch-old','Old','m','m','chat',12);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out, err := s.ProviderToday(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out["ch-old"] != 12 {
		t.Fatalf("历史记录未迁移: %+v", out)
	}
}

func TestRecorderAsync(t *testing.T) {
	s := newTestStore(t)
	r := NewRecorder(s, 16)
	r.Record(Record{CreatedAt: time.Now(), ChannelID: "ch-x", ChannelName: "X", ModelRequested: "m", ModelUpstream: "m"})
	// 轮询等异步落库（Close 会关库，先查后关）
	deadline := time.Now().Add(2 * time.Second)
	var rows []AggRow
	var err error
	for time.Now().Before(deadline) {
		rows, err = s.Summary(time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.Close()
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
