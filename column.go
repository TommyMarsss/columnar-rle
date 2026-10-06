package columnar

// DefaultBlockSize is the number of rows per encoded block when a caller
// does not specify one. Blocks are the unit of predicate pushdown: each
// block is encoded independently and carries its own min/max/sum statistics.
const DefaultBlockSize = 1024

// Block is one independently RLE-encoded chunk of a column, plus the
// statistics used for predicate pushdown.
type Block struct {
	Runs []Run // RLE pairs covering exactly Rows values
	Rows int   // number of decoded rows in this block
	Min  int64 // minimum value in the block
	Max  int64 // maximum value in the block
	Sum  int64 // sum of all values in the block
}

// Column is a column stored as a sequence of RLE-encoded blocks.
type Column struct {
	Blocks    []Block
	BlockSize int // row capacity per block (last block may be smaller)
	N         int // total number of rows
}

// EncodeColumn splits values into blocks of blockSize rows and RLE-encodes
// each block independently, computing per-block min/max/sum statistics.
// blockSize <= 0 means DefaultBlockSize.
func EncodeColumn(values []int64, blockSize int) Column {
	if blockSize <= 0 {
		blockSize = DefaultBlockSize
	}
	c := Column{BlockSize: blockSize, N: len(values)}
	for start := 0; start < len(values); start += blockSize {
		end := start + blockSize
		if end > len(values) {
			end = len(values)
		}
		chunk := values[start:end]
		b := Block{
			Runs: Encode(chunk),
			Rows: len(chunk),
			Min:  chunk[0],
			Max:  chunk[0],
		}
		for _, v := range chunk {
			if v < b.Min {
				b.Min = v
			}
			if v > b.Max {
				b.Max = v
			}
			b.Sum += v
		}
		c.Blocks = append(c.Blocks, b)
	}
	return c
}

// DecodeColumn fully expands the column back to the original values.
func (c Column) DecodeColumn() []int64 {
	out := make([]int64, 0, c.N)
	for _, b := range c.Blocks {
		out = append(out, Decode(b.Runs)...)
	}
	return out
}

// QueryStats records how predicate pushdown treated each block, so tests and
// the demo page can verify that exactly the right blocks were skipped.
type QueryStats struct {
	BlocksTotal    int // total blocks in the column
	BlocksSkipped  int // VerdictNone: not decoded at all
	BlocksAllMatch int // VerdictAll: accepted wholesale, not decoded
	BlocksExpanded int // VerdictPartial: decoded and checked row by row
}

// FilterResult is the outcome of a pushed-down filter query.
type FilterResult struct {
	Indices []int // row indices (in the original column) that match
	Values  []int64
	Stats   QueryStats
}

// Filter evaluates p with predicate pushdown: VerdictNone blocks are skipped
// without decoding, VerdictAll blocks contribute all their rows without
// decoding, and only VerdictPartial blocks are decoded and checked per row.
func (c Column) Filter(p Predicate) FilterResult {
	res := FilterResult{}
	res.Stats.BlocksTotal = len(c.Blocks)
	rowBase := 0
	for _, b := range c.Blocks {
		switch EvalBlock(b.Min, b.Max, p) {
		case VerdictNone:
			res.Stats.BlocksSkipped++
		case VerdictAll:
			res.Stats.BlocksAllMatch++
			for i := 0; i < b.Rows; i++ {
				res.Indices = append(res.Indices, rowBase+i)
			}
			// Values for an all-match block are obtained from the runs
			// metadata only when the caller needs them; to keep Filter
			// honest we expand values lazily here from runs (cheap) but
			// never touch row-level predicate checks.
			for _, r := range b.Runs {
				for k := uint32(0); k < r.Count; k++ {
					res.Values = append(res.Values, r.Value)
				}
			}
		case VerdictPartial:
			res.Stats.BlocksExpanded++
			vals := Decode(b.Runs)
			for i, v := range vals {
				if p.Match(v) {
					res.Indices = append(res.Indices, rowBase+i)
					res.Values = append(res.Values, v)
				}
			}
		}
		rowBase += b.Rows
	}
	return res
}

// AggResult is the outcome of a pushed-down count/sum aggregation.
type AggResult struct {
	Count int
	Sum   int64
	Stats QueryStats
}

// Aggregate computes COUNT(*) and SUM(value) over rows matching p, pushing
// the predicate down to block level. VerdictAll blocks contribute their
// precomputed Rows/Sum directly; VerdictNone blocks are skipped; only
// VerdictPartial blocks are decoded. This stays correct when the query range
// cuts a block in the middle, because such a block is VerdictPartial and its
// rows are checked individually.
func (c Column) Aggregate(p Predicate) AggResult {
	res := AggResult{}
	res.Stats.BlocksTotal = len(c.Blocks)
	for _, b := range c.Blocks {
		switch EvalBlock(b.Min, b.Max, p) {
		case VerdictNone:
			res.Stats.BlocksSkipped++
		case VerdictAll:
			res.Stats.BlocksAllMatch++
			res.Count += b.Rows
			res.Sum += b.Sum
		case VerdictPartial:
			res.Stats.BlocksExpanded++
			for _, v := range Decode(b.Runs) {
				if p.Match(v) {
					res.Count++
					res.Sum += v
				}
			}
		}
	}
	return res
}
