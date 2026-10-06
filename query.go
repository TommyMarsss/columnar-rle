package rle

import (
	"fmt"
	"math/big"
)

// RunTrace 是部分块内单个 run 的下钻决策。
type RunTrace struct {
	RunIndex int      `json:"runIndex"` // 全列 run 下标
	Value    int64    `json:"value"`
	Count    int64    `json:"count"`
	Decision Decision `json:"decision"` // 同值 run 只有 all/none
	Reason   string   `json:"reason"`
}

// BlockTrace 是一个块的完整下推决策记录。
type BlockTrace struct {
	BlockIndex int        `json:"blockIndex"`
	RowStart   int64      `json:"rowStart"`
	RowCount   int64      `json:"rowCount"`
	Min        int64      `json:"min"`
	Max        int64      `json:"max"`
	Decision   Decision   `json:"decision"`
	Reason     string     `json:"reason"`
	Runs       []RunTrace `json:"runs,omitempty"` // 仅部分块下钻到 run
}

// QueryResult 是一次谓词查询的结果与决策轨迹。
type QueryResult struct {
	PredicateText string       `json:"predicateText"`
	IsRange       bool         `json:"isRange"`
	Blocks        []BlockTrace `json:"blocks"`

	// 命中行数；命中值之和始终用 big.Int 精确保存。
	MatchCount int64    `json:"matchCount"`
	Sum        *big.Int `json:"sum"`

	// 下推统计。
	SkippedBlocks  int `json:"skippedBlocks"`  // 整块跳过（不读 run、不还原值）
	WholeHitBlocks int `json:"wholeHitBlocks"` // 整块命中（用块统计聚合，不读 run）
	PartialBlocks  int `json:"partialBlocks"`  // 下钻到 run 的块
	DrilledRuns    int `json:"drilledRuns"`    // 下钻时实际读取的 run 数

	RunSkippedRows  int64 `json:"runSkippedRows"`  // 下钻后 run 级跳过的行
	RunAcceptedRows int64 `json:"runAcceptedRows"` // 下钻后 run 级整体接受的行

	// AccessedBlocks 记录执行中真正读取了 run 的块下标。
	// 关键不变量：它恰好等于 PartialMatch 块集合；被跳过/整块命中
	// 的块的 run 一次都不会被访问。
	AccessedBlocks map[int]bool `json:"-"`
}

// SumInt64 在聚合和可装进 int64 时返回它。
func (q *QueryResult) SumInt64() (int64, bool) {
	if q.Sum == nil || !q.Sum.IsInt64() {
		return 0, false
	}
	return q.Sum.Int64(), true
}

// QueryScalar 在压缩列上执行等值/比较谓词并做 count、sum 聚合。
// 全程不调用 Decode：跳过块与整块命中块只访问块头统计。
func (c *Column) QueryScalar(p Predicate) *QueryResult {
	q := &QueryResult{PredicateText: p.String(), AccessedBlocks: map[int]bool{}, Sum: new(big.Int)}
	for i := range c.blocks {
		b := &c.blocks[i]
		d := DecideScalar(p, b.Min, b.Max)
		t := BlockTrace{
			BlockIndex: i, RowStart: b.RowStart, RowCount: b.RowCount,
			Min: b.Min, Max: b.Max, Decision: d,
		}
		switch d {
		case NoneMatch:
			t.Reason = fmt.Sprintf("块值域 [%d, %d] 与谓词（%s）不相交，整块跳过，不读取任何 run", b.Min, b.Max, p)
			q.SkippedBlocks++
		case AllMatch:
			t.Reason = fmt.Sprintf("块值域 [%d, %d] 全部满足（%s），整块 %d 行直接计入，不读取 run", b.Min, b.Max, p, b.RowCount)
			q.WholeHitBlocks++
			q.acceptWhole(c, b)
		default:
			t.Reason = fmt.Sprintf("块值域 [%d, %d] 与谓词（%s）部分重叠，展开块内 run 逐个判断", b.Min, b.Max, p)
			q.PartialBlocks++
			q.drillDown(c, i, p.Eval, &t)
		}
		q.Blocks = append(q.Blocks, t)
	}
	return q
}

// QueryRange 在压缩列上执行闭区间谓词 lo<=value<=hi 并聚合。
// 区间在某个块中间切开时该块为 PartialMatch，下钻到 run 精确定位。
func (c *Column) QueryRange(r RangePredicate) *QueryResult {
	q := &QueryResult{
		PredicateText: r.String(), IsRange: true,
		AccessedBlocks: map[int]bool{}, Sum: new(big.Int),
	}
	for i := range c.blocks {
		b := &c.blocks[i]
		d := DecideRange(r.Lo, r.Hi, b.Min, b.Max)
		t := BlockTrace{
			BlockIndex: i, RowStart: b.RowStart, RowCount: b.RowCount,
			Min: b.Min, Max: b.Max, Decision: d,
		}
		switch d {
		case NoneMatch:
			t.Reason = fmt.Sprintf("块值域 [%d, %d] 与查询区间 [%d, %d] 不相交，整块跳过，不读取任何 run", b.Min, b.Max, r.Lo, r.Hi)
			q.SkippedBlocks++
		case AllMatch:
			t.Reason = fmt.Sprintf("块值域 [%d, %d] 完全包含在 [%d, %d] 内，整块 %d 行直接计入，不读取 run", b.Min, b.Max, r.Lo, r.Hi, b.RowCount)
			q.WholeHitBlocks++
			q.acceptWhole(c, b)
		default:
			t.Reason = fmt.Sprintf("块值域 [%d, %d] 与 [%d, %d] 部分重叠（区间在块内切开），展开块内 run 逐个判断", b.Min, b.Max, r.Lo, r.Hi)
			q.PartialBlocks++
			q.drillDown(c, i, r.EvalRow, &t)
		}
		q.Blocks = append(q.Blocks, t)
	}
	return q
}

// acceptWhole 用块头统计直接聚合整块，不读取块内任何 run。
func (q *QueryResult) acceptWhole(c *Column, b *Block) {
	q.MatchCount += b.RowCount
	q.Sum.Add(q.Sum, c.blockSumsBig[b.Index])
}

// drillDown 把部分块展开为 run 序列逐个判定。run 内值恒定，所以单个
// run 对任意比较/区间谓词只有 all/none 两种结论：命中 run 直接按
// Count 聚合，跳过 run 的行完全不还原。
func (q *QueryResult) drillDown(c *Column, blockIdx int, eval func(int64) bool, t *BlockTrace) {
	q.AccessedBlocks[blockIdx] = true
	b := &c.blocks[blockIdx]
	for ri := b.RunStart; ri < b.RunEnd; ri++ {
		r := c.runs[ri]
		q.DrilledRuns++
		rt := RunTrace{RunIndex: ri, Value: r.Value, Count: r.Count}
		if eval(r.Value) {
			rt.Decision = AllMatch
			rt.Reason = fmt.Sprintf("run 值 %d 满足谓词，%d 行整体计入", r.Value, r.Count)
			q.MatchCount += r.Count
			q.RunAcceptedRows += r.Count
			q.Sum.Add(q.Sum, new(big.Int).Mul(big.NewInt(r.Value), big.NewInt(r.Count)))
		} else {
			rt.Decision = NoneMatch
			rt.Reason = fmt.Sprintf("run 值 %d 不满足谓词，%d 行整体跳过（不还原）", r.Value, r.Count)
			q.RunSkippedRows += r.Count
		}
		t.Runs = append(t.Runs, rt)
	}
}
