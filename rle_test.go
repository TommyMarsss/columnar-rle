package rle

import (
	"math"
	"math/big"
	"math/rand"
	"testing"
)

// expectBlockDecision 是下推决策的独立参考实现：只看一块原始数据的
// 实际命中数与该块 min/max，给出“仅凭 min/max 块头”能达到的最优三分类。
//
// 关键语义：实际零命中 ≠ 可跳过。例如块 {1,5} 对谓词 =3 没有任何命中，
// 但 3 落在 [1,5] 内，min/max 无法证明 3 不存在，必须展开到 run 级
// （run 同值，到 run 级即可整体跳过，无需逐行还原）。
func expectBlockDecision(chunk []int64, op Op, v int64, isRange bool, lo, hi int64) Decision {
	mn, mx := chunk[0], chunk[0]
	matches := 0
	for _, x := range chunk {
		if x < mn {
			mn = x
		}
		if x > mx {
			mx = x
		}
		var m bool
		if isRange {
			m = x >= lo && x <= hi
		} else {
			switch op {
			case OpEq:
				m = x == v
			case OpNe:
				m = x != v
			case OpLt:
				m = x < v
			case OpLe:
				m = x <= v
			case OpGt:
				m = x > v
			case OpGe:
				m = x >= v
			}
		}
		if m {
			matches++
		}
	}
	switch {
	case matches == len(chunk):
		return AllMatch
	case matches > 0:
		return PartialMatch
	}
	// 零命中：只有当谓词的命中域与 [mn,mx] 不相交时才可整块跳过。
	provableNone := false
	if isRange {
		provableNone = mx < lo || mn > hi
	} else {
		switch op {
		case OpEq:
			provableNone = v < mn || v > mx
		case OpNe:
			provableNone = mn == mx // 零命中说明常量值恰为 v
		case OpLt:
			provableNone = mn >= v
		case OpLe:
			provableNone = mn > v
		case OpGt:
			provableNone = mx <= v
		case OpGe:
			provableNone = mx < v
		}
	}
	if provableNone {
		return NoneMatch
	}
	return PartialMatch
}

// bruteBlockDecisions 按 blockRows 对原始数据切块，独立给出每块在
// min/max 块头下的最优决策，不依赖被测的下推代码路径。
func bruteBlockDecisions(values []int64, blockRows int64, op Op, v int64, isRange bool, lo, hi int64) []Decision {
	var want []Decision
	for off := 0; off < len(values); off += int(blockRows) {
		end := off + int(blockRows)
		if end > len(values) {
			end = len(values)
		}
		want = append(want, expectBlockDecision(values[off:end], op, v, isRange, lo, hi))
	}
	return want
}

func bruteAggregate(values []int64, eval func(int64) bool) (int64, *big.Int) {
	var cnt int64
	sum := new(big.Int)
	for _, v := range values {
		if eval(v) {
			cnt++
			sum.Add(sum, big.NewInt(v))
		}
	}
	return cnt, sum
}

func TestEncodeDecodeEdges(t *testing.T) {
	cases := map[string][]int64{
		"empty":        {},
		"single":       {42},
		"all same":     {7, 7, 7, 7, 7},
		"all distinct": {1, 2, 3, 4, 5},
		"alternating":  {1, 1, 2, 2, 1, 1, 3, 3, 3},
		"negatives":    {-5, -5, -5, 0, 0, 1, 1, 1, 1},
		"extremes":     {math.MinInt64, math.MinInt64, math.MaxInt64, math.MaxInt64},
	}
	for name, vals := range cases {
		t.Run(name, func(t *testing.T) {
			runs := Encode(vals)
			got, err := Decode(runs)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}
			if !equalInt64(got, vals) {
				t.Fatalf("round-trip mismatch\nwant %v\ngot  %v", vals, got)
			}
		})
	}
}

