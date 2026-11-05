package web

import (
	"net/http"
	"strings"
	"testing"
)

// Offline reading, downloads and surfaces, server side (G6b, decision 0019). Helpers start with "ofd".

// TestOffDownloadView is the rights table of the book page's Download control for a text book: it needs the
// right to read the text and the right to download, the rule of the ebook file download.
func TestOffDownloadView(t *testing.T) {
	l := rsSeed(t, ofrEPUB(t))
	page := "/books/" + l.book.ID
	marker := `data-ts-download="` + l.book.ID + `"`
	table := []struct {
		name string
		ck   *http.Cookie
		want bool
	}{
		{"owner", l.owner, true},
		{"member with ReadText, no download", l.reader, false},
		{"member without ReadText", l.noRead, false},
	}
	for _, c := range table {
		rec := l.get(c.ck, page, false)
		if got := rec.Code == http.StatusOK && strings.Contains(rec.Body.String(), marker); got != c.want {
			t.Errorf("%s: Download control = %v (status %d), want %v", c.name, got, rec.Code, c.want)
		}
	}
	// Can download added: the reader gets the control; the person without ReadText still does not.
	ofrAllowDownload(t, l)
	if body := l.get(l.reader, page, false).Body.String(); !strings.Contains(body, marker) {
		t.Error("member with ReadText and DownloadBook has no Download control")
	}
	if body := l.get(l.noRead, page, false).Body.String(); strings.Contains(body, marker) {
		t.Error("member without ReadText has a Download control")
	}
}

// TestOffDownloadShellRows: /offline?text= lists a held text book with a Read button, leaves out what the
// person may not read, and answers a bad id with 400.
func TestOffDownloadShellRows(t *testing.T) {
	l := rsSeed(t, ofrEPUB(t))
	id := l.book.ID
	body := l.get(l.owner, "/offline?books="+id+"&text="+id, false).Body.String()
	for _, w := range []string{l.book.Title, "Ready offline", `href="/books/` + id + `/read"`, "data-ts-read", "Read " + l.book.Title} {
		if !strings.Contains(body, w) {
			t.Errorf("shell row lacks %q", w)
		}
	}
	if strings.Contains(body, "data-ts-play=") || strings.Contains(body, "Needs a connection") {
		t.Error("a text-only row has a play control, or is listed twice")
	}
	// Without ?text= a text book is not a row (the old client): nothing to play.
	if body := l.get(l.owner, "/offline?books="+id, false).Body.String(); strings.Contains(body, `href="/books/`+id+`/read"`) {
		t.Error("a book is listed with a Read button without being in text=")
	}
	// A reader without the right to the text, an unknown id: left out without a word.
	for _, c := range []struct {
		name string
		ck   *http.Cookie
		q    string
	}{{"no ReadText", l.noRead, "text=" + id}, {"unknown id", l.owner, "text=bk_unknown"}} {
		rec := l.get(c.ck, "/offline?"+c.q, false)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `/read"`) || strings.Contains(rec.Body.String(), "bk_unknown") {
			t.Errorf("%s: %d, row shown or id echoed", c.name, rec.Code)
		}
	}
	for _, q := range []string{"text=a%20b", "text=a,,b", "text=../x"} {
		if rec := l.get(l.owner, "/offline?"+q, false); rec.Code != http.StatusBadRequest {
			t.Errorf("GET /offline?%s: %d, want 400", q, rec.Code)
		}
	}
}
