// Package columnar implements a columnar RLE (Run-Length Encoding) storage
// format with predicate pushdown, using only the Go standard library.
package columnar

// MaxRunCount is the maximum repeat count representable in a single run's
// count field. The count field is 16 bits wide, so a run longer than
// MaxRunCount must be split into multiple consecutive runs (segmentation)
// instead of overflowing or truncating the counter.
const MaxRunCount = 1<<16 - 1 // 65535

// Run is one (value, count) pair: Value repeated Count times.
// Count is always in [1, MaxRunCount] for runs produced by Encode.
type Run struct {
	Value int64
	Count uint32
}

// Encode compresses a column of values into RLE runs. Adjacent equal values
// are merged; a run longer than MaxRunCount is split into as many full
// MaxRunCount segments as needed plus a remainder segment, so no count ever
// overflows the 16-bit count field.
func Encode(values []int64) []Run {
	runs := make([]Run, 0, len(values)/2+1)
	i := 0
	for i < len(values) {
		v := values[i]
		runLen := 1
		for i+runLen < len(values) && values[i+runLen] == v {
			runLen++
		}
		// Segment the run so every count fits the count field.
		for rem := runLen; rem > 0; {
			n := rem
			if n > MaxRunCount {
				n = MaxRunCount
			}
			runs = append(runs, Run{Value: v, Count: uint32(n)})
			rem -= n
		}
		i += runLen
	}
	return runs
}

// Decode expands RLE runs back into the original column. Runs with Count==0
// contribute nothing. Decode(Encode(v)) equals v for every v.
func Decode(runs []Run) []int64 {
	total := 0
	for _, r := range runs {
		total += int(r.Count)
	}
	out := make([]int64, 0, total)
	for _, r := range runs {
		for k := uint32(0); k < r.Count; k++ {
			out = append(out, r.Value)
		}
	}
	return out
}