func TestCountSegmentation(t *testing.T) {
	// 分段纯函数：n=10, max=3 -> [3,3,3,1]，无溢出无截断。
	got := segmentCounts(10, 3)
	want := []int64{3, 3, 3, 1}
	if !equalInt64(got, want) {
		t.Fatalf("segmentCounts(10,3) = %v, want %v", got, want)
	}

	// 恰好等于字段容量：一段；超出 1：拆成满段 + 余数 1。
	if segs := segmentCounts(7, 7); len(segs) != 1 || segs[0] != 7 {
		t.Fatalf("exact-fit segments = %v, want [7]", segs)
	}
	if segs := segmentCounts(8, 7); len(segs) != 2 || segs[0] != 7 || segs[1] != 1 {
		t.Fatalf("overflow-by-1 segments = %v, want [7 1]", segs)
	}

	// uint32 字段容量下的超大游程（不实际分配 80 亿行，只验分段数学）。
	n := 2*MaxCountDefault + 5
	segs := segmentCounts(n, MaxCountDefault)
	if len(segs) != 3 || segs[0] != MaxCountDefault || segs[1] != MaxCountDefault || segs[2] != 5 {
		t.Fatalf("huge segments wrong: len=%d tail=%d", len(segs), segs[len(segs)-1])
	}
	var recomputed int64
	for _, s := range segs {
		if s > MaxCountDefault || s < 1 {
			t.Fatalf("segment count %d out of field range [1,%d]", s, MaxCountDefault)
		}
		recomputed += s
	}
	if recomputed != n {
		t.Fatalf("segmented total %d != original %d (truncation)", recomputed, n)
	}
}

func TestEncodeWithSmallMaxRoundTrip(t *testing.T) {
	// 用 maxCount=3 强制大量分段，覆盖真实编码路径（含跨段同值）。
	vals := []int64{9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 2, 2, 2, 2, 5, 1, 1}
	runs := EncodeWithMax(vals, 3)
	for i, r := range runs {
		if r.Count > 3 {
			t.Fatalf("run %d count %d exceeds maxCount 3", i, r.Count)
		}
	}
	got, err := Decode(runs)
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(got, vals) {
		t.Fatalf("round-trip mismatch\nwant %v\ngot  %v", vals, got)
	}
	// 逻辑游程合并后应只剩 9x10, 2x4, 5x1, 1x2。
	merged := MergeRuns(runs)
	wantMerged := []Run{{9, 10}, {2, 4}, {5, 1}, {1, 2}}
	if len(merged) != len(wantMerged) {
		t.Fatalf("merged runs = %v, want %v", merged, wantMerged)
	}
	for i := range wantMerged {
		if merged[i] != wantMerged[i] {
			t.Fatalf("merged run %d = %+v, want %+v", i, merged[i], wantMerged[i])
		}
	}
}

func TestNewColumnRejectsOverflow(t *testing.T) {
	if _, err := NewColumn([]Run{{1, 2}}, 1, 2); err == nil {
		t.Fatal("expected error for run count exceeding maxCount")
	}
	if _, err := NewColumn([]Run{{1, 0}}, 2, 2); err == nil {
		t.Fatal("expected error for non-positive run count")
	}
}

func TestBlockedEncodingStructure(t *testing.T) {
	// 20 行同值、每块 6 行：游程必须在块边界切开，块大小固定。
	vals := make([]int64, 20)
	for i := range vals {
		vals[i] = 5
	}
	col, err := EncodeBlocked(vals, 100, 6)
	if err != nil {
		t.Fatal(err)
	}
	blocks := col.Blocks()
	if len(blocks) != 4 {
		t.Fatalf("got %d blocks, want 4", len(blocks))
	}
	var total int64
	for i, b := range blocks {
		wantRows := int64(6)
		if i == 3 {
			wantRows = 2
		}
		if b.RowCount != wantRows {
			t.Fatalf("block %d rows=%d, want %d", i, b.RowCount, wantRows)
		}
		if b.Min != 5 || b.Max != 5 {
			t.Fatalf("block %d stats wrong: [%d,%d]", i, b.Min, b.Max)
		}
		total += b.RowCount
	}
	if total != 20 {
		t.Fatalf("block rows total %d, want 20", total)
	}
	got, err := col.DecodeColumn()
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(got, vals) {
		t.Fatalf("blocked round-trip mismatch (len got=%d want=%d)", len(got), len(vals))
	}
}

