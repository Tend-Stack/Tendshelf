package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

var rstSeq int

// rstRead records a finished reading session for a person: it ended at `end` after `mins` minutes and `chars` characters.
func rstRead(t *testing.T, w *listeningWorld, user, book string, end time.Time, mins, chars int64) {
	t.Helper()
	rstSeq++
	ms := mins * 60_000
	if _, err := w.a.st.Writer().Exec(`INSERT INTO reading_sessions(id, user_id, book_id, device_id, started_at, ended_at, chars_read, active_ms)
VALUES (?, ?, ?, 'dev', ?, ?, ?, ?)`, fmt.Sprintf("rds_w%d", rstSeq), user, book, end.UnixMilli()-ms, end.UnixMilli(), chars, ms); err != nil {
		t.Fatal(err)
	}
}

// rstFinish marks a book finished in the person's text place.
func rstFinish(t *testing.T, w *listeningWorld, user, book string, at time.Time) {
	t.Helper()
	if _, err := w.a.st.Writer().Exec(`INSERT INTO reading_positions(user_id, book_id, chapter_idx, char_pos, percent_bp, finished_at,
client_updated_at, server_updated_at, device_id) VALUES (?, ?, 1, 0, 10000, ?, ?, ?, 'dev')`, user, book, at.UnixMilli(), at.UnixMilli(), at.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func rstPost(t *testing.T, w *listeningWorld, ck *http.Cookie, htmx bool, value string) (int, string, string) {
	t.Helper()
	form := url.Values{"_csrf": {w.a.tokenFor(t, ck)}, "goal_books": {value}}
	rec := w.a.do(http.MethodPost, "/me/reading/goal", form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
	return rec.Code, rec.Header().Get("Location"), rec.Body.String()
}

func TestReadStatsPage(t *testing.T) {
	w := newListeningWorld(t)
	rstRead(t, w, w.ownerID, w.ownerBook, listenAt(11, 2), 60, 90_000)
	rstRead(t, w, w.ownerID, w.ownerBook, listenAt(11, 3), 60, 90_000)
	rstRead(t, w, w.ownerID, w.ownerBook, listenAt(10, 20), 30, 30_000)
	rstFinish(t, w, w.ownerID, w.ownerBook, listenAt(11, 3))

	code, month := w.get("/me/reading", w.owner)
	if code != http.StatusOK {
		t.Fatalf("month = %d", code)
	}
	mustContain(t, "month", month, "Your reading", "Only you see these.", "November 2026", "Time reading", "2 h 0 m", "Days read", "2 of 7",
		"Words a minute", "About 300", "Books finished", "Time", "Yearly goal", "Finished", listenOwnerTitle,
		`href="/me/reading?range=week"`, `aria-label="Show October 2026"`, "Reading time by day",
		"2 h 0 m in November 2026, about 1 h 0 m on the days you read.", "Set a number of books to finish this year.",
		`name="goal_books"`, `name="_csrf"`)
	mustNotContain(t, "month", month, "Your listening", "style=", "Time of day")

	for _, c := range []struct{ path, want string }{
		{"/me/reading?range=week", "1–7 Nov 2026"},
		{"/me/reading?range=year", "Reading time by month"},
		{"/me/reading?range=month&at=2026-10-15", "30 m in October 2026"},
		{"/me/reading?at=2030-01-01", "November 2026"},
	} {
		code, body := w.get(c.path, w.owner)
		if code != http.StatusOK {
			t.Errorf("GET %s = %d", c.path, code)
			continue
		}
		mustContain(t, c.path, body, c.want)
	}
	for _, p := range []string{"/me/reading?range=decade", "/me/reading?at=2026-13-01", "/me/reading?at=1999-12-31", "/me/reading?at=x"} {
		if code, _ := w.get(p, w.owner); code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, code)
		}
	}
	rec := w.a.do(http.MethodGet, "/me/reading", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("signed out = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Never read: the empty state. Read, but not this month: a line in place of the charts.
	_, none := w.get("/me/reading", w.member)
	mustContain(t, "never", none, "Nothing to show yet", "Read a book and your time will show here, by day.", `href="/libraries"`)
	rstRead(t, w, w.memberID, w.memBook, listenAt(9, 3), 30, 1000)
	_, quiet := w.get("/me/reading", w.member)
	mustContain(t, "quiet", quiet, "No reading in November 2026.", "ts-dash__range")
	mustNotContain(t, "quiet", quiet, "Yearly goal")
}

func TestReadStatsProfileLink(t *testing.T) {
	w := newListeningWorld(t)
	_, me := w.get("/me", w.owner)
	mustNotContain(t, "no reading yet", me, "/me/reading")
	rstRead(t, w, w.ownerID, w.ownerBook, listenAt(11, 3), 5, 500)
	_, me = w.get("/me", w.owner)
	mustContain(t, "reading", me, `<a class="label ts-dash__more" href="/me/reading">Your reading</a>`)
	_, other := w.get("/me", w.member)
	mustNotContain(t, "another person", other, "/me/reading")
}

func TestReadStatsLeak(t *testing.T) {
	w := newListeningWorld(t)
	rstRead(t, w, w.ownerID, w.ownerBook, listenAt(11, 3), 120, 100_000)
	rstFinish(t, w, w.ownerID, w.ownerBook, listenAt(11, 3))
	rstRead(t, w, w.memberID, w.memBook, listenAt(11, 4), 45, 40_000)
	rstFinish(t, w, w.memberID, w.memBook, listenAt(11, 4))

	// An admin sees their own reading only, whatever the request says.
	_, o := w.get("/me/reading?user="+w.memberID+"&person="+w.memberID, w.owner)
	mustContain(t, "owner", o, "2 h 0 m in November 2026", listenOwnerTitle)
	mustNotContain(t, "owner", o, listenMemberTitle, "45 m in")
	_, m := w.get("/me/reading", w.member)
	mustContain(t, "member", m, "45 m in November 2026", listenMemberTitle)
	mustNotContain(t, "member", m, listenOwnerTitle, "2 h 0 m")

	// A revoked book's title is gone; the time and the count stay.
	if _, err := w.a.st.Writer().Exec(`DELETE FROM grants WHERE user_id = ?`, w.memberID); err != nil {
		t.Fatal(err)
	}
	code, body := w.get("/me/reading", w.member)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	mustContain(t, "revoked", body, "45 m in November 2026", "Books finished")
	mustNotContain(t, "revoked", body, listenMemberTitle, listenOwnerTitle)
	if !strings.Contains(body, `<dd class="time ts-dash__num">1</dd>`) {
		t.Error("the finished count of a revoked book is lost")
	}
}

func TestReadStatsGoal(t *testing.T) {
	w := newListeningWorld(t)
	rstRead(t, w, w.ownerID, w.ownerBook, listenAt(11, 3), 30, 30_000)
	rstFinish(t, w, w.ownerID, w.ownerBook, listenAt(11, 3))
	ctx := context.Background()
	for _, v := range []string{"0", "1", "365"} {
		code, loc, _ := rstPost(t, w, w.owner, false, v)
		if code != http.StatusSeeOther || loc != "/me/reading" {
			t.Errorf("goal %s = %d %q, want 303 to the page", v, code, loc)
		}
		g, err := w.a.srv.users.LoadReadingGoal(ctx, w.ownerID)
		if err != nil || fmt.Sprint(g.Books) != v {
			t.Errorf("stored goal = %+v, %v; want %s", g, err, v)
		}
	}
	// 365 is stored; junk is 422 and leaves it alone, as a page and as an htmx toast.
	for _, v := range []string{"366", "-1", "abc", ""} {
		code, _, body := rstPost(t, w, w.owner, false, v)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "Choose a whole number from 0 to 365.") {
			t.Errorf("goal %q = %d", v, code)
		}
		code, _, body = rstPost(t, w, w.owner, true, v)
		if code != http.StatusUnprocessableEntity || strings.Contains(body, "<html") {
			t.Errorf("htmx goal %q = %d %q", v, code, body)
		}
	}
	if g, _ := w.a.srv.users.LoadReadingGoal(ctx, w.ownerID); g.Books != 365 {
		t.Errorf("goal after junk = %d, want 365", g.Books)
	}
	// The page shows the sentence and the meter; another person has no goal.
	rstPost(t, w, w.owner, false, "4")
	_, page := w.get("/me/reading", w.owner)
	mustContain(t, "goal", page, "1 of 4 books this year", `aria-label="1 of 4 books"`, `value="4"`, `width="25"`)
	if g, _ := w.a.srv.users.LoadReadingGoal(ctx, w.memberID); g.Books != 0 {
		t.Errorf("member goal = %d", g.Books)
	}
	// CSRF: without the token the post is refused and nothing changes.
	rec := w.a.do(http.MethodPost, "/me/reading/goal", url.Values{"goal_books": {"9"}}, nil, w.owner)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no token = %d, want 403", rec.Code)
	}
	if g, _ := w.a.srv.users.LoadReadingGoal(ctx, w.ownerID); g.Books != 4 {
		t.Errorf("goal after a refused post = %d", g.Books)
	}
	// Signed out is a redirect to sign in.
	rec = w.a.do(http.MethodPost, "/me/reading/goal", url.Values{"goal_books": {"9"}}, nil)
	if rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/me/reading" {
		t.Error("a signed-out post was accepted")
	}
}
