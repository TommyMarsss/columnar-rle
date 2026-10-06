// Command demo 在一组示例列数据上执行 RLE 编码与多组谓词查询，
// 然后生成单一的、数据与逻辑全部内嵌的静态报告 report.html
// （不启动任何服务、不依赖任何第三方库）。
//
// 用法：
//
//	go run ./cmd/demo            # 写入 ./report.html
//	go run ./cmd/demo out.html   # 指定输出路径
package main

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TommyMarsss/columnar-rle"
)

func main() {
	out := "report.html"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}

	// ---- 1. 构造示例列：小值域、含长游程，刻意触发两类分段 ----
	const (
		maxCount  = int64(5) // 计数字段只允许 1..5，长度 11 的游程必须拆成 5+5+1
		blockRows = int64(8) // 每块 8 行，长游程同时在块边界被切开
	)
	var values []int64
	put := func(v int64, n int) {
		for i := 0; i < n; i++ {
			values = append(values, v)
		}
	}
	put(3, 5)  // 块 0 全部
	put(7, 11) // 跨块 0/1/2，且 11 > maxCount 5 -> 5+5+1 三段
	put(1, 1)
	put(2, 1)
	put(9, 3) // 块 2/3
	put(0, 6) // 跨块 3/4
	put(5, 4)
	put(2, 2)
	put(8, 5)
	put(4, 1)

	col, err := rle.EncodeBlocked(values, maxCount, blockRows)
	if err != nil {
		fail(err)
	}
	// 解码自检：报告里展示的压缩列必须能无损还原。
	decoded, err := col.DecodeColumn()
	if err != nil {
		fail(err)
	}
	roundTripOK := len(decoded) == len(values)
	for i := range values {
		if decoded[i] != values[i] {
			roundTripOK = false
		}
	}
	if !roundTripOK {
		fail(fmt.Errorf("round-trip mismatch"))
	}

	// ---- 2. 组织编码结构（物理分段 + 逻辑游程 + 块内 run 切片）----
	allRuns := col.Runs()
	type runJSON struct {
		Index int   `json:"index"`
		Value int64 `json:"value"`
		Count int64 `json:"count"`
	}
	runs := make([]runJSON, len(allRuns))
	for i, r := range allRuns {
		runs[i] = runJSON{Index: i, Value: r.Value, Count: r.Count}
	}
	type blockJSON struct {
		Index    int       `json:"index"`
		RowStart int64     `json:"rowStart"`
		RowCount int64     `json:"rowCount"`
		Min      int64     `json:"min"`
		Max      int64     `json:"max"`
		Sum      string    `json:"sum"`
		SumValid bool      `json:"sumValid"`
		Runs     []runJSON `json:"runs"`
	}
	blocks := col.Blocks()
	bjs := make([]blockJSON, len(blocks))
	for i, b := range blocks {
		bj := blockJSON{
			Index: b.Index, RowStart: b.RowStart, RowCount: b.RowCount,
			Min: b.Min, Max: b.Max, SumValid: b.SumValid,
		}
		if b.SumValid {
			bj.Sum = fmt.Sprintf("%d", b.Sum)
		} else {
			bj.Sum = "溢出"
		}
		for ri := b.RunStart; ri < b.RunEnd; ri++ {
			bj.Runs = append(bj.Runs, runs[ri])
		}
		bjs[i] = bj
	}

	// ---- 3. 一组有代表性的查询 ----
	type querySpec struct {
		id   string
		kind string
		text string
		eval func(int64) bool
		run  func() *rle.QueryResult
		op   string
		val  *int64
		lo   *int64
		hi   *int64
	}
	v := func(x int64) *int64 { return &x }
	specs := []querySpec{
		{id: "eq9", kind: "scalar", op: "=", val: v(9), text: "value = 9",
			eval: func(x int64) bool { return x == 9 },
			run:  func() *rle.QueryResult { return col.QueryScalar(rle.Predicate{Op: rle.OpEq, Value: 9}) }},
		{id: "eq6", kind: "scalar", op: "=", val: v(6), text: "value = 6（值不存在，但落在多个块的值域内）",
			eval: func(x int64) bool { return x == 6 },
			run:  func() *rle.QueryResult { return col.QueryScalar(rle.Predicate{Op: rle.OpEq, Value: 6}) }},
		{id: "eq100", kind: "scalar", op: "=", val: v(100), text: "value = 100（超出整列值域，所有块都应跳过）",
			eval: func(x int64) bool { return x == 100 },
			run:  func() *rle.QueryResult { return col.QueryScalar(rle.Predicate{Op: rle.OpEq, Value: 100}) }},
		{id: "lt5", kind: "scalar", op: "<", val: v(5), text: "value < 5",
			eval: func(x int64) bool { return x < 5 },
			run:  func() *rle.QueryResult { return col.QueryScalar(rle.Predicate{Op: rle.OpLt, Value: 5}) }},
		{id: "range47", kind: "range", lo: v(4), hi: v(7), text: "4 ≤ value ≤ 7（区间在多个块中间切开）",
			eval: func(x int64) bool { return x >= 4 && x <= 7 },
			run:  func() *rle.QueryResult { return col.QueryRange(rle.RangePredicate{Lo: 4, Hi: 7}) }},
		{id: "range77", kind: "range", lo: v(7), hi: v(7), text: "7 ≤ value ≤ 7（等值区间，命中跨 3 个块的长游程）",
			eval: func(x int64) bool { return x == 7 },
			run:  func() *rle.QueryResult { return col.QueryRange(rle.RangePredicate{Lo: 7, Hi: 7}) }},
		{id: "range09", kind: "range", lo: v(0), hi: v(9), text: "0 ≤ value ≤ 9（全覆盖，所有块整块命中）",
			eval: func(x int64) bool { return x >= 0 && x <= 9 },
			run:  func() *rle.QueryResult { return col.QueryRange(rle.RangePredicate{Lo: 0, Hi: 9}) }},
	}

	type runTraceJSON struct {
		RunIndex int    `json:"runIndex"`
		Value    int64  `json:"value"`
		Count    int64  `json:"count"`
		Decision int    `json:"decision"`
		Reason   string `json:"reason"`
	}
	type blockTraceJSON struct {
		BlockIndex int            `json:"blockIndex"`
		RowStart   int64          `json:"rowStart"`
		RowCount   int64          `json:"rowCount"`
		Min        int64          `json:"min"`
		Max        int64          `json:"max"`
		Decision   int            `json:"decision"`
		Reason     string         `json:"reason"`
		Runs       []runTraceJSON `json:"runs,omitempty"`
	}
	type queryJSON struct {
		ID              string           `json:"id"`
		Kind            string           `json:"kind"`
		Text            string           `json:"text"`
		Op              string           `json:"op,omitempty"`
		Value           *int64           `json:"value,omitempty"`
		Lo              *int64           `json:"lo,omitempty"`
		Hi              *int64           `json:"hi,omitempty"`
		Blocks          []blockTraceJSON `json:"blocks"`
		MatchCount      int64            `json:"matchCount"`
		Sum             string           `json:"sum"`
		SkippedBlocks   int              `json:"skippedBlocks"`
		WholeHitBlocks  int              `json:"wholeHitBlocks"`
		PartialBlocks   int              `json:"partialBlocks"`
		DrilledRuns     int              `json:"drilledRuns"`
		RunSkippedRows  int64            `json:"runSkippedRows"`
		RunAcceptedRows int64            `json:"runAcceptedRows"`
		// Brute* 是对完全展开的原始数据逐行扫描得到的参照结果，
		// 报告中用它与下推结果逐查询比对。
		BruteCount int64  `json:"bruteCount"`
		BruteSum   string `json:"bruteSum"`
	}

	queries := make([]queryJSON, 0, len(specs))
	for _, s := range specs {
		q := s.run()
		bj := make([]blockTraceJSON, len(q.Blocks))
		for i, t := range q.Blocks {
			bj[i] = blockTraceJSON{
				BlockIndex: t.BlockIndex, RowStart: t.RowStart, RowCount: t.RowCount,
				Min: t.Min, Max: t.Max, Decision: int(t.Decision), Reason: t.Reason,
			}
			for _, rt := range t.Runs {
				bj[i].Runs = append(bj[i].Runs, runTraceJSON{
					RunIndex: rt.RunIndex, Value: rt.Value, Count: rt.Count,
					Decision: int(rt.Decision), Reason: rt.Reason,
				})
			}
		}
		var bruteCnt int64
		bruteSum := new(big.Int)
		for _, x := range values {
			if s.eval(x) {
				bruteCnt++
				bruteSum.Add(bruteSum, big.NewInt(x))
			}
		}
		queries = append(queries, queryJSON{
			ID: s.id, Kind: s.kind, Text: s.text, Op: s.op, Value: s.val, Lo: s.lo, Hi: s.hi,
			Blocks: bj, MatchCount: q.MatchCount, Sum: q.Sum.String(),
			SkippedBlocks: q.SkippedBlocks, WholeHitBlocks: q.WholeHitBlocks,
			PartialBlocks: q.PartialBlocks, DrilledRuns: q.DrilledRuns,
			RunSkippedRows: q.RunSkippedRows, RunAcceptedRows: q.RunAcceptedRows,
			BruteCount: bruteCnt, BruteSum: bruteSum.String(),
		})
	}

	// ---- 4. 体积对比 ----
	rawBytes := int64(len(values)) * 8
	payload := map[string]any{
		"generatedAt": time.Now().Format("2006-01-02 15:04:05"),
		"config": map[string]int64{
			"maxCount": maxCount, "blockRows": blockRows,
			"countFieldWidth": int64(rle.CountFieldWidth(maxCount)),
		},
		"values":       values,
		"runs":         runs,
		"logicalRuns":  col.LogicalRuns(),
		"blocks":       bjs,
		"queries":      queries,
		"rawBytes":     rawBytes,
		"encodedBytes": col.EncodedSize(),
		"rowCount":     col.RowCount(),
		"roundTripOK":  roundTripOK,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		fail(err)
	}
	// 防止 JSON 中偶然出现 "</script>" 提前结束脚本块。
	jsonBlob := string(data)
	html := strings.ReplaceAll(reportHTML, "/*__DATA__*/", jsonBlob)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fail(err)
	}
	if err := os.WriteFile(out, []byte(html), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("已生成 %s（%d 行，%d 个物理 run，%d 个块，%d 组查询）\n",
		out, len(values), len(runs), len(blocks), len(queries))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "demo:", err)
	os.Exit(1)
}