func TestDecideScalarTable(t *testing.T) {
	// 块值域 [1,10]。
	mn, mx := int64(1), int64(10)
	cases := []struct {
		op   Op
		v    int64
		want Decision
	}{
		{OpEq, 5, PartialMatch}, // 经典坑：端点不等于 5，块内可能等于 5
		{OpEq, 0, NoneMatch},    // 目标在值域左侧
		{OpEq, 11, NoneMatch},   // 目标在值域右侧
		{OpEq, 1, PartialMatch}, // 端点命中但内部不全是
		{OpNe, 5, PartialMatch}, // 部分等于 5
		{OpNe, 0, AllMatch},     // 目标在值域之外，全部不等于
		{OpNe, 1, PartialMatch}, // 只有第一个值不满足
		{OpLt, 11, AllMatch},
		{OpLt, 10, PartialMatch}, // 只有 10 不满足
		{OpLt, 1, NoneMatch},
		{OpLe, 10, AllMatch},
		{OpLe, 0, NoneMatch},
		{OpGt, 0, AllMatch},
		{OpGt, 1, PartialMatch},
		{OpGt, 10, NoneMatch},
		{OpGe, 1, AllMatch},
		{OpGe, 2, PartialMatch},
		{OpGe, 11, NoneMatch},
	}
	for _, tc := range cases {
		if got := DecideScalar(Predicate{Op: tc.op, Value: tc.v}, mn, mx); got != tc.want {
			t.Errorf("%s %d on [1,10] = %s, want %s", tc.op, tc.v, got, tc.want)
		}
	}
	// 常量块 [7,7] 上等值必须是整块命中。
	if got := DecideScalar(Predicate{Op: OpEq, Value: 7}, 7, 7); got != AllMatch {
		t.Errorf("eq on constant block = %s, want all", got)
	}
	if got := DecideScalar(Predicate{Op: OpNe, Value: 7}, 7, 7); got != NoneMatch {
		t.Errorf("ne on constant block = %s, want none", got)
	}
}

func TestDecideRangeTable(t *testing.T) {
	mn, mx := int64(1), int64(10)
	cases := []struct {
		lo, hi int64
		want   Decision
	}{
		{5, 8, PartialMatch},   // 块比区间宽
		{0, 20, AllMatch},      // 区间包住块
		{11, 20, NoneMatch},    // 落在右侧
		{-5, 0, NoneMatch},     // 落在左侧
		{10, 20, PartialMatch}, // 仅端点 10 命中——区间在块边界切开
		{0, 1, PartialMatch},   // 仅端点 1 命中
		{10, 10, PartialMatch}, // 等值
		{1, 10, AllMatch},
	}
	for _, tc := range cases {
		if got := DecideRange(tc.lo, tc.hi, mn, mx); got != tc.want {
			t.Errorf("range [%d,%d] vs [1,10] = %s, want %s", tc.lo, tc.hi, got, tc.want)
		}
	}
}

// sampleColumn 构造一个跨多块、含长游程与多 run 块的列。
func sampleColumn(t *testing.T) (*Column, []int64) {
	t.Helper()
	var vals []int64
	put := func(v int64, n int) {
		for i := 0; i < n; i++ {
			vals = append(vals, v)
		}
	}
	put(1, 3)
	put(5, 4)
	put(9, 2)
	put(2, 5)
	put(7, 3)
	put(4, 4)
	put(9, 2)
	put(0, 3)
	col, err := EncodeBlocked(vals, MaxCountDefault, 5) // 每块 5 行，故意制造跨块长游程
	if err != nil {
		t.Fatal(err)
	}
	return col, vals
}

