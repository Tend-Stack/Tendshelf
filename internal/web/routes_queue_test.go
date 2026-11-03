package web

import (
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Up next (Q2): the list fragment, the page and the three changes, as htmx and as plain forms.

func queueSend(t *testing.T, a *testApp, ck *http.Cookie, htmx bool, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if form.Get("_csrf") == "" {
		form.Set("_csrf", a.tokenFor(t, ck))
	}
	return a.do(http.MethodPost, path, form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

// queueIDs lists the book ids in an Up next fragment or page, in order.
func queueIDs(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<li id="item-([^"]+)"`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func queueApp(t *testing.T) (*testApp, []library.Book) {
	a, f := bookApp(t)
	return a, browseAddMany(t, a, f, 4)
}

func TestQueueAddHTMX(t *testing.T) {
	a, books := queueApp(t)
	ck := a.session
	for _, c := range []struct {
		id, where, toast string
		want             []string
	}{
		{books[0].ID, "end", queueAddedEnd, []string{books[0].ID}},
		{books[1].ID, "end", queueAddedEnd, []string{books[0].ID, books[1].ID}},
		{books[0].ID, "end", queueAlready, []string{books[0].ID, books[1].ID}},
		{books[2].ID, "next", queueAddedFront, []string{books[2].ID, books[0].ID, books[1].ID}},
	} {
		rec := queueSend(t, a, ck, true, "/queue", url.Values{"book_id": {c.id}, "where": {c.where}, "next": {"/books/" + c.id}})
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, html.EscapeString(c.toast)) {
			t.Fatalf("add %s %s: %d %s", c.id, c.where, rec.Code, body)
		}
		// The answer is the menu alone (closed, no hx-preserve, focus on the toggle) plus an OOB toast.
		if !strings.Contains(body, `id="book-queue-menu"`) || strings.Contains(body, "hx-preserve") || strings.Contains(body, " open") ||
			!strings.Contains(body, `<summary class="ts-btn ts-btn--primary ts-btn--lg ts-split__toggle" aria-label="Queue options" autofocus`) ||
			!strings.Contains(body, `hx-swap-oob="beforeend:#toasts"`) || strings.Contains(body, "<html") {
			t.Fatalf("answer shape: %s", body)
		}
		if got := queueIDs(browseGet(a, "/queue", false, ck).Body.String()); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Fatalf("after %s %s: %v, want %v", c.id, c.where, got, c.want)
		}
	}
}

