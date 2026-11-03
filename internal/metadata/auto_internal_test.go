package metadata

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

func TestBulkPickAuto(t *testing.T) {
	cand := func(asin string, exact, confident bool) Candidate {
		return Candidate{Provider: "audnexus", Exact: exact, Confident: confident, Details: Details{Title: "T " + asin, ASIN: asin}}
	}
	full := online.Query{Title: "Dune", Author: "Frank Herbert"}
	cases := []struct {
		name  string
		q     online.Query
		cands []Candidate
		want  string // ASIN, or "" for none
	}{
		{"no candidates", full, nil, ""},
		{"exact applies even without an author", online.Query{Title: "Dune", ASIN: "A1"}, []Candidate{cand("A1", true, false)}, "A1"},
		{"one confident edition", full, []Candidate{cand("A1", false, true), cand("A2", false, false)}, "A1"},
		{"two confident editions need a look", full, []Candidate{cand("A1", false, true), cand("A2", false, true)}, ""},
		{"the same edition twice is one", full, []Candidate{cand("A1", false, true), cand("A1", false, true)}, "A1"},
		{"nothing confident", full, []Candidate{cand("A1", false, false)}, ""},
		{"no author in the query", online.Query{Title: "Dune"}, []Candidate{cand("A1", false, true)}, ""},
		{"no title in the query", online.Query{Author: "Frank Herbert"}, []Candidate{cand("A1", false, true)}, ""},
		{"two different exact books need a look", online.Query{ISBN: "9"}, []Candidate{cand("A1", true, false), cand("A2", true, false)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickAuto(tc.q, tc.cands)
			if tc.want == "" {
				if ok {
					t.Fatalf("picked %q", got.Details.ASIN)
				}
				return
			}
			if !ok || got.Details.ASIN != tc.want {
				t.Fatalf("got %q %v, want %q", got.Details.ASIN, ok, tc.want)
			}
		})
	}
}
