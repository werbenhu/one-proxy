// 一次性诊断工具：读 usage.db 的最近记录，确认 provider_id 是否写入。
package main

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: inspect-usage <path-to-usage.db>")
		os.Exit(2)
	}
	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		panic(err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT datetime(created_at,'unixepoch','localtime'),
		channel_id, provider_id, model_requested, model_upstream,
		input_tokens, output_tokens, cache_read_tokens, status, error
		FROM request_log ORDER BY id DESC LIMIT 20`)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	for rows.Next() {
		var ts, chID, pvID, mReq, mUp, errStr string
		var in, out, cache, status int64
		if err := rows.Scan(&ts, &chID, &pvID, &mReq, &mUp, &in, &out, &cache, &status, &errStr); err != nil {
			panic(err)
		}
		fmt.Printf("%s | ch=%s | pv=[%s] | %s->%s | in=%d out=%d cache=%d | status=%d err=%q\n",
			ts, chID, pvID, mReq, mUp, in, out, cache, status, errStr)
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}

	// 按 provider 聚合
	fmt.Println("---- 按 provider_id 聚合 ----")
	prows, err := db.Query(`SELECT provider_id, COUNT(*), SUM(input_tokens+output_tokens+cache_read_tokens)
		FROM request_log GROUP BY provider_id`)
	if err != nil {
		panic(err)
	}
	defer prows.Close()
	for prows.Next() {
		var id string
		var n, total int64
		if err := prows.Scan(&id, &n, &total); err != nil {
			panic(err)
		}
		fmt.Printf("pv=[%s] rows=%d total=%d\n", id, n, total)
	}

	// 今天的记录
	fmt.Println("---- 今天的记录 ----")
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	trows, err := db.Query(`SELECT provider_id, COUNT(*), SUM(input_tokens+output_tokens+cache_read_tokens)
		FROM request_log WHERE created_at >= ? GROUP BY provider_id`, today)
	if err != nil {
		panic(err)
	}
	defer trows.Close()
	for trows.Next() {
		var id string
		var n, total int64
		if err := trows.Scan(&id, &n, &total); err != nil {
			panic(err)
		}
		fmt.Printf("pv=[%s] rows=%d total=%d\n", id, n, total)
	}
}