func TestQueueAddPlain(t *testing.T) {
	a, books := queueApp(t)
	rec := queueSend(t, a, a.session, false, "/queue", url.Values{"book_id": {books[0].ID}, "where": {"end"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/books/"+books[0].ID {
		t.Fatalf("plain add: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = queueSend(t, a, a.session, false, "/queue", url.Values{"book_id": {books[1].ID}, "where": {"end"}, "next": {"//evil.example"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("unsafe next: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = queueSend(t, a, a.session, false, "/queue", url.Values{"book_id": {books[1].ID}, "where": {"sideways"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad where: %d", rec.Code)
	}
	if rec = queueSend(t, a, a.session, false, "/queue", url.Values{"book_id": {"bk_unknown"}, "where": {"end"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown book: %d", rec.Code)
	}
}

func TestQueueAddErrors(t *testing.T) {
	a, f := bookApp(t)
	good := browseAddMany(t, a, f, 1)[0]
	refused := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Locked", Refused: library.RefusedDRM})
	for _, c := range []struct {
		name  string
		form  url.Values
		code  int
		toast string
	}{
		{"bad where", url.Values{"book_id": {good.ID}, "where": {"x"}}, 400, queueErrWhere},
		{"refused", url.Values{"book_id": {refused.ID}, "where": {"end"}}, 409, queueErrUnplayable},
		{"unknown", url.Values{"book_id": {"bk_unknown"}, "where": {"end"}}, 404, queueErrGone},
	} {
		rec := queueSend(t, a, a.session, true, "/queue", c.form)
		body := rec.Body.String()
		// An error answer re-renders the same target (the menu) and carries the toast, so the swap is safe.
		if rec.Code != c.code || !strings.Contains(body, html.EscapeString(c.toast)) || !strings.Contains(body, `id="book-queue-menu"`) {
			t.Errorf("%s: %d %s", c.name, rec.Code, body)
		}
	}
	// Full: 100 rows, the 101st is refused with the menu and a toast; re-adding a queued book still works.
	for range library.QueueMax {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Filler"})
		if _, err := a.lib.QueueAdd(t.Context(), ownerID(t, a), b.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	rec := queueSend(t, a, a.session, true, "/queue", url.Values{"book_id": {good.ID}, "where": {"end"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), queueErrFull) || !strings.Contains(rec.Body.String(), `id="book-queue-menu"`) {
		t.Errorf("full: %d %s", rec.Code, rec.Body)
	}
}

func ownerID(t *testing.T, a *testApp) string {
	t.Helper()
	var id string
	if err := a.st.Reader().QueryRow(`SELECT id FROM users ORDER BY created_at LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestQueueMoveAndRemoveHTMX(t *testing.T) {
	a, books := queueApp(t)
	ck := a.session
	for _, b := range books[:3] {
		queueSend(t, a, ck, true, "/queue", url.Values{"book_id": {b.ID}, "where": {"end"}})
	}
	ids := func(body string) string { return strings.Join(queueIDs(body), ",") }
	b0, b1, b2 := books[0].ID, books[1].ID, books[2].ID

	rec := queueSend(t, a, ck, true, "/queue/"+b0+"/move", url.Values{"dir": {"down"}, "in": {"page"}})
	body := rec.Body.String()
	if rec.Code != 200 || ids(body) != b1+","+b0+","+b2 || !strings.Contains(body, `id="up-next"`) || strings.Contains(body, "now-panel-queue-count") {
		t.Fatalf("move down: %d %s", rec.Code, body)
	}
	// The moved row keeps the focus on the same arrow.
	if !regexp.MustCompile(`name="dir" value="down">\s*<input[^>]*>\s*<input[^>]*>\s*<button[^>]* autofocus`).MatchString(body) {
		t.Errorf("focus not on the moved row's down arrow: %s", body)
	}
	// In the panel the answer carries the tab's count out of band, and the list has the panel's id.
	rec = queueSend(t, a, ck, true, "/queue/"+b0+"/move", url.Values{"dir": {"down"}, "in": {"panel"}})
	body = rec.Body.String()
	if rec.Code != 200 || ids(body) != b1+","+b2+","+b0 || !strings.Contains(body, `id="now-up-next"`) ||
		!strings.Contains(body, `<span id="now-panel-queue-count" hx-swap-oob="outerHTML">3</span>`) {
		t.Fatalf("panel move: %d %s", rec.Code, body)
	}
	// At the end the move is a no-op and the focus goes to the other arrow (up).
	rec = queueSend(t, a, ck, true, "/queue/"+b0+"/move", url.Values{"dir": {"down"}, "in": {"page"}})
	if rec.Code != 200 || ids(rec.Body.String()) != b1+","+b2+","+b0 || !regexp.MustCompile(`name="dir" value="up">\s*<input[^>]*>\s*<input[^>]*>\s*<button[^>]* autofocus`).MatchString(rec.Body.String()) {
		t.Errorf("move at the end: %d %s", rec.Code, rec.Body)
	}
	// Remove: the focus moves to the next row's remove button, then to the previous one, then the empty state.
	rec = queueSend(t, a, ck, true, "/queue/"+b2+"/delete", url.Values{"in": {"page"}})
	body = rec.Body.String()
	if rec.Code != 200 || ids(body) != b1+","+b0 || strings.Count(body, " autofocus") != 1 || !strings.Contains(body, `aria-label="Remove `+books[0].Title+` from Up next" autofocus`) {
		t.Fatalf("remove middle: %d %s", rec.Code, body)
	}
	rec = queueSend(t, a, ck, true, "/queue/"+b0+"/delete", url.Values{"in": {"page"}})
	if !strings.Contains(rec.Body.String(), `aria-label="Remove `+books[1].Title+` from Up next" autofocus`) {
		t.Errorf("remove last: %s", rec.Body)
	}
	rec = queueSend(t, a, ck, true, "/queue/"+b1+"/delete", url.Values{"in": {"page"}})
	if rec.Code != 200 || len(queueIDs(rec.Body.String())) != 0 || !strings.Contains(rec.Body.String(), `class="ts-np__empty" tabindex="-1" autofocus`) {
		t.Errorf("remove all: %d %s", rec.Code, rec.Body)
	}
	// Not queued: 404 with the fresh list and a toast; a bad direction: 400 the same way.
	rec = queueSend(t, a, ck, true, "/queue/"+b1+"/delete", url.Values{"in": {"page"}})
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), `id="up-next"`) || !strings.Contains(rec.Body.String(), html.EscapeString(queueErrGone)) {
		t.Errorf("remove missing: %d %s", rec.Code, rec.Body)
	}
	rec = queueSend(t, a, ck, true, "/queue/"+b1+"/move", url.Values{"dir": {"sideways"}, "in": {"page"}})
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), queueErrDir) || !strings.Contains(rec.Body.String(), `id="up-next"`) {
		t.Errorf("bad dir: %d %s", rec.Code, rec.Body)
	}
}

func TestQueueMovePlain(t *testing.T) {
	a, books := queueApp(t)
	for _, b := range books[:2] {
		queueSend(t, a, a.session, false, "/queue", url.Values{"book_id": {b.ID}, "where": {"end"}})
	}
	rec := queueSend(t, a, a.session, false, "/queue/"+books[0].ID+"/move", url.Values{"dir": {"down"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/queue#item-"+books[0].ID {
		t.Fatalf("plain move: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if got := queueIDs(browseGet(a, "/queue", false, nil).Body.String()); strings.Join(got, ",") != books[1].ID+","+books[0].ID {
		t.Errorf("order %v", got)
	}
	if rec = queueSend(t, a, a.session, false, "/queue/"+books[0].ID+"/move", url.Values{"dir": {"x"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad dir: %d", rec.Code)
	}
	rec = queueSend(t, a, a.session, false, "/queue/"+books[0].ID+"/delete", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/queue#item-"+books[0].ID {
		t.Errorf("plain delete: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = queueSend(t, a, a.session, false, "/queue/"+books[0].ID+"/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("delete again: %d", rec.Code)
	}
}

func TestQueueListAndPage(t *testing.T) {
	a, books := queueApp(t)
	queueSend(t, a, a.session, true, "/queue", url.Values{"book_id": {books[0].ID}, "where": {"end"}})
	page := browseGet(a, "/queue", false, nil)
	body := page.Body.String()
	if page.Code != 200 || !strings.Contains(body, "<html") || !strings.Contains(body, `<h1 class="title-1">Up next</h1>`) || !strings.Contains(body, `id="up-next"`) {
		t.Fatalf("page: %d", page.Code)
	}
	// The fragment is the list alone, with the id of its place; only the panel's has the count.
	frag := browseGet(a, "/hx/queue?in=panel", true, nil)
	fb := frag.Body.String()
	if frag.Code != 200 || strings.Contains(fb, "<html") || !strings.Contains(fb, `id="now-up-next"`) ||
		!strings.Contains(fb, `<span id="now-panel-queue-count" hx-swap-oob="outerHTML">1</span>`) {
		t.Fatalf("panel fragment: %d %s", frag.Code, fb)
	}
	if fb = browseGet(a, "/hx/queue", true, nil).Body.String(); !strings.Contains(fb, `id="up-next"`) || strings.Contains(fb, "now-panel-queue-count") {
		t.Errorf("page fragment: %s", fb)
	}
	// Empty: the words, no list.
	queueSend(t, a, a.session, true, "/queue/"+books[0].ID+"/delete", url.Values{"in": {"page"}})
	if fb = browseGet(a, "/hx/queue", true, nil).Body.String(); !strings.Contains(fb, queueEmpty) || strings.Contains(fb, "<ol") {
		t.Errorf("empty: %s", fb)
	}
}

// A hidden book never shows, and another person's queue is theirs alone.
func TestQueueForbidden(t *testing.T) {
	a, books := queueApp(t)
	owner := a.session
	queueSend(t, a, owner, true, "/queue", url.Values{"book_id": {books[0].ID}, "where": {"end"}})
	member := a.as(t, users.RoleMember)
	guest := a.as(t, users.RoleGuest)
	for who, ck := range map[string]*http.Cookie{"member": member, "guest": guest} {
		for _, p := range []string{"/queue", "/hx/queue?in=panel"} {
			rec := browseGet(a, p, strings.HasPrefix(p, "/hx"), ck)
			if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), books[0].ID) {
				t.Errorf("%s GET %s: %d (their list must be empty)", who, p, rec.Code)
			}
		}
		// Without a grant the book is unknown: add, move and remove are 404 and change nothing.
		for _, c := range []struct{ path, key string }{
			{"/queue", "add"}, {"/queue/" + books[0].ID + "/move", "move"}, {"/queue/" + books[0].ID + "/delete", "del"},
		} {
			rec := queueSend(t, a, ck, false, c.path, url.Values{"book_id": {books[0].ID}, "where": {"end"}, "dir": {"up"}})
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d, want 404", who, c.key, rec.Code)
			}
			rec = queueSend(t, a, ck, true, c.path, url.Values{"book_id": {books[0].ID}, "where": {"end"}, "dir": {"up"}})
			if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), html.EscapeString(queueErrGone)) {
				t.Errorf("%s %s htmx: %d %s", who, c.key, rec.Code, rec.Body)
			}
		}
	}
	if got := queueIDs(browseGet(a, "/queue", false, owner).Body.String()); len(got) != 1 {
		t.Errorf("owner's queue = %v", got)
	}
	// No cookie: sign in first, and nothing changes.
	for _, p := range []string{"/queue", "/hx/queue"} {
		if rec := a.do(http.MethodGet, p, nil, nil); rec.Code != http.StatusSeeOther {
			t.Errorf("signed out GET %s: %d", p, rec.Code)
		}
	}
	// A change without the CSRF token is refused.
	rec := a.do(http.MethodPost, "/queue", url.Values{"book_id": {books[1].ID}, "where": {"end"}}, nil, owner)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF: %d", rec.Code)
	}
}
