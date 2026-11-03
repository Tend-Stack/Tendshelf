package online

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestOnlineAutoPace(t *testing.T) {
	type step struct {
		o      Outcome
		jitter float64
		want   Wait
	}
	trouble := func(d time.Duration, reason string) step {
		return step{o: OutcomeTrouble, want: Wait{For: d, Reason: reason}}
	}
	cases := []struct {
		name  string
		steps []step
	}{
		{"sent paces with jitter", []step{{OutcomeSent, 0.5, Wait{For: BulkGap + BulkJitter/2, Reason: "pace"}}}},
		{"cached waits for nothing", []step{{OutcomeCached, 0.9, Wait{}}}},
		{"backoff doubles", []step{trouble(30*time.Second, "backoff"), trouble(60*time.Second, "backoff"), trouble(120*time.Second, "backoff")}},
		{"a good book resets the run", []step{trouble(30*time.Second, "backoff"), {OutcomeSent, 0, Wait{For: BulkGap, Reason: "pace"}}, trouble(30*time.Second, "backoff")}},
		{"cached resets the run", []step{trouble(30*time.Second, "backoff"), {OutcomeCached, 0, Wait{}}, trouble(30*time.Second, "backoff")}},
		{"breaker pauses 30m then doubles to the cap and the sixth trip stops", func() []step {
			var s []step
			for _, pause := range []time.Duration{30 * time.Minute, 60 * time.Minute, 120 * time.Minute, 240 * time.Minute, 240 * time.Minute} {
				s = append(s, trouble(30*time.Second, "backoff"), trouble(60*time.Second, "backoff"), trouble(120*time.Second, "backoff"), trouble(pause, "pause"))
			}
			s = append(s, trouble(30*time.Second, "backoff"), trouble(60*time.Second, "backoff"), trouble(120*time.Second, "backoff"),
				step{o: OutcomeTrouble, want: Wait{Stop: true}})
			return s
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p Pace
			for i, s := range tc.steps {
				if got := p.Next(s.o, s.jitter); got != s.want {
					t.Fatalf("step %d: got %+v want %+v", i, got, s.want)
				}
			}
		})
	}
	if shiftCap(BackoffBase, 10, BackoffCap) != BackoffCap {
		t.Error("backoff is capped")
	}
}

func TestOnlineAutoNextDay(t *testing.T) {
	now := time.Date(2026, 11, 3, 23, 59, 30, 0, time.UTC)
	if got, want := NextDay(now, 0.5), time.Date(2026, 11, 4, 0, 5, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("got %v want %v", got, want)
	}
	if got, want := NextDay(time.Date(2026, 12, 31, 10, 0, 0, 0, time.UTC), 0), time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("year end: got %v", got)
	}
}

func TestOnlineAutoTroubled(t *testing.T) {
	for code, want := range map[string]bool{"busy": true, "server": true, "offline": true, "bad_answer": true, "too_large": true, "not_found": false, "": false} {
		if Troubled(code) != want {
			t.Errorf("%q", code)
		}
	}
}

func TestOnlineAutoConfident(t *testing.T) {
	ann := []Person{{Name: "Ann Author"}}
	cases := []struct {
		name          string
		b             Book
		title, author string
		want          bool
	}{
		{"equal title and author", Book{Title: "The Test Match", Authors: ann}, "The Test Match", "Ann Author", true},
		{"sequel is not the book", Book{Title: "Dune Messiah", Authors: ann}, "Dune", "Ann Author", false},
		{"accents folded", Book{Title: "Café Noir", Authors: []Person{{Name: "Zoë Author"}}}, "Cafe Noir", "Zoe Author", true},
		{"title plus subtitle", Book{Title: "Dune", Subtitle: "Book One", Authors: ann}, "Dune: Book One", "Ann Author", true},
		{"blank author", Book{Title: "Dune", Authors: ann}, "Dune", "", false},
		{"blank title", Book{Title: "Dune", Authors: ann}, "", "Ann Author", false},
		{"author miss", Book{Title: "Dune", Authors: ann}, "Dune", "Bob Other", false},
		{"one of several authors", Book{Title: "Dune", Authors: []Person{{Name: "Bob"}, {Name: "Ann Author"}}}, "Dune", "Ann Author & Cy", true},
	}
	for _, tc := range cases {
		if got := Confident(tc.b, tc.title, tc.author); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
}

func TestOnlineAutoSent(t *testing.T) {
	e := newEnv(t)
	q := Query{Title: "The Test Match", Author: "Ann Author"}
	res, err := e.c.Lookup(context.Background(), q, []string{"audnexus"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Steps {
		if s.Sent != 1 || s.Cached {
			t.Errorf("fresh step: %+v", s)
		}
	}
	if got := res.SentBy()["audnexus"]; got != len(res.Steps) || got == 0 {
		t.Errorf("SentBy %d for %d steps", got, len(res.Steps))
	}
	res, err = e.c.Lookup(context.Background(), q, []string{"audnexus"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res.Steps {
		if s.Sent != 0 || !s.Cached {
			t.Errorf("repeated step: %+v", s)
		}
	}
	if len(res.SentBy()) != 0 {
		t.Errorf("SentBy %v", res.SentBy())
	}
}

func TestOnlineAutoLimit(t *testing.T) {
	e := newEnv(t)
	var items []string
	for i := 0; i < 10; i++ {
		items = append(items, fmt.Sprintf(`{"asin":"B0TEST%04d"}`, i))
	}
	body := []byte(`{"products":[` + strings.Join(items, ",") + `]}`)
	e.net.routes = append([]route{{"https://api.audible.com/1.0/catalog/products", func(int) reply { return reply{body: body} }}}, e.net.routes...)
	q := Query{Title: "Anything", Author: "Ann Author"}
	if _, err := e.c.LookupWith(context.Background(), q, []string{"audnexus"}, LookupOptions{MaxAudnexusBooks: 3}); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, u := range e.net.urls {
		if strings.HasPrefix(u, "https://api.audnex.us/books/") {
			n++
		}
	}
	if n != 3 {
		t.Errorf("%d audnex.us requests, want 3", n)
	}
}
