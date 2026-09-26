// Package usage 请求用量记账：SQLite 单表 request_log，查询时 GROUP BY 聚合。
package usage

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Record 一次请求的用量记录。
type Record struct {
	CreatedAt        time.Time
	ChannelID        string
	ChannelName      string
	ProviderID       string
	ModelRequested   string // 对外模型名
	ModelUpstream    string // 映射后上游模型名
	Protocol         string // anthropic | chat | responses
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	Status           int // HTTP 状态（0 = 未完成/流中断）
	LatencyMs        int64
	Error            string
}

// Store SQLite 记账存储。
type Store struct {
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("打开用量数据库: %w", err)
	}
	db.SetMaxOpenConns(1) // modernc sqlite 单写连接避免锁竞争
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭底层连接（幂等）。
func (s *Store) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.db.Close() })
	return s.closeErr
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS request_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at INTEGER NOT NULL,
		channel_id TEXT NOT NULL,
		channel_name TEXT NOT NULL,
		provider_id TEXT NOT NULL DEFAULT '',
		model_requested TEXT NOT NULL,
		model_upstream TEXT NOT NULL,
		protocol TEXT NOT NULL,
		input_tokens INTEGER NOT NULL DEFAULT 0,
		output_tokens INTEGER NOT NULL DEFAULT 0,
		cache_read_tokens INTEGER NOT NULL DEFAULT 0,
		cache_write_tokens INTEGER NOT NULL DEFAULT 0,
		status INTEGER NOT NULL DEFAULT 0,
		latency_ms INTEGER NOT NULL DEFAULT 0,
		error TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_request_log_created ON request_log(created_at);
	CREATE INDEX IF NOT EXISTS idx_request_log_channel ON request_log(channel_id, created_at);`)
	if err != nil {
		return fmt.Errorf("初始化用量表: %w", err)
	}
	var hasProviderID int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('request_log') WHERE name = 'provider_id'`).Scan(&hasProviderID); err != nil {
		return fmt.Errorf("检查用量表版本: %w", err)
	}
	if hasProviderID == 0 {
		if _, err := s.db.Exec(`ALTER TABLE request_log ADD COLUMN provider_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("升级用量表: %w", err)
		}
		// 旧版每个渠道即一个上游账户；迁移后账户沿用原渠道 ID。
		if _, err := s.db.Exec(`UPDATE request_log SET provider_id = channel_id WHERE provider_id = ''`); err != nil {
			return fmt.Errorf("迁移历史用量: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_request_log_provider ON request_log(provider_id, created_at)`); err != nil {
		return fmt.Errorf("创建提供商用量索引: %w", err)
	}
	return nil
}

