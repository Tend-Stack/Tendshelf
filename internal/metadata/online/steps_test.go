package online

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func stepLine(s Step) string {
	return fmt.Sprintf("%s|%s|%s|%s|%d|%s|%v", s.Label, s.Region, s.State, s.Query, s.Count, s.Code, s.Cached)
}

func stepLines(steps []Step) []string {
	var out []string
	for _, s := range steps {
		out = append(out, stepLine(s))
	}
	return out
}

func wantSteps(t *testing.T, got []Step, want ...string) {
	t.Helper()
	g := stepLines(got)
	if strings.Join(g, "\n") != strings.Join(want, "\n") {
		t.Errorf("steps:\n%s\nwant:\n%s", strings.Join(g, "\n"), strings.Join(want, "\n"))
	}
}

func TestLookupProgressASINRegions(t *testing.T) {
	e := newEnv(t)
	res, err := e.c.Lookup(context.Background(), Query{Title: "x", ASIN: "B0NOWHERE1"}, []string{"audnexus"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != len(audnexusRegions) {
		t.Fatalf("%d steps", len(res.Steps))
	}
	wantSteps(t, res.Steps[:2],
		"Audnexus|US|none|B0NOWHERE1|0||false",
		"Audnexus|FR|none|B0NOWHERE1|0||false")
	// A found ASIN stops at the store that has it.
	e = newEnv(t)
	res, _ = e.c.Lookup(context.Background(), Query{ASIN: "B0TESTBOOK"}, []string{"audnexus"})
	wantSteps(t, res.Steps, "Audnexus|US|done|B0TESTBOOK|1||false")
}

func TestLookupProgressSearchPath(t *testing.T) {
	e := newEnv(t)
	var seen [][]Step
	res, err := e.c.LookupSteps(context.Background(), Query{Title: "The Test Match", Author: "Ann Author"}, []string{"audnexus"},
		func(s []Step) { seen = append(seen, s) })
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) < 2 {
		t.Fatalf("steps: %v", stepLines(res.Steps))
	}
	cat := res.Steps[0]
	if cat.Label != "Audible catalogue" || cat.State != StepDone || cat.Count < 1 || cat.Query != `"The Test Match" by Ann Author` {
		t.Errorf("catalogue step: %+v", cat)
	}
	for _, s := range res.Steps[1:] {
		if s.Label != "Audnexus" || s.Region != "US" || s.Query == "" || strings.Contains(s.Query, "Ann") {
			t.Errorf("book step: %+v", s)
		}
	}
	// Called at every boundary: the first call is the plan, the catalogue shows as running before it ends.
	if len(seen) < 3 || seen[0][0].State != StepWaiting || seen[1][0].State != StepRunning || seen[2][0].State == StepRunning {
		t.Errorf("onStep calls: %d", len(seen))
	}
}

func TestLookupProgressMorePlaces(t *testing.T) {
	e := newEnv(t)
	var first []Step
	res, err := e.c.LookupSteps(context.Background(), Query{Title: "The Test Match", Author: "Ann Author"}, ChosenProviders([]string{"openlibrary", "itunes"}),
		func(s []Step) {
			if first == nil {
				first = s
			}
		})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || first[0].State != StepWaiting || first[2].Label != "Apple Books" {
		t.Errorf("plan: %v", stepLines(first))
	}
	var labels []string
	for _, s := range res.Steps {
		if s.State != StepDone && s.State != StepNone {
			t.Errorf("unfinished step: %+v", s)
		}
		labels = append(labels, s.Label)
	}
	// Audible steps come first, the other two follow in order, each placed after the one before.
	n := len(labels)
	if n < 4 || strings.Join(labels[n-2:], ",") != "Open Library,Apple Books" || labels[0] != "Audible catalogue" {
		t.Errorf("order: %v", labels)
	}
	for _, s := range res.Steps[n-2:] {
		if s.Query != `"The Test Match" by Ann Author` || s.Count < 1 {
			t.Errorf("step: %+v", s)
		}
	}
}

func TestLookupProgressCached(t *testing.T) {
	e := newEnv(t)
	q := Query{Title: "The Test Match", Author: "Ann Author"}
	if _, err := e.c.Lookup(context.Background(), q, []string{"audnexus"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.c.Lookup(context.Background(), q, []string{"audnexus"})
	if err != nil || len(res.Steps) == 0 {
		t.Fatalf("%v %d", err, len(res.Steps))
	}
	for _, s := range res.Steps {
		if !s.Cached || s.State != StepDone {
			t.Errorf("a repeated lookup is answered from the saved copy: %+v", s)
		}
	}
}

func TestLookupProgressProviderFailure(t *testing.T) {
	e := newEnv(t)
	e.net.routes = append([]route{{"https://openlibrary.org/", func(int) reply { return reply{status: 429} }}}, e.net.routes...)
	res, err := e.c.Lookup(context.Background(), Query{Title: "The Test Match"}, []string{"openlibrary", "itunes"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Errors["openlibrary"] != "busy" {
		t.Fatalf("errors: %v", res.Errors)
	}
	wantSteps(t, res.Steps,
		`Open Library||failed|"The Test Match"|0|busy|false`,
		`Apple Books||done|"The Test Match"|1||false`)
	// One place failing never hides another's results.
	if len(res.Candidates) == 0 || res.Candidates[0].Provider != "itunes" {
		t.Errorf("candidates: %+v", res.Candidates)
	}
	// A 5xx is its own code, and the other place still answers.
	e = newEnv(t)
	e.net.routes = append([]route{{"https://itunes.apple.com/", func(int) reply { return reply{status: 503} }}}, e.net.routes...)
	res, _ = e.c.Lookup(context.Background(), Query{Title: "The Test Match"}, []string{"openlibrary", "itunes"})
	if res.Errors["itunes"] != "server" || len(res.Candidates) == 0 || res.Candidates[0].Provider != "openlibrary" {
		t.Errorf("5xx: %v %+v", res.Errors, res.Candidates)
	}
	// A network failure is a failed step, in the same words as the error.
	e = newEnv(t)
	e.net.fail = errors.New("no route")
	res, _ = e.c.Lookup(context.Background(), Query{ISBN: "9780000000002"}, []string{"openlibrary"})
	wantSteps(t, res.Steps, "Open Library||failed|9780000000002|0|offline|false")
}

func TestLookupProgressNothingFound(t *testing.T) {
	e := newEnv(t)
	e.net.routes = nil
	res, err := e.c.Lookup(context.Background(), Query{Title: "Nothing Here"}, []string{"audnexus", "openlibrary"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Candidates) != 0 {
		t.Fatal("candidates")
	}
	wantSteps(t, res.Steps,
		`Audible catalogue||none|"Nothing Here"|0||false`,
		`Open Library||none|"Nothing Here"|0||false`)
}

func TestLookupProgressCap(t *testing.T) {
	r := &recorder{cur: -1}
	for i := 0; i < MaxSteps+5; i++ {
		r.begin("audnexus", "Audnexus", "US", "B0X")
		r.end(1, nil)
	}
	if len(r.steps) != MaxSteps {
		t.Errorf("%d steps kept", len(r.steps))
	}
}
