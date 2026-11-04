package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// inboxPost sends a urlencoded form with the token and the extra fields.
func (l *textLab) inboxPost(t *testing.T, ck *http.Cookie, path string, fields map[string]string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := map[string][]string{"_csrf": {l.a.tokenFor(t, ck)}}
	for k, v := range fields {
		form[k] = []string{v}
	}
	return l.a.do(http.MethodPost, path, form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

func inboxHas(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}

func inboxLacks(t *testing.T, body string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(body, w) {
			t.Errorf("page has %q", w)
		}
	}
}

func inboxHold(t *testing.T, l *textLab, id string) (book, hold string) {
	t.Helper()
	if err := l.a.st.Reader().QueryRow(`SELECT COALESCE(book_id,''), COALESCE(hold,'') FROM ebook_files WHERE id = ?`, id).Scan(&book, &hold); err != nil {
		t.Fatal(err)
	}
	return book, hold
}

func TestEbookInboxList(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	if body := l.get(l.owner, "/admin/ebooks", false).Body.String(); !strings.Contains(body, "Every ebook is paired with an audiobook.") {
		t.Error("the empty inbox lacks its sentence")
	}
	match := l.textPairAdd(t, "inbox-match.epub", "Textword Lantern", "Imogen Textauthor", "")
	none := l.textPairAdd(t, "inbox-none.epub", "Zzyzx Almanac", "Nobody Known", "")
	kept := l.textPairAdd(t, "inbox-kept.epub", "Kept Almanac", "Nobody Known", "")
	l.addPlaced(t, "inbox-waiting.epub", "pending", "")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE state = 'pending'`); err != nil {
		t.Fatal(err)
	}
	if rec := l.inboxPost(t, l.owner, "/admin/ebooks/"+kept.ID+"/keep", nil, false); rec.Code != http.StatusSeeOther {
		t.Fatalf("keep: %d", rec.Code)
	}
	rec := l.get(l.owner, "/admin/ebooks", false)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("inbox: %d", rec.Code)
	}
	inboxHas(t, body, "Ebooks to pair", "These ebooks are not paired with an audiobook yet.",
		"inbox-"+match.ID, "inbox-"+none.ID, "Zzyzx Almanac", "same title and author", "Textword Lantern",
		`/admin/ebooks/`+match.ID+`/pair`, "Choose the audiobook", "/admin/ebooks/"+none.ID,
		"Kept on their own", "It stays here, off your shelf, until you pair it with an audiobook.", "Reading inbox-waiting.epub…")
	if i, j := strings.Index(body, "inbox-"+kept.ID), strings.Index(body, "Kept on their own"); i < j {
		t.Error("the kept file is above its section")
	}
	// The unmatched row offers no "Choose" form.
	if strings.Contains(body, "/admin/ebooks/"+none.ID+"/pair") || strings.Contains(body, "/admin/ebooks/"+kept.ID+"/pair") {
		t.Error("a row with no match offers Choose")
	}
	// The libraries page links the inbox with its count (kept and waiting-to-be-read files are not counted).
	inboxHas(t, l.get(l.owner, "/admin/libraries", false).Body.String(), "2 ebooks to pair", `href="/admin/ebooks"`)
}

func TestEbookInboxPick(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	match := l.textPairAdd(t, "pick-match.epub", "Textword Lantern", "Imogen Textauthor", "")
	none := l.textPairAdd(t, "pick-none.epub", "Zzyzx Almanac", "Nobody Known", "")
	body := l.get(l.owner, "/admin/ebooks/"+match.ID, false).Body.String()
	inboxHas(t, body, "Add an ebook", "Which audiobook is it?", "Close matches", "same title and author", "Textword Lantern",
		"Keep it as an ebook on its own", "Search your audiobooks")
	inboxLacks(t, body, "None of your")
	body = l.get(l.owner, "/admin/ebooks/"+none.ID, false).Body.String()
	inboxHas(t, body, "None of your 1 audiobooks has this title and author. It may be listed under a different name.", "ts-pair--nomatch")
	inboxLacks(t, body, "Close matches")
	// A search lists the audiobooks the person can see, each with Choose.
	body = l.get(l.owner, "/admin/ebooks/"+none.ID+"?q=Lantern", false).Body.String()
	inboxHas(t, body, "Audiobooks matching “Lantern”", "Textword Lantern", `name="book"`)
	body = l.get(l.owner, "/admin/ebooks/"+none.ID+"?q=Nonexistentword", false).Body.String()
	inboxHas(t, body, "No audiobook matches “Nonexistentword”.")
	// An ebook with a book, or one that is not ready, has no pick page.
	paired := l.addReady(t, "pick-paired.epub", "Paired One", 2)
	pending := l.addPlaced(t, "pick-pending.epub", "pending", "")
	for _, id := range []string{paired.ID, pending, "nope"} {
		if got := l.get(l.owner, "/admin/ebooks/"+id, false).Code; got != 404 {
			t.Errorf("pick %s: %d, want 404", id, got)
		}
	}
	// The filter applies to the audiobook count and the matches: a person with no grant sees no
	// titles (members get 404 on the page itself, see TestEbookInboxRoles).
}

func TestEbookInboxPair(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	a := l.textPairAdd(t, "pair-a.epub", "Zzyzx Almanac", "Nobody Known", "")
	b := l.textPairAdd(t, "pair-b.epub", "Yyx Almanac", "Nobody Known", "")
	rec := l.inboxPost(t, l.owner, "/admin/ebooks/"+a.ID+"/pair", map[string]string{"book": l.book.ID}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/ebooks" {
		t.Fatalf("pair: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if book, _ := inboxHold(t, l, a.ID); book != l.book.ID {
		t.Errorf("file is on %q", book)
	}
	// It is paired now: the inbox no longer lists it, and the pick page and a second pair are 404.
	if body := l.get(l.owner, "/admin/ebooks", false).Body.String(); strings.Contains(body, "inbox-"+a.ID) {
		t.Error("a paired ebook is still in the inbox")
	}
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/"+a.ID+"/pair", map[string]string{"book": l.book.ID}, false).Code; got != 404 {
		t.Errorf("second pair: %d", got)
	}
	// No book chosen: 422, a toast for htmx and the page with the sentence otherwise.
	rec = l.inboxPost(t, l.owner, "/admin/ebooks/"+b.ID+"/pair", nil, true)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose a book first.") || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("htmx, no book: %d %q", rec.Code, rec.Body.String())
	}
	rec = l.inboxPost(t, l.owner, "/admin/ebooks/"+b.ID+"/pair", nil, false)
	inboxHas(t, rec.Body.String(), `role="alert"`, "Choose a book first.", "Which audiobook is it?")
	if rec.Code != 422 {
		t.Errorf("plain, no book: %d", rec.Code)
	}
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/"+b.ID+"/pair", map[string]string{"book": "nobook"}, false).Code; got != 404 {
		t.Errorf("unknown book: %d", got)
	}
	// Not ready: 409, a toast for htmx.
	wait := l.addPlaced(t, "pair-wait.epub", "pending", "")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, wait); err != nil {
		t.Fatal(err)
	}
	rec = l.inboxPost(t, l.owner, "/admin/ebooks/"+wait+"/pair", map[string]string{"book": l.book.ID}, true)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "still being read") {
		t.Errorf("not ready, htmx: %d %q", rec.Code, rec.Body.String())
	}
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/"+wait+"/pair", map[string]string{"book": l.book.ID}, false).Code; got != 409 {
		t.Errorf("not ready, plain: %d", got)
	}
	// The htmx success answer is the inbox itself.
	rec = l.inboxPost(t, l.admin, "/admin/ebooks/"+b.ID+"/pair", map[string]string{"book": l.book.ID}, true)
	if rec.Code != 200 {
		t.Errorf("htmx pair: %d", rec.Code)
	}
}

func TestEbookInboxKeep(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.textPairAdd(t, "keep.epub", "Zzyzx Almanac", "Nobody Known", "")
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/"+f.ID+"/keep", nil, false).Code; got != 303 {
		t.Fatalf("keep: %d", got)
	}
	if book, hold := inboxHold(t, l, f.ID); book != "" || hold != "kept" {
		t.Errorf("after keep: book %q hold %q", book, hold)
	}
	body := l.get(l.owner, "/admin/ebooks", false).Body.String()
	inboxHas(t, body, "Kept on their own", "inbox-"+f.ID)
	// The Text tab never lists it, and the libraries page does not count it.
	inboxLacks(t, l.get(l.owner, l.path(""), false).Body.String(), "Zzyzx Almanac")
	inboxLacks(t, l.get(l.owner, "/admin/libraries", false).Body.String(), "ebook to pair", "ebooks to pair")
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/nope/keep", nil, false).Code; got != 404 {
		t.Errorf("keep unknown: %d", got)
	}
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/"+f.ID+"/keep", nil, true).Code; got != 200 {
		t.Errorf("keep again, htmx: %d", got)
	}
}

func TestEbookInboxRoles(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.textPairAdd(t, "roles.epub", "Zzyzx Almanac", "Nobody Known", "")
	for _, who := range []struct {
		name string
		ck   *http.Cookie
		ok   bool
	}{{"member", l.reader, false}, {"member no read", l.noRead, false}, {"guest", l.guest, false}, {"guest no read", l.guestNoR, false}, {"admin", l.admin, true}, {"owner", l.owner, true}} {
		want := map[bool]int{true: 200, false: 404}[who.ok]
		for _, path := range []string{"/admin/ebooks", "/admin/ebooks/" + f.ID} {
			if got := l.get(who.ck, path, false).Code; got != want {
				t.Errorf("%s GET %s: %d, want %d", who.name, path, got, want)
			}
		}
		if who.ok {
			continue
		}
		for _, verb := range []string{"pair", "keep"} {
			if got := l.inboxPost(t, who.ck, "/admin/ebooks/"+f.ID+"/"+verb, map[string]string{"book": l.book.ID}, false).Code; got != 404 {
				t.Errorf("%s POST %s: %d, want 404", who.name, verb, got)
			}
		}
	}
	if book, hold := inboxHold(t, l, f.ID); book != "" || hold != "" {
		t.Errorf("a refused person changed the file: book %q hold %q", book, hold)
	}
	// Anonymous people are sent to sign in, never shown the inbox.
	if rec := l.a.do(http.MethodGet, "/admin/ebooks", nil, nil); rec.Code == 200 {
		t.Error("anonymous GET /admin/ebooks: 200")
	}
	for _, who := range []struct {
		name string
		ck   *http.Cookie
	}{{"admin", l.admin}, {"owner", l.owner}} {
		if got := l.inboxPost(t, who.ck, "/admin/ebooks/"+f.ID+"/keep", nil, false).Code; got != 303 {
			t.Errorf("%s keep: %d", who.name, got)
		}
	}
}

func TestEbookInboxNeverABook(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	count := func(q string) (n int) {
		t.Helper()
		if err := l.a.st.Reader().QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	pages := []string{"/", "/library", "/search?q=Unpaired"}
	snap := func() (books, fts, shelves int, bodies []string) {
		for _, p := range pages {
			bodies = append(bodies, l.get(l.owner, p, false).Body.String())
		}
		return count(`SELECT count(*) FROM books`), count(`SELECT count(*) FROM books_fts`), count(`SELECT count(*) FROM shelf_items`), bodies
	}
	b0, f0, s0, bodies0 := snap()
	f := l.textPairAdd(t, "quillwort.epub", "Quillwort Unpaired", "Nobody Known", "")
	if book, _ := inboxHold(t, l, f.ID); book != "" {
		t.Fatal("the file is on a book")
	}
	b1, f1, s1, bodies1 := snap()
	if b0 != b1 || f0 != f1 || s0 != s1 {
		t.Errorf("rows changed: books %d->%d fts %d->%d shelf_items %d->%d", b0, b1, f0, f1, s0, s1)
	}
	for i, p := range pages {
		inboxLacks(t, bodies1[i], "Quillwort Unpaired")
		// The pages say the same before and after, apart from per-request tokens.
		if i < 2 && strings.Count(bodies0[i], "ts-card") != strings.Count(bodies1[i], "ts-card") {
			t.Errorf("%s: the number of books listed changed", p)
		}
	}
	// Kept on its own, it is still not a book anywhere but the inbox.
	if got := l.inboxPost(t, l.owner, "/admin/ebooks/"+f.ID+"/keep", nil, false).Code; got != 303 {
		t.Fatalf("keep: %d", got)
	}
	b2, f2, _, bodies2 := snap()
	if b2 != b0 || f2 != f0 {
		t.Errorf("rows changed after keep: books %d fts %d", b2, f2)
	}
	for i := range pages {
		inboxLacks(t, bodies2[i], "Quillwort Unpaired")
	}
	inboxHas(t, l.get(l.owner, "/admin/ebooks", false).Body.String(), "Quillwort Unpaired")
}
