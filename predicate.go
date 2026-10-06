package columnar

// Predicate is an equality or closed-range [Lo, Hi] predicate on int64
// values. For an equality predicate Lo == Hi == the target value.
type Predicate struct {
	Lo int64
	Hi int64
}

// Eq builds an equality predicate: value == v.
func Eq(v int64) Predicate { return Predicate{Lo: v, Hi: v} }

// Between builds a closed-range predicate: lo <= value <= hi.
// If lo > hi the bounds are swapped so the predicate stays well-formed.
func Between(lo, hi int64) Predicate {
	if lo > hi {
		lo, hi = hi, lo
	}
	return Predicate{Lo: lo, Hi: hi}
}

// Match reports whether a single value satisfies the predicate.
func (p Predicate) Match(v int64) bool { return p.Lo <= v && v <= p.Hi }

// Verdict is the result of evaluating a predicate against a whole block
// using only block-level statistics (min/max), without decoding values.
type Verdict int

const (
	// VerdictNone: no row in the block can match — the block is skipped
	// entirely (never decoded).
	VerdictNone Verdict = iota
	// VerdictAll: every row in the block matches — the block is accepted
	// wholesale, still without decoding individual values.
	VerdictAll
	// VerdictPartial: the block may contain both matching and non-matching
	// rows — it must be decoded and checked row by row.
	VerdictPartial
)

func (v Verdict) String() string {
	switch v {
	case VerdictNone:
		return "SKIP"
	case VerdictAll:
		return "ALL"
	default:
		return "PARTIAL"
	}
}

// EvalBlock classifies a block against p using only the block's min/max
// statistics:
//
//   - p covers [Min, Max] entirely  -> VerdictAll   (every value matches)
//   - [Min, Max] disjoint from p    -> VerdictNone  (no value can match)
//   - otherwise                     -> VerdictPartial
//
// This is exact, not approximate: min/max of a block are tight bounds, so a
// VerdictNone block is guaranteed to contain zero matching rows and skipping
// it can never lose results.
func EvalBlock(min, max int64, p Predicate) Verdict {
	if p.Lo <= min && max <= p.Hi {
		return VerdictAll
	}
	if max < p.Lo || min > p.Hi {
		return VerdictNone
	}
	return VerdictPartial
}
