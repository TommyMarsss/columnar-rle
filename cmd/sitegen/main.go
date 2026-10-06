// Command sitegen builds a single self-contained static HTML file
// (no server, no third-party libraries) that visualizes the RLE-encoded
// column, the block layout, and the per-block skip/expand decisions made
// by predicate pushdown for a set of demo queries.
//
// Usage: go run ./cmd/sitegen [-out rle_demo.html]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	columnar "github.com/TommyMarsss/columnar-rle"
)

type runJSON struct {
	Value int64  `json:"value"`
	Count uint32 `json:"count"`
}

type blockJSON struct {
	Runs []runJSON `json:"runs"`
	Rows int       `json:"rows"`
	Min  int64     `json:"min"`
	Max  int64     `json:"max"`
	Sum  int64     `json:"sum"`
}

type queryJSON struct {
	Label       string    `json:"label"`
	Lo          int64     `json:"lo"`
	Hi          int64     `json:"hi"`
	Verdicts    []int     `json:"verdicts"` // 0=SKIP 1=ALL 2=PARTIAL, per block
	PartialHits [][]int   `json:"partialHits"`
	Stats       statsJSON `json:"stats"`
	FilterCount int       `json:"filterCount"`
	AggCount    int       `json:"aggCount"`
	AggSum      int64     `json:"aggSum"`
	BruteCount  int       `json:"bruteCount"`
	BruteSum    int64     `json:"bruteSum"`
}

type statsJSON struct {
	Total    int `json:"total"`
	Skipped  int `json:"skipped"`
	AllMatch int `json:"allMatch"`
	Expanded int `json:"expanded"`
}

type pageData struct {
	BlockSize   int         `json:"blockSize"`
	MaxRunCount int         `json:"maxRunCount"`
	Values      []int64     `json:"values"`
	Blocks      []blockJSON `json:"blocks"`
	SegDemo     struct {
		RunLen int       `json:"runLen"`
		Runs   []runJSON `json:"runs"`
	} `json:"segDemo"`
	Queries []queryJSON `json:"queries"`
}

func main() {
	out := flag.String("out", "rle_demo.html", "output HTML file")
	flag.Parse()

	const bs = 16
	values := demoColumn()
	col := columnar.EncodeColumn(values, bs)

	data := pageData{BlockSize: bs, MaxRunCount: columnar.MaxRunCount, Values: values}
	for _, b := range col.Blocks {
		bj := blockJSON{Rows: b.Rows, Min: b.Min, Max: b.Max, Sum: b.Sum}
		for _, r := range b.Runs {
			bj.Runs = append(bj.Runs, runJSON{Value: r.Value, Count: r.Count})
		}
		data.Blocks = append(data.Blocks, bj)
	}

	// Segmentation demo: one run far longer than the count field allows.
	segLen := 2*columnar.MaxRunCount + 7
	segRuns := columnar.Encode(repeatVals(42, segLen))
	data.SegDemo.RunLen = segLen
	for _, r := range segRuns {
		data.SegDemo.Runs = append(data.SegDemo.Runs, runJSON{Value: r.Value, Count: r.Count})
	}

	preds := []struct {
		label string
		p     columnar.Predicate
	}{
		{"value = 5", columnar.Eq(5)},
		{"3 ≤ value ≤ 6", columnar.Between(3, 6)},
		{"60 ≤ value ≤ 70", columnar.Between(60, 70)},
		{"-100 ≤ value ≤ 100", columnar.Between(-100, 100)},
		{"value = 99", columnar.Eq(99)},
	}
	for _, q := range preds {
		fr := col.Filter(q.p)
		ag := col.Aggregate(q.p)
		bc, bsum := brute(values, q.p)
		qj := queryJSON{
			Label: q.label, Lo: q.p.Lo, Hi: q.p.Hi,
			FilterCount: len(fr.Values),
			AggCount:    ag.Count, AggSum: ag.Sum,
			BruteCount: bc, BruteSum: bsum,
			Stats: statsJSON{
				Total:    ag.Stats.BlocksTotal,
				Skipped:  ag.Stats.BlocksSkipped,
				AllMatch: ag.Stats.BlocksAllMatch,
				Expanded: ag.Stats.BlocksExpanded,
			},
		}
		rowBase := 0
		for _, b := range col.Blocks {
			v := columnar.EvalBlock(b.Min, b.Max, q.p)
			qj.Verdicts = append(qj.Verdicts, int(v))
			if v == columnar.VerdictPartial {
				var hits []int
				for i, val := range columnar.Decode(b.Runs) {
					if q.p.Match(val) {
						hits = append(hits, rowBase+i)
					}
				}
				qj.PartialHits = append(qj.PartialHits, hits)
			} else {
				qj.PartialHits = append(qj.PartialHits, nil)
			}
			rowBase += b.Rows
		}
		data.Queries = append(data.Queries, qj)
	}

	js, err := json.Marshal(data)
	if err != nil {
		log.Fatal(err)
	}
	html := strings.Replace(pageTemplate, "/*__DATA__*/", string(js), 1)
	if err := os.WriteFile(*out, []byte(html), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s (%d bytes)\n", *out, len(html))
}

func repeatVals(v int64, n int) []int64 {
	out := make([]int64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func brute(values []int64, p columnar.Predicate) (int, int64) {
	c := 0
	var s int64
	for _, v := range values {
		if p.Match(v) {
			c++
			s += v
		}
	}
	return c, s
}

// demoColumn builds 8 blocks of 16 rows with varied shapes so the demo shows
// every verdict: uniform blocks (ALL/SKIP), heterogeneous blocks (PARTIAL),
// and blocks disjoint from some queries.
func demoColumn() []int64 {
	var v []int64
	v = append(v, repeatVals(5, 16)...)  // block 0: uniform 5
	v = append(v, repeatVals(50, 16)...) // block 1: uniform 50
	for i := int64(1); i <= 16; i++ {    // block 2: 1..16
		v = append(v, i)
	}
	v = append(v, repeatVals(5, 16)...) // block 3: uniform 5
	for i := int64(90); i < 106; i++ {  // block 4: 90..105
		v = append(v, i)
	}
	v = append(v, repeatVals(-7, 16)...) // block 5: uniform -7
	for i := 0; i < 16; i++ {            // block 6: 3,4 alternating
		v = append(v, int64(3+i%2))
	}
	for i := int64(60); i < 76; i++ { // block 7: 60..75
		v = append(v, i)
	}
	return v
}
