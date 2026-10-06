package columnar

import "testing"

func TestEvalBlock(t *testing.T) {
	cases := []struct {
		name        string
		min, max    int64
		p           Predicate
		wantVerdict Verdict
	}{
		{"eq-hit-uniform", 5, 5, Eq(5), VerdictAll},
		{"eq-miss-uniform", 5, 5, Eq(6), VerdictNone},
		{"eq-inside-range", 1, 10, Eq(5), VerdictPartial},
		{"eq-at-min-edge", 5, 9, Eq(5), VerdictPartial},
		{"eq-at-max-edge", 1, 5, Eq(5), VerdictPartial},
		{"range-covers-block", 3, 8, Between(0, 10), VerdictAll},
		{"range-exactly-covers", 3, 8, Between(3, 8), VerdictAll},
		{"range-disjoint-below", 10, 20, Between(0, 9), VerdictNone},
		{"range-disjoint-above", 10, 20, Between(21, 30), VerdictNone},
		{"range-touching-min-is-partial", 10, 20, Between(0, 10), VerdictPartial},
		{"range-touching-max-is-partial", 10, 20, Between(20, 30), VerdictPartial},
		{"range-cuts-middle", 10, 20, Between(12, 15), VerdictPartial},
		{"negative-values", -20, -10, Between(-15, -5), VerdictPartial},
		{"negative-disjoint", -20, -10, Between(0, 5), VerdictNone},
	}
	for _, tc := range cases {
		if got := EvalBlock(tc.min, tc.max, tc.p); got != tc.wantVerdict {
			t.Errorf("%s: EvalBlock(%d,%d,%+v) = %v, want %v",
				tc.name, tc.min, tc.max, tc.p, got, tc.wantVerdict)
		}
	}
}

func TestPredicateMatch(t *testing.T) {
	if !Eq(3).Match(3) || Eq(3).Match(4) {
		t.Fatal("Eq match wrong")
	}
	p := Between(2, 5)
	for _, v := range []int64{2, 3, 5} {
		if !p.Match(v) {
			t.Fatalf("Between(2,5) should match %d", v)
		}
	}
	for _, v := range []int64{1, 6} {
		if p.Match(v) {
			t.Fatalf("Between(2,5) should not match %d", v)
		}
	}
	// Swapped bounds are normalized.
	if Between(5, 2) != Between(2, 5) {
		t.Fatal("Between should normalize swapped bounds")
	}
}
