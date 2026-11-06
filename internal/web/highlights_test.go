package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// ahlAddFor stores a highlight of a person in the lab's book.
func ahlAddFor(t *testing.T, l *rdrLab, user, id, quote, note, colour string, at int64) {
	t.Helper()
	s0, _ := l.sidIn(1)
	if _, err := l.a.st.Writer().Exec(`INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, note, colour, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, 0, ?, 5, ?, ?, ?, ?, ?)`, id, user, l.book.ID, l.ed.ID, rdrCh, s0, s0, quote, note, colour, at, at); err != nil {
		t.Fatal(err)
	}
}

func TestAllHighlightsRoles(t *testing.T) {
	l := rdrSeed(t)
	readerID, noReadID := sweepUserID(t, l.a, l.reader), sweepUserID(t, l.a, l.noRead)
	ahlAddFor(t, l, l.ownerID, "hl_own", "Ownerquote alpha", "owner note", "green", 1000)
	ahlAddFor(t, l, readerID, "hl_rd", "Readerquote beta", "", "pink", 2000)
	ahlAddFor(t, l, noReadID, "hl_nr", "Noreadquote gamma", "", "blue", 3000)

	if rec := l.a.do(http.MethodGet, "/me/highlights", nil, nil); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("anonymous = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for _, p := range []string{"/me/highlights.md", "/me/highlights.json"} {
		if rec := l.a.do(http.MethodGet, p, nil, nil); rec.Code == http.StatusOK {
			t.Errorf("anonymous got %s", p)
		}
	}
	get := func(ck *http.Cookie, path string) (int, string) {
		rec := l.get(ck, path, false)
		return rec.Code, rec.Body.String()
	}
	_, body := get(l.owner, "/me/highlights")
	mustContain(t, "owner", body, "Ownerquote alpha", "owner note", "Green highlight", "#s-")
	mustNotContain(t, "owner", body, "Readerquote", "Noreadquote")
	_, body = get(l.reader, "/me/highlights")
	mustContain(t, "reader", body, "Readerquote beta", "Pink highlight")
	mustNotContain(t, "reader", body, "Ownerquote", "Noreadquote")
	// A person whose grant lacks can_read sees nothing, though the row is stored; admin and guest see only their own (none).
	for name, ck := range map[string]*http.Cookie{"noRead": l.noRead, "admin": l.admin, "guest": l.guest} {
		code, body := get(ck, "/me/highlights")
		if code != http.StatusOK {
			t.Errorf("%s: %d", name, code)
		}
		mustNotContain(t, name, body, "Ownerquote", "Readerquote", "Noreadquote", l.book.Title)
	}
	// Search, colour and the one-book list.
	_, body = get(l.owner, "/me/highlights?q=OWNER+NOTE")
	mustContain(t, "search", body, "Ownerquote alpha")
	_, body = get(l.owner, "/me/highlights?q=%25")
	mustContain(t, "wildcard", body, "Nothing matches that.")
	_, body = get(l.owner, "/me/highlights?colour=pink")
	mustContain(t, "colour", body, "Nothing matches that.")
	_, body = get(l.owner, "/me/highlights?book="+l.book.ID)
	mustContain(t, "book", body, "Only "+l.book.Title+".", "Ownerquote alpha")
	if rec := l.a.do(http.MethodPost, "/me/highlights", nil, nil, l.owner); rec.Code == http.StatusOK {
		t.Error("POST answered 200")
	}

	// The reader loses the book: the rows stay stored, the page and the exports show nothing.
	if _, err := l.a.st.Writer().Exec(`DELETE FROM grants WHERE user_id = ?`, readerID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/me/highlights", "/me/highlights.md", "/me/highlights.json"} {
		_, body = get(l.reader, p)
		mustNotContain(t, "revoked "+p, body, "Readerquote", l.book.Title)
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT count(*) FROM highlights WHERE user_id = ? AND deleted_at IS NULL`, readerID).Scan(&n); err != nil || n != 1 {
		t.Errorf("stored rows = %d %v", n, err)
	}
}

func TestAllHighlightsExport(t *testing.T) {
	l := rdrSeed(t)
	readerID := sweepUserID(t, l.a, l.reader)
	ahlAddFor(t, l, l.ownerID, "hl_own", "Ownerquote alpha", "owner note", "green", 1000)
	ahlAddFor(t, l, readerID, "hl_rd", "Readerquote beta", "", "pink", 2000)
	rec := l.get(l.owner, "/me/highlights.md", false)
	md := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Disposition") != `attachment; filename="tendshelf-highlights.md"` || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("md: %d %v", rec.Code, rec.Header())
	}
	mustContain(t, "md", md, "# "+l.book.Title+"\n", "> Ownerquote alpha", "owner note", "*Highlight, green, ")
	mustNotContain(t, "md", md, "Readerquote")
	rec = l.get(l.owner, "/me/highlights.json", false)
	var out struct {
		Books []struct {
			Book  string
			Marks []struct{ Kind, Quote, Colour string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || len(out.Books) != 1 || out.Books[0].Book != l.book.Title ||
		len(out.Books[0].Marks) != 1 || out.Books[0].Marks[0].Quote != "Ownerquote alpha" || out.Books[0].Marks[0].Colour != "green" {
		t.Fatalf("json: %v %s", err, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Content-Disposition") != `attachment; filename="tendshelf-highlights.json"` {
		t.Errorf("json headers: %v", rec.Header())
	}
	// A person with nothing gets an empty list, not an error.
	rec = l.get(l.admin, "/me/highlights.json", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"books": []`) {
		t.Errorf("admin json: %d %s", rec.Code, rec.Body.String())
	}
	// The per-book export is untouched: same shape as before (book, marks), reader format.
	rec = l.get(l.owner, "/books/"+l.book.ID+"/read/marks.json", false)
	if !strings.HasPrefix(rec.Body.String(), "{\n  \"book\": \""+l.book.Title+"\",\n  \"marks\": [\n    {\n      \"kind\": \"highlight\",") {
		t.Errorf("per-book json changed:\n%s", rec.Body.String())
	}
}

func TestAllHighlightsProfileLink(t *testing.T) {
	l := rdrSeed(t)
	ahlAddFor(t, l, l.ownerID, "hl_own", "Ownerquote alpha", "", "yellow", 1000)
	if _, err := l.a.st.Writer().Exec(`INSERT INTO reading_sessions(id, user_id, book_id, device_id, started_at, ended_at, chars_read, active_ms)
VALUES ('rds_ahl', ?, ?, 'dev', 1, 2, 10, 1)`, l.ownerID, l.book.ID); err != nil {
		t.Fatal(err)
	}
	mustContain(t, "stats panel", l.get(l.owner, "/me", false).Body.String(), "/me/highlights")
}
