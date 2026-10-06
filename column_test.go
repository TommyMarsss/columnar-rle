package columnar

import (
	"math/rand"
	"reflect"
	"testing"
)

// bruteFilter is the reference implementation over fully expanded data.
func bruteFilter(values []int64, p Predicate) (indices []int, vals []int64) {
	for i, v := range values {
		if p.Match(v) {
			indices = append(indices, i)
			vals = append(vals, v)
		}
	}
	return indices, vals
}

func bruteAgg(values []int64, p Predicate) (count int, sum int64) {
	for _, v := range values {
		if p.Match(v) {
			count++
			sum += v
		}
	}
	return count, sum
}

// expectedStats derives the pushdown decision independently from the RAW
// data: it recomputes each chunk's min/max from the raw values and applies
// the zone-map rule. This must agree exactly with the stats recorded by
// Filter/Aggregate — proving every block that the min/max rule allows to be
// skipped was in fact skipped (no missed skips), and none beyond that.
func expectedStats(values []int64, blockSize int, p Predicate) QueryStats {
	var st QueryStats
	for start := 0; start < len(values); start += blockSize {
		end := start + blockSize
		if end > len(values) {
			end = len(values)
		}
		st.BlocksTotal++
		lo, hi := values[start], values[start]
		for _, v := range values[start:end] {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		switch {
		case p.Lo <= lo && hi <= p.Hi:
			st.BlocksAllMatch++
		case hi < p.Lo || lo > p.Hi:
			st.BlocksSkipped++
		default:
			st.BlocksExpanded++
		}
	}
	return st
}

// checkSoundness verifies the safety properties of pushdown against raw
// data: a skipped block must contain zero matching rows (no lost results),
// and an all-match block must have every row matching (no wrong results).
func checkSoundness(t *testing.T, values []int64, blockSize int, c Column, p Predicate) {
	t.Helper()
	rowBase := 0
	for bi, b := range c.Blocks {
		chunk := values[rowBase : rowBase+b.Rows]
		switch EvalBlock(b.Min, b.Max, p) {
		case VerdictNone:
			for _, v := range chunk {
				if p.Match(v) {
					t.Fatalf("block %d was SKIPPED but contains matching value %d", bi, v)
				}
			}
		case VerdictAll:
			for _, v := range chunk {
				if !p.Match(v) {
					t.Fatalf("block %d was ALL-MATCH but contains non-matching value %d", bi, v)
				}
			}
		}
		rowBase += b.Rows
	}
}

// demoValues builds a column with distinct block behaviors for blockSize 8:
//
//	block 0: all 5s            (uniform)
//	block 1: all 50s           (uniform, far away)
//	block 2: 1..8              (heterogeneous — range queries cut it mid-block)
//	block 3: all 5s again      (uniform, matches block 0's predicate)
//	block 4: 90..97            (heterogeneous, high values)
func demoValues() []int64 {
	var v []int64
	v = append(v, repeat(5, 8)...)
	v = append(v, repeat(50, 8)...)
	for i := int64(1); i <= 8; i++ {
		v = append(v, i)
	}
	v = append(v, repeat(5, 8)...)
	for i := int64(90); i <= 97; i++ {
		v = append(v, i)
	}
	return v
}

func TestColumnRoundTrip(t *testing.T) {
	values := demoValues()
	c := EncodeColumn(values, 8)
	if got := c.DecodeColumn(); !reflect.DeepEqual(values, got) {
		t.Fatal("column round trip mismatch")
	}
	if len(c.Blocks) != 5 {
		t.Fatalf("want 5 blocks, got %d", len(c.Blocks))
	}
	// Last-block-smaller case.
	c2 := EncodeColumn(values[:17], 8)
	if len(c2.Blocks) != 3 || c2.Blocks[2].Rows != 1 {
		t.Fatalf("partial last block wrong: %+v", c2.Blocks)
	}
	if got := c2.DecodeColumn(); !reflect.DeepEqual(values[:17], got) {
		t.Fatal("partial-last-block round trip mismatch")
	}
}

func TestFilterPushdownMatchesBruteForce(t *testing.T) {
	values := demoValues()
	c := EncodeColumn(values, 8)
	preds := map[string]Predicate{
		"eq-5":          Eq(5),
		"eq-50":         Eq(50),
		"eq-absent":     Eq(999),
		"range-3-6":     Between(3, 6),   // cuts block 2 mid-way
		"range-1-8":     Between(1, 8),   // exactly covers block 2
		"range-45-55":   Between(45, 55), // only block 1
		"range-all":     Between(-100, 100),
		"range-none":    Between(60, 70),
		"range-edge-90": Between(90, 90), // touches block 4 min edge
	}
	for name, p := range preds {
		res := c.Filter(p)
		wantIdx, wantVals := bruteFilter(values, p)
		if !reflect.DeepEqual(res.Indices, wantIdx) || !reflect.DeepEqual(res.Values, wantVals) {
			t.Errorf("%s: filter mismatch\n got idx=%v vals=%v\nwant idx=%v vals=%v",
				name, res.Indices, res.Values, wantIdx, wantVals)
		}
		if want := expectedStats(values, 8, p); res.Stats != want {
			t.Errorf("%s: stats %+v, want %+v (no extra skips, no missed skips)",
				name, res.Stats, want)
		}
		checkSoundness(t, values, 8, c, p)
	}
}

func TestFilterSkipsExactlyTheRightBlocks(t *testing.T) {
	values := demoValues()
	c := EncodeColumn(values, 8)

	// Eq(5): blocks 0 and 3 are all-5 (ALL), blocks 1 (50s) and 4 (90..97)
	// cannot match (SKIP), block 2 (1..8) contains 5 among other values
	// (PARTIAL — must be expanded).
	res := c.Filter(Eq(5))
	want := QueryStats{BlocksTotal: 5, BlocksSkipped: 2, BlocksAllMatch: 2, BlocksExpanded: 1}
	if res.Stats != want {
		t.Fatalf("Eq(5) stats %+v, want %+v", res.Stats, want)
	}
	if len(res.Values) != 8+8+1 {
		t.Fatalf("Eq(5) matched %d rows, want 17", len(res.Values))
	}

	// Eq(999): every block skipped, nothing decoded, empty result.
	res = c.Filter(Eq(999))
	if res.Stats.BlocksSkipped != 5 || res.Stats.BlocksExpanded != 0 || res.Stats.BlocksAllMatch != 0 {
		t.Fatalf("Eq(999) stats %+v, want all 5 skipped", res.Stats)
	}
	if len(res.Indices) != 0 {
		t.Fatalf("Eq(999) should match nothing, got %v", res.Indices)
	}
}

func TestAggregateCrossBlockBoundary(t *testing.T) {
	values := demoValues()
	c := EncodeColumn(values, 8)

	// Between(3, 6) cuts block 2 (values 1..8) in the middle: only rows
	// with values 3,4,5,6 of that block count, plus the two all-5 blocks.
	p := Between(3, 6)
	got := c.Aggregate(p)
	wantCount, wantSum := bruteAgg(values, p)
	if got.Count != wantCount || got.Sum != wantSum {
		t.Fatalf("Aggregate(%v) = (%d,%d), brute force = (%d,%d)",
			p, got.Count, got.Sum, wantCount, wantSum)
	}
	// The cut block must be expanded, not skipped nor accepted wholesale.
	if got.Stats.BlocksExpanded != 1 {
		t.Fatalf("cut block should be expanded exactly once: %+v", got.Stats)
	}
	// Expected: count = 8 (block0) + 4 (3,4,5,6 in block2) + 8 (block3) = 20
	// sum     = 40 + 18 + 40 = 98
	if got.Count != 20 || got.Sum != 98 {
		t.Fatalf("hand-computed expectation failed: count=%d sum=%d", got.Count, got.Sum)
	}
}

func TestAggregateMatchesBruteForceSweep(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	values := make([]int64, 1000)
	for i := range values {
		values[i] = int64(rng.Intn(40) - 20)
	}
	const bs = 32
	c := EncodeColumn(values, bs)
	for lo := int64(-22); lo <= 20; lo += 3 {
		for hi := lo; hi <= 22; hi += 3 {
			p := Between(lo, hi)
			got := c.Aggregate(p)
			wc, ws := bruteAgg(values, p)
			if got.Count != wc || got.Sum != ws {
				t.Fatalf("Aggregate[%d,%d] = (%d,%d), want (%d,%d)",
					lo, hi, got.Count, got.Sum, wc, ws)
			}
			if want := expectedStats(values, bs, p); got.Stats != want {
				t.Fatalf("Aggregate[%d,%d] stats %+v, want %+v", lo, hi, got.Stats, want)
			}
			fr := c.Filter(p)
			fi, _ := bruteFilter(values, p)
			if !reflect.DeepEqual(fr.Indices, fi) {
				t.Fatalf("Filter[%d,%d] indices mismatch", lo, hi)
			}
			checkSoundness(t, values, bs, c, p)
		}
	}
}

func TestAggregateSegmentedLongRun(t *testing.T) {
	// A single block whose one run exceeds MaxRunCount: aggregation must
	// still see every row, and the block must be accepted wholesale.
	values := repeat(9, MaxRunCount+10)
	c := EncodeColumn(values, len(values))
	if len(c.Blocks) != 1 {
		t.Fatalf("want 1 block, got %d", len(c.Blocks))
	}
	got := c.Aggregate(Eq(9))
	if got.Count != len(values) || got.Sum != int64(9*len(values)) {
		t.Fatalf("segmented aggregate = (%d,%d)", got.Count, got.Sum)
	}
	if got.Stats.BlocksAllMatch != 1 || got.Stats.BlocksExpanded != 0 {
		t.Fatalf("segmented block should be ALL-match: %+v", got.Stats)
	}
}