func checkAgainstBrute(t *testing.T, col *Column, vals []int64, blockRows int64,
	eval func(int64) bool, q *QueryResult, op Op, v int64, isRange bool, lo, hi int64) {
	t.Helper()
	wantDec := bruteBlockDecisions(vals, blockRows, op, v, isRange, lo, hi)
	if len(q.Blocks) != len(wantDec) {
		t.Fatalf("trace blocks=%d, want %d", len(q.Blocks), len(wantDec))
	}
	var wantSkip, wantAll, wantPart int
	for i, d := range wantDec {
		if q.Blocks[i].Decision != d {
			t.Errorf("block %d decision=%s, want %s", i, q.Blocks[i].Decision, d)
		}
		switch d {
		case NoneMatch:
			wantSkip++
		case AllMatch:
			wantAll++
		default:
			wantPart++
		}
	}
	// 核心断言：跳过/整块命中/展开的块数必须与理论值逐个一致。
	if q.SkippedBlocks != wantSkip {
		t.Errorf("skipped blocks=%d, want %d（多跳过会漏结果，少跳过是无谓解压）", q.SkippedBlocks, wantSkip)
	}
	if q.WholeHitBlocks != wantAll {
		t.Errorf("whole-hit blocks=%d, want %d", q.WholeHitBlocks, wantAll)
	}
	if q.PartialBlocks != wantPart {
		t.Errorf("partial blocks=%d, want %d", q.PartialBlocks, wantPart)
	}
	// 被跳过/整块命中的块绝不能出现在“实际读取 run”的集合里。
	for i, d := range wantDec {
		accessed := q.AccessedBlocks[i]
		if d == PartialMatch && !accessed {
			t.Errorf("block %d should have been expanded but its runs were never read", i)
		}
		if d != PartialMatch && accessed {
			t.Errorf("block %d was decided %s but its runs were read (unnecessary expansion)", i, d)
		}
	}
	// 聚合结果必须与全展开扫描一致。
	wantCnt, wantSum := bruteAggregate(vals, eval)
	if q.MatchCount != wantCnt {
		t.Errorf("match count=%d, want %d", q.MatchCount, wantCnt)
	}
	if q.Sum.Cmp(wantSum) != 0 {
		t.Errorf("sum=%s, want %s", q.Sum, wantSum)
	}
}

func TestScalarPushdown(t *testing.T) {
	col, vals := sampleColumn(t)
	preds := []Predicate{
		{Op: OpEq, Value: 9},
		{Op: OpEq, Value: 100}, // 无任何命中：所有块都应跳过
		{Op: OpEq, Value: 1},   // 仅首块部分命中
		{Op: OpNe, Value: 9},
		{Op: OpLt, Value: 5},
		{Op: OpLe, Value: 5},
		{Op: OpGt, Value: 6},
		{Op: OpGe, Value: 9},
	}
	for _, p := range preds {
		t.Run(p.String(), func(t *testing.T) {
			q := col.QueryScalar(p)
			checkAgainstBrute(t, col, vals, 5, p.Eval, q, p.Op, p.Value, false, 0, 0)
		})
	}
}

func TestRangePushdownAcrossBlockBoundaries(t *testing.T) {
	col, vals := sampleColumn(t)
	ranges := []RangePredicate{
		{4, 7},     // 常规区间，边缘块在块中间切开
		{3, 3},     // 无此值：全部块跳过且无漏判
		{0, 9},     // 全值域：所有块整块命中
		{5, 5},     // 等值区间
		{9, 9},     // 长游程 9 跨块边界
		{-1, 1},    // 仅触达首块中的值 1
		{4, 4},     // 末段附近
		{2, 2},     // 值 2 的游程长 5，恰跨块边界
		{-100, -1}, // 全不命中
	}
	for _, r := range ranges {
		t.Run(r.String(), func(t *testing.T) {
			q := col.QueryRange(r)
			checkAgainstBrute(t, col, vals, 5, r.EvalRow, q, OpEq, 0, true, r.Lo, r.Hi)
		})
	}
}

func TestRangeCutExactlyAtBlockAndRunBoundaries(t *testing.T) {
	// 数据按 5 行/块：[1,1,1,1,1][1,1,2,2,2][2,2,4,4,4]...
	vals := []int64{1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 4, 4, 4}
	col, err := EncodeBlocked(vals, MaxCountDefault, 5)
	if err != nil {
		t.Fatal(err)
	}
	// 区间上界正好切在 1 的长游程中间（第 6 行之后），
	// 第二个块 [1,1,2,2,2] 必须被判定为部分块并精确取前 2 行。
	q := col.QueryRange(RangePredicate{Lo: 1, Hi: 1})
	wantCnt, wantSum := bruteAggregate(vals, func(v int64) bool { return v == 1 })
	if q.MatchCount != wantCnt || q.Sum.Cmp(wantSum) != 0 {
		t.Fatalf("[1,1] count=%d sum=%s, want count=%d sum=%s", q.MatchCount, q.Sum, wantCnt, wantSum)
	}
	if q.Blocks[1].Decision != PartialMatch {
		t.Fatalf("block 1 = %s, want partial (range cuts through a run mid-block)", q.Blocks[1].Decision)
	}
}

