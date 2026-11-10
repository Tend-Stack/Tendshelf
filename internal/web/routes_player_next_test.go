package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Up next (Q1): POST /hx/player/next, the autoplay endpoint.

func TestPlayerNext(t *testing.T) {
	e := syncSeed(t)
	ownerTok := e.a.tokenFor(t, e.ck[sweepOwner])
	memberTok := e.a.tokenFor(t, e.ck[sweepMember])
	post := func(role int, tok string, form url.Values) *httptest.ResponseRecorder {
		if form == nil {
			form = url.Values{}
		}
		return e.a.do(http.MethodPost, "/hx/player/next", form, func(r *http.Request) {
			if tok != "" {
				r.Header.Set("X-CSRF-Token", tok)
			}
			r.Header.Set("HX-Request", "true")
		}, e.ck[role])
	}
	count := func(uid string) int {
		var n int
		if err := e.a.st.Reader().QueryRow(`SELECT count(*) FROM queue_items WHERE user_id = ?`, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// Nothing queued: 204, with or without an ended book.
	if rec := post(sweepOwner, ownerTok, nil); rec.Code != 204 || rec.Body.Len() != 0 {
		t.Errorf("empty: %d %q", rec.Code, rec.Body)
	}
	if rec := post(sweepOwner, ownerTok, url.Values{"ended": {e.book.ID}}); rec.Code != 204 {
		t.Errorf("empty with ended: %d", rec.Code)
	}
	// Bad ended: 400 plain text. No CSRF: 403.
	for _, bad := range []string{"", strings.Repeat("x", 65)} {
		rec := post(sweepOwner, ownerTok, url.Values{"ended": {bad}})
		if rec.Code != 400 || strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("ended %q: %d %s", bad, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	if rec := post(sweepOwner, "", nil); rec.Code != 403 {
		t.Errorf("no CSRF: %d", rec.Code)
	}

	// Queue three; the head plays and leaves the list; ended drops its row.
	for _, id := range []string{e.extra[0], e.extra[1], e.book.ID} {
		if rec := e.send(e.owner, http.MethodPost, "/api/v1/queue", queueBody(id, "")); rec.Code != 200 {
			t.Fatalf("queue %s: %d %s", id, rec.Code, rec.Body)
		}
	}
	rec := post(sweepOwner, ownerTok, url.Values{"ended": {e.extra[1]}})
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `id="player-data"`) || !strings.Contains(body, `data-book-id="`+e.extra[0]+`"`) ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("head: %d %s", rec.Code, body)
	}
	if got := e.queue(t, e.owner); got.Total != 1 || got.Items[0].Book.ID != e.book.ID {
		t.Errorf("after pop = %+v", got)
	}

	// The member holds a row for a book they cannot open; it is skipped and kept, and the next
	// playable book plays.
	if _, err := e.a.st.Writer().Exec(`INSERT INTO queue_items(user_id, item_id, book_id, position, added_at) VALUES (?1, ?2, ?2, -9, 1)`,
		e.uid[sweepMember], e.hidden.ID); err != nil {
		t.Fatal(err)
	}
	if rec := post(sweepMember, memberTok, nil); rec.Code != 204 {
		t.Errorf("only a hidden book queued: %d %s", rec.Code, rec.Body)
	}
	if n := count(e.uid[sweepMember]); n != 1 {
		t.Errorf("hidden row dropped: %d rows", n)
	}
	if rec := e.send(e.member, http.MethodPost, "/api/v1/queue", queueBody(e.extra[0], "")); rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	rec = post(sweepMember, memberTok, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-book-id="`+e.extra[0]+`"`) {
		t.Errorf("member next: %d %s", rec.Code, rec.Body)
	}
	if n := count(e.uid[sweepMember]); n != 1 {
		t.Errorf("hidden row should remain: %d", n)
	}

	// Someone else's queue is never touched: the owner still holds the last book.
	if n := count(e.uid[sweepOwner]); n != 1 {
		t.Errorf("owner rows = %d", n)
	}
	// A refused book is neither listed nor popped, so nothing plays.
	if _, err := e.a.st.Writer().Exec(`UPDATE books SET refused_reason = 'drm' WHERE id = ?`, e.book.ID); err != nil {
		t.Fatal(err)
	}
	if rec := post(sweepOwner, ownerTok, nil); rec.Code != 204 {
		t.Errorf("refused head: %d %s", rec.Code, rec.Body)
	}
}