// Insert 异步埋点写入（由 proxy 的 recorder goroutine 调用）。
func (s *Store) Insert(r Record) error {
	_, err := s.db.Exec(`INSERT INTO request_log
		(created_at, channel_id, channel_name, provider_id, model_requested, model_upstream, protocol,
		 input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, status, latency_ms, error)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.CreatedAt.Unix(), r.ChannelID, r.ChannelName, r.ProviderID, r.ModelRequested, r.ModelUpstream, r.Protocol,
		r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.Status, r.LatencyMs, r.Error)
	if err != nil {
		return fmt.Errorf("写入用量记录: %w", err)
	}
	return nil
}

// Recorder 异步记账器：channel 缓冲写入，避免阻塞请求路径。
type Recorder struct {
	store *Store
	ch    chan Record
	done  chan struct{}
}

func NewRecorder(store *Store, buffer int) *Recorder {
	if buffer <= 0 {
		buffer = 256
	}
	r := &Recorder{store: store, ch: make(chan Record, buffer), done: make(chan struct{})}
	go r.loop()
	return r
}

func (r *Recorder) Record(rec Record) {
	select {
	case r.ch <- rec:
	default:
		// 缓冲满时丢弃（个人工具：记账不阻塞、不崩主流程）
	}
}

func (r *Recorder) loop() {
	defer close(r.done)
	for rec := range r.ch {
		_ = r.store.Insert(rec)
	}
}

// Close 停止接收并排空缓冲；closeDB 为 true 时同时关闭底层连接
// （Windows 上句柄不关闭会锁住 db 文件，测试清理必需）。
func (r *Recorder) Close() {
	close(r.ch)
	<-r.done
	_ = r.store.db.Close()
}

// Store 暴露底层存储（查询用）。
func (r *Recorder) Store() *Store { return r.store }

// AggRow 聚合行。
type AggRow struct {
	ChannelID      string `json:"channelId"`
	ChannelName    string `json:"channelName"`
	ModelRequested string `json:"modelRequested"`
	ModelUpstream  string `json:"modelUpstream"`
	Requests       int64  `json:"requests"`
	InputTokens    int64  `json:"inputTokens"`
	OutputTokens   int64  `json:"outputTokens"`
	CacheRead      int64  `json:"cacheReadTokens"`
	CacheWrite     int64  `json:"cacheWriteTokens"`
	Errors         int64  `json:"errors"`
}

// Summary 按渠道×模型聚合（since 为 0 表示全部）。
func (s *Store) Summary(since time.Time) ([]AggRow, error) {
	where := ""
	args := []any{}
	if !since.IsZero() {
		where = "WHERE created_at >= ?"
		args = append(args, since.Unix())
	}
	rows, err := s.db.Query(`SELECT channel_id, channel_name, model_requested, model_upstream,
		COUNT(*), SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(cache_write_tokens),
		SUM(CASE WHEN status >= 400 OR error != '' THEN 1 ELSE 0 END)
		FROM request_log `+where+` GROUP BY channel_id, model_requested, model_upstream
		ORDER BY SUM(input_tokens)+SUM(output_tokens) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("聚合查询: %w", err)
	}
	defer rows.Close()
	var out []AggRow
	for rows.Next() {
		var r AggRow
		if err := rows.Scan(&r.ChannelID, &r.ChannelName, &r.ModelRequested, &r.ModelUpstream,
			&r.Requests, &r.InputTokens, &r.OutputTokens, &r.CacheRead, &r.CacheWrite, &r.Errors); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SeriesRow 时间序列行（bucket: hour|day|week|month）。
type SeriesRow struct {
	Bucket       string `json:"bucket"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	Requests     int64  `json:"requests"`
}

// Series 按时间桶聚合。
func (s *Store) Series(bucket string, since time.Time) ([]SeriesRow, error) {
	fmtStr := map[string]string{
		"hour": "%Y-%m-%dT%H:00", "day": "%Y-%m-%d", "week": "%Y-W%W", "month": "%Y-%m",
	}[bucket]
	if fmtStr == "" {
		return nil, fmt.Errorf("无效时间桶 %q", bucket)
	}
	where := ""
	args := []any{}
	if !since.IsZero() {
		where = "WHERE created_at >= ?"
		args = append(args, since.Unix())
	}
	rows, err := s.db.Query(`SELECT strftime('`+fmtStr+`', created_at, 'unixepoch'),
		SUM(input_tokens), SUM(output_tokens), COUNT(*)
		FROM request_log `+where+` GROUP BY 1 ORDER BY 1`, args...)
	if err != nil {
		return nil, fmt.Errorf("序列查询: %w", err)
	}
	defer rows.Close()
	var out []SeriesRow
	for rows.Next() {
		var r SeriesRow
		if err := rows.Scan(&r.Bucket, &r.InputTokens, &r.OutputTokens, &r.Requests); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ChannelToday 渠道今日 token 汇总（渠道列表徽标用，本地时区自然日）。
func (s *Store) ChannelToday(now time.Time) (map[string]int64, error) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	rows, err := s.db.Query(`SELECT channel_id, SUM(input_tokens)+SUM(output_tokens)+SUM(cache_read_tokens)
		FROM request_log WHERE created_at >= ? GROUP BY channel_id`, todayStart.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id string
		var tokens int64
		if err := rows.Scan(&id, &tokens); err != nil {
			return nil, err
		}
		out[id] = tokens
	}
	return out, rows.Err()
}

// ProviderToday 按真实上游账户汇总今日 Token。
func (s *Store) ProviderToday(now time.Time) (map[string]int64, error) {
	today, _, _, err := s.ProviderTokens(now)
	return today, err
}

// totalTokens 单次请求的计费 token 口径（与列表今日用量一致）。
const totalTokens = "COALESCE(input_tokens,0)+COALESCE(output_tokens,0)+COALESCE(cache_read_tokens,0)"

// ProviderTokens 按真实上游账户汇总今日 / 本周（周一起）/ 本月的 Token。
func (s *Store) ProviderTokens(now time.Time) (today, week, month map[string]int64, err error) {
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekStart := todayStart.AddDate(0, 0, -(int(now.Weekday())+6)%7)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	rows, err := s.db.Query(`SELECT provider_id,
		SUM(CASE WHEN created_at >= ? THEN `+totalTokens+` ELSE 0 END),
		SUM(CASE WHEN created_at >= ? THEN `+totalTokens+` ELSE 0 END),
		SUM(`+totalTokens+`)
		FROM request_log WHERE created_at >= ? AND provider_id != '' GROUP BY provider_id`,
		todayStart.Unix(), weekStart.Unix(), monthStart.Unix())
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	today, week, month = map[string]int64{}, map[string]int64{}, map[string]int64{}
	for rows.Next() {
		var id string
		var daySum, weekSum, monthSum int64
		if err := rows.Scan(&id, &daySum, &weekSum, &monthSum); err != nil {
			return nil, nil, nil, err
		}
		today[id], week[id], month[id] = daySum, weekSum, monthSum
	}
	return today, week, month, rows.Err()
}

// DayTokens 单个自然日（本地时区）的 token 量。
type DayTokens struct {
	Day    string `json:"day"`
	Tokens int64  `json:"tokens"`
}

// ModelDayTokens 单个自然日 × 上游模型的 token 量。
type ModelDayTokens struct {
	Day    string `json:"day"`
	Model  string `json:"model"`
	Tokens int64  `json:"tokens"`
}

// ModelHourTokens 单个自然小时 × 上游模型的 token 量（今日分布图用）。
// Bucket 形如 "2026-09-26T14"（本地时区，跨零点回看也能区分日期）。
type ModelHourTokens struct {
	Bucket string `json:"bucket"`
	Model  string `json:"model"`
	Tokens int64  `json:"tokens"`
}

// ProviderDaily 某提供商 since 以来的逐日 token（详情弹窗热力图用）。
func (s *Store) ProviderDaily(providerID string, since time.Time) ([]DayTokens, error) {
	rows, err := s.db.Query(`SELECT strftime('%Y-%m-%d', created_at, 'unixepoch', 'localtime'), SUM(`+totalTokens+`)
		FROM request_log WHERE provider_id = ? AND created_at >= ? GROUP BY 1 ORDER BY 1`, providerID, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("逐日用量查询: %w", err)
	}
	defer rows.Close()
	out := []DayTokens{}
	for rows.Next() {
		var r DayTokens
		if err := rows.Scan(&r.Day, &r.Tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ProviderDailyByModel 某提供商 since 以来的逐日 × 上游模型 token（趋势图用）。
func (s *Store) ProviderDailyByModel(providerID string, since time.Time) ([]ModelDayTokens, error) {
	rows, err := s.db.Query(`SELECT strftime('%Y-%m-%d', created_at, 'unixepoch', 'localtime'), model_upstream, SUM(`+totalTokens+`)
		FROM request_log WHERE provider_id = ? AND created_at >= ? GROUP BY 1, 2 ORDER BY 1`, providerID, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("逐日模型用量查询: %w", err)
	}
	defer rows.Close()
	out := []ModelDayTokens{}
	for rows.Next() {
		var r ModelDayTokens
		if err := rows.Scan(&r.Day, &r.Model, &r.Tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DailyByModel 全部提供商 since 以来的逐日 × 上游模型 token（用量页趋势图用）。
func (s *Store) DailyByModel(since time.Time) ([]ModelDayTokens, error) {
	rows, err := s.db.Query(`SELECT strftime('%Y-%m-%d', created_at, 'unixepoch', 'localtime'), model_upstream, SUM(`+totalTokens+`)
		FROM request_log WHERE created_at >= ? GROUP BY 1, 2 ORDER BY 1`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("逐日模型用量查询: %w", err)
	}
	defer rows.Close()
	out := []ModelDayTokens{}
	for rows.Next() {
		var r ModelDayTokens
		if err := rows.Scan(&r.Day, &r.Model, &r.Tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HourlyByModel 全部提供商 since 以来的逐小时 × 上游模型 token（今日分布图用）。
func (s *Store) HourlyByModel(since time.Time) ([]ModelHourTokens, error) {
	rows, err := s.db.Query(`SELECT strftime('%Y-%m-%dT%H', created_at, 'unixepoch', 'localtime'), model_upstream, SUM(`+totalTokens+`)
		FROM request_log WHERE created_at >= ? GROUP BY 1, 2 ORDER BY 1`, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("逐小时模型用量查询: %w", err)
	}
	defer rows.Close()
	out := []ModelHourTokens{}
	for rows.Next() {
		var r ModelHourTokens
		if err := rows.Scan(&r.Bucket, &r.Model, &r.Tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ModelStat 单上游模型的累计消耗。
type ModelStat struct {
	Model        string `json:"model"`
	Requests     int64  `json:"requests"`
	InputTokens  int64  `json:"inputTokens"`
	OutputTokens int64  `json:"outputTokens"`
	CacheRead    int64  `json:"cacheReadTokens"`
	TotalTokens  int64  `json:"totalTokens"`
}

// ProviderModelStats 某提供商按上游模型聚合的累计消耗（since 为零值表示全部），按总量降序。
func (s *Store) ProviderModelStats(providerID string, since time.Time) ([]ModelStat, error) {
	where := "provider_id = ?"
	args := []any{providerID}
	if !since.IsZero() {
		where += " AND created_at >= ?"
		args = append(args, since.Unix())
	}
	rows, err := s.db.Query(`SELECT model_upstream, COUNT(*),
		SUM(input_tokens), SUM(output_tokens), SUM(cache_read_tokens), SUM(`+totalTokens+`)
		FROM request_log WHERE `+where+` GROUP BY model_upstream ORDER BY SUM(`+totalTokens+`) DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("按模型用量汇总: %w", err)
	}
	defer rows.Close()
	out := []ModelStat{}
	for rows.Next() {
		var r ModelStat
		if err := rows.Scan(&r.Model, &r.Requests, &r.InputTokens, &r.OutputTokens, &r.CacheRead, &r.TotalTokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Cleanup 删除保留期外的记录，返回删除行数。
func (s *Store) Cleanup(retainDays int) (int64, error) {
	if retainDays < 1 {
		retainDays = 90
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays).Unix()
	res, err := s.db.Exec(`DELETE FROM request_log WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