func TestRandomRoundTripAndPushdown(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 50; iter++ {
		n := rng.Intn(200) + 1
		vals := make([]int64, n)
		for i := range vals {
			// 小值域制造长游程，允许负数。
			vals[i] = int64(rng.Intn(12) - 3)
		}
		blockRows := int64(1 + rng.Intn(17))
		col, err := EncodeBlocked(vals, 3+int64(rng.Intn(50)), blockRows)
		if err != nil {
			t.Fatal(err)
		}
		got, err := col.DecodeColumn()
		if err != nil {
			t.Fatal(err)
		}
		if !equalInt64(got, vals) {
			t.Fatalf("iter %d: round-trip mismatch", iter)
		}
		lo := int64(rng.Intn(12) - 4)
		hi := lo + int64(rng.Intn(8))
		r := RangePredicate{Lo: lo, Hi: hi}
		q := col.QueryRange(r)
		checkAgainstBrute(t, col, vals, blockRows, r.EvalRow, q, OpEq, 0, true, lo, hi)
	}
}

func TestSumOverflowUsesBigInt(t *testing.T) {
	// MaxInt64 重复 3 次：int64 和必然溢出，但下推聚合仍须精确。
	vals := []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64, 1, 1}
	col, err := EncodeBlocked(vals, MaxCountDefault, 2)
	if err != nil {
		t.Fatal(err)
	}
	b0 := col.Blocks()[0]
	if b0.SumValid {
		t.Fatal("expected block int64 sum to be marked invalid on overflow")
	}
	q := col.QueryRange(RangePredicate{Lo: math.MaxInt64, Hi: math.MaxInt64})
	if q.MatchCount != 3 {
		t.Fatalf("count=%d, want 3", q.MatchCount)
	}
	want := new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(3))
	if q.Sum.Cmp(want) != 0 {
		t.Fatalf("sum=%s, want %s", q.Sum, want)
	}
	if _, ok := q.SumInt64(); ok {
		t.Fatal("SumInt64 should report overflow")
	}

	// 全表扫描口径一致。
	qAll := col.QueryRange(RangePredicate{Lo: math.MinInt64, Hi: math.MaxInt64})
	wantAll := new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(3))
	wantAll.Add(wantAll, big.NewInt(2))
	if qAll.Sum.Cmp(wantAll) != 0 {
		t.Fatalf("all-row sum=%s, want %s", qAll.Sum, wantAll)
	}
}

func TestAllSameAndAllDifferentColumns(t *testing.T) {
	// 全部相同：每个块都应整块命中或整块跳过，不存在部分块。
	same := make([]int64, 17)
	for i := range same {
		same[i] = 8
	}
	col, err := EncodeBlocked(same, 4, 5) // maxCount=4 强制计数字段分段
	if err != nil {
		t.Fatal(err)
	}
	got, err := col.DecodeColumn()
	if err != nil {
		t.Fatal(err)
	}
	if !equalInt64(got, same) {
		t.Fatal("all-same round-trip mismatch")
	}
	q := col.QueryScalar(Predicate{Op: OpEq, Value: 8})
	if q.PartialBlocks != 0 || q.WholeHitBlocks != len(q.Blocks) {
		t.Fatalf("all-same: partial=%d whole=%d skip=%d, want 0/%d/0", q.PartialBlocks, q.WholeHitBlocks, q.SkippedBlocks, len(q.Blocks))
	}
	if q.MatchCount != 17 {
		t.Fatalf("all-same count=%d, want 17", q.MatchCount)
	}

	// 全部不同：任何非全覆盖等值谓词都会产生部分块。
	distinct := []int64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	col2, err := EncodeBlocked(distinct, MaxCountDefault, 4)
	if err != nil {
		t.Fatal(err)
	}
	q2 := col2.QueryScalar(Predicate{Op: OpEq, Value: 7})
	checkAgainstBrute(t, col2, distinct, 4, func(v int64) bool { return v == 7 }, q2, OpEq, 7, false, 0, 0)
}

func equalInt64(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
