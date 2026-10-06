package columnar

import (
	"math/rand"
	"reflect"
	"testing"
)

func repeat(v int64, n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := map[string][]int64{
		"empty":    {},
		"single":   {42},
		"all-same": repeat(7, 1000),
		"all-distinct": func() []int64 {
			v := make([]int64, 500)
			for i := range v {
				v[i] = int64(i)
			}
			return v
		}(),
		"alternating": func() []int64 {
			v := make([]int64, 999)
			for i := range v {
				v[i] = int64(i % 2)
			}
			return v
		}(),
		"mixed":         {1, 1, 1, 2, 2, 3, 3, 3, 3, 1, 1, -5, -5, 0},
		"negative-runs": {-3, -3, -3, -1, 0, 0, 5},
	}
	for name, in := range cases {
		got := Decode(Encode(in))
		if len(in) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(in, got) {
			t.Errorf("%s: round trip mismatch: in=%v got=%v", name, in, got)
		}
	}
}

func TestEncodeMergesAdjacent(t *testing.T) {
	got := Encode([]int64{5, 5, 5, 2, 2, 9})
	want := []Run{{Value: 5, Count: 3}, {Value: 2, Count: 2}, {Value: 9, Count: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSegmentationBeyondMaxRunCount(t *testing.T) {
	cases := []struct {
		runLen int
		want   []Run
	}{
		{MaxRunCount - 1, []Run{{7, MaxRunCount - 1}}},
		{MaxRunCount, []Run{{7, MaxRunCount}}},
		{MaxRunCount + 1, []Run{{7, MaxRunCount}, {7, 1}}},
		{MaxRunCount + 5, []Run{{7, MaxRunCount}, {7, 5}}},
		{2*MaxRunCount + 7, []Run{{7, MaxRunCount}, {7, MaxRunCount}, {7, 7}}},
	}
	for _, tc := range cases {
		got := Encode(repeat(7, tc.runLen))
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("runLen=%d: got %v want %v", tc.runLen, got, tc.want)
		}
		for _, r := range got {
			if r.Count == 0 || r.Count > MaxRunCount {
				t.Errorf("runLen=%d: count %d out of range [1,%d]", tc.runLen, r.Count, MaxRunCount)
			}
		}
		if dec := Decode(got); !reflect.DeepEqual(dec, repeat(7, tc.runLen)) {
			t.Errorf("runLen=%d: segmented round trip failed", tc.runLen)
		}
	}
}

func TestSegmentationDoesNotMergeAcrossBoundary(t *testing.T) {
	// A long run of 7s followed by a short run of 7s (same value, but the
	// encoder sees them as one logical run anyway) — ensure segmentation
	// only splits, never drops or merges with a *different* value.
	in := append(repeat(3, MaxRunCount+2), repeat(4, 3)...)
	got := Encode(in)
	want := []Run{{3, MaxRunCount}, {3, 2}, {4, 3}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if dec := Decode(got); !reflect.DeepEqual(dec, in) {
		t.Fatal("round trip failed")
	}
}

func TestRoundTripRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 200; trial++ {
		n := rng.Intn(2000)
		in := make([]int64, n)
		for i := range in {
			// Small value domain forces many runs and merges.
			in[i] = int64(rng.Intn(8) - 4)
		}
		if got := Decode(Encode(in)); !reflect.DeepEqual(in, got) {
			t.Fatalf("trial %d: random round trip mismatch (n=%d)", trial, n)
		}
	}
}
