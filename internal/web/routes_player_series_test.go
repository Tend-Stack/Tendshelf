package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestSeriesNext: POST /hx/player/next falls back to the next book of the ended book's series when
// Up next is empty and the person's "next in series" setting is on (the web player's half of the
// feature; the app has the same answer through /api/v1/queue/next).
func TestSeriesNext(t *testing.T) {
	e := syncSeed(t)
	first, hidden, last := e.a1Series(t)
	ctx := context.Background()
	tok := map[int]string{
		sweepOwner:  e.a.tokenFor(t, e.ck[sweepOwner]),
		sweepMember: e.a.tokenFor(t, e.ck[sweepMember]),
	}
	post := func(role int, ended string, csrf bool) (int, string) {
		rec := e.a.do(http.MethodPost, "/hx/player/next", url.Values{"ended": {ended}}, func(r *http.Request) {
			if csrf {
				r.Header.Set("X-CSRF-Token", tok[role])
			}
			r.Header.Set("HX-Request", "true")
		}, e.ck[role])
		return rec.Code, rec.Body.String()
	}
	setting := func(on bool) {
		t.Helper()
		s, err := e.a.srv.users.LoadSettings(ctx, e.uid[sweepOwner])
		if err != nil {
			t.Fatal(err)
		}
		s.NextInSeries = on
		if err := e.a.srv.users.SaveSettings(ctx, e.uid[sweepOwner], s); err != nil {
			t.Fatal(err)
		}
	}
	reset := func() {
		setting(true)
		for _, q := range []string{`DELETE FROM queue_items`, `UPDATE progress SET finished_at = NULL`} {
			if _, err := e.a.st.Writer().Exec(q); err != nil {
				t.Fatal(err)
			}
		}
	}

	cases := []struct {
		name   string
		setup  func()
		role   int
		ended  string
		want   int
		bookID string // the book whose player comes back; "" when nothing plays
	}{
		{"setting on plays the next book", func() {}, sweepOwner, first.ID, 200, hidden.ID},
		{"setting off", func() { setting(false) }, sweepOwner, first.ID, 204, ""},
		{"queue wins over series", func() {
			if rec := e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], "")); rec.Code != 200 {
				t.Fatalf("queue: %d", rec.Code)
			}
		}, sweepOwner, first.ID, 200, e.extra[0]},
		{"no right to the next book is skipped", func() {}, sweepMember, first.ID, 200, last.ID},
		{"finished next book", func() {
			if rec := e.send(e.owner, http.MethodPut, "/api/v1/progress/"+hidden.ID, putBody(900, 1000, "")); rec.Code != 200 {
				t.Fatalf("progress: %d", rec.Code)
			}
			if _, err := e.a.st.Writer().Exec(`UPDATE progress SET finished_at = 1 WHERE book_id = ?`, hidden.ID); err != nil {
				t.Fatal(err)
			}
		}, sweepOwner, first.ID, 204, ""},
		{"last in series", func() {}, sweepOwner, last.ID, 204, ""},
		{"no series", func() {}, sweepOwner, e.extra[1], 204, ""},
		{"hidden ended book gives the member nothing", func() {}, sweepMember, hidden.ID, 204, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reset()
			c.setup()
			code, body := post(c.role, c.ended, true)
			if code != c.want {
				t.Fatalf("status %d, want %d: %.200s", code, c.want, body)
			}
			if c.want == 204 && body != "" {
				t.Errorf("204 with body %q", body)
			}
			if c.bookID != "" && !strings.Contains(body, c.bookID) {
				t.Errorf("fragment is not the player of %s", c.bookID)
			}
		})
	}

	t.Run("forbidden without the CSRF token", func(t *testing.T) {
		reset()
		if code, _ := post(sweepOwner, first.ID, false); code != 403 {
			t.Errorf("no CSRF: %d", code)
		}
	})
}
