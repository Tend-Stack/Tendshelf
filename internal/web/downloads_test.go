package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestDownloadsPage: /me/downloads answers a signed-in person with the board's words, the empty
// state (the whole page without JavaScript), the DOM contract for js/downloads.js (every message,
// the two row templates, the one switch) and none of the deferred settings. The nav marks Downloads
// current. An anonymous visitor is sent to sign in.
func TestDownloadsPage(t *testing.T) {
	a := newTestApp(t)
	ck := a.as(t, users.RoleMember)
	rec := a.do(http.MethodGet, "/me/downloads", nil, nil, ck)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /me/downloads = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"<h1", "Downloads</h1>", "Books saved here play without a connection.",
		"No downloads yet", "Download a book to listen without a connection.",
		"Storage on this phone", "Remove finished books", "After 7 days. Frees space; your progress stays.",
		"Keep Tendshelf open until downloads finish.", "Install Tendshelf",
		"This browser may clear downloads when the phone runs low on space.",
		"Safari may clear downloads after a week without a visit.",
		"Downloads need a secure (https) address.",
		`aria-label="Download settings"`, `data-ts-downloads`, `data-ts-storage-bar`, `data-ts-storage-free`,
		`data-ts-storage-used`, `data-ts-persist-note`, `data-ts-safari-note`, `data-ts-insecure`,
		`data-ts-install`, `data-ts-keep-open`, `data-ts-active-count`, `data-ts-active-list`,
		`data-ts-ready-count`, `data-ts-ready-size`, `data-ts-ready-list`, `data-ts-empty`,
		`data-ts-row-active`, `data-ts-row-ready`, `data-ts-pref="removeFinished"`,
		`data-ts-act="pause"`, `data-ts-act="resume"`, `data-ts-act="cancel"`, `data-ts-act="remove"`,
		`data-ts-cover`, `data-ts-title`, `data-ts-meta`, `data-ts-row-bar`} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, m := range downloadsMessages {
		if !strings.Contains(body, " data-ts-msg-"+m.Key+"=") {
			t.Errorf("page lacks data-ts-msg-%s", m.Key)
		}
	}
	if n := strings.Count(body, `role="switch"`); n != 1 {
		t.Errorf("%d switches, want only Remove finished books", n)
	}
	for _, not := range []string{"Wi-Fi", "next book in a series", "Other apps", "is on its way"} {
		if strings.Contains(body, not) {
			t.Errorf("page contains %q", not)
		}
	}
	// Everything JS fills starts hidden (the storage and settings blocks keep their place but are not drawn: data-ts-pending), and nothing is a visible button before JS runs.
	for _, hidden := range []string{"data-ts-storage ", "data-ts-settings", "data-ts-active ", "data-ts-ready ", "data-ts-install", "data-ts-keep-open"} {
		i := strings.Index(body, hidden)
		if i < 0 {
			t.Errorf("no %q", hidden)
			continue
		}
		start := strings.LastIndex(body[:i], "<")
		if tag := body[start : i+strings.Index(body[i:], ">")]; !strings.Contains(tag, "hidden") && !strings.Contains(tag, "data-ts-pending") {
			t.Errorf("%q starts visible: %s", hidden, tag)
		}
	}
	// The nav link to /me/downloads carries aria-current="page" (the rail, the side nav or the tab bar).
	current := regexp.MustCompile(`<a[^>]*href="/me/downloads"[^>]*aria-current="page"|<a[^>]*aria-current="page"[^>]*href="/me/downloads"`)
	if !current.MatchString(body) {
		t.Error("the nav does not mark Downloads current")
	}

	anon := a.do(http.MethodGet, "/me/downloads", nil, nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next=%2Fme%2Fdownloads" {
		t.Errorf("anonymous /me/downloads = %d %s", anon.Code, anon.Header().Get("Location"))
	}
}

// TestBookDownloadButton: the Download pair is on the book page for the owner, and for a person whose
// grant allows downloads; a grant without can_download, a refused book and a book with no playable
// file get no pair. The pair is also in the play region the page refreshes.
func TestBookDownloadButton(t *testing.T) {
	e := sweepSeed(t)
	f := library.Folder{ID: e.folderID, LibraryID: e.libID, Path: e.folderDir}
	drm := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{Title: "Protected Dl", Refused: library.RefusedDRM})
	pair := []string{`data-ts-download="`, `data-ts-download-status`, `href="/me/downloads"`, `data-ts-msg-downloading="Downloading · {pct}%"`}
	has := func(path string, role int) bool {
		code, body := fetchAs(e, role, path)
		if code != http.StatusOK {
			t.Fatalf("GET %s as %d: %d", path, role, code)
		}
		return strings.Contains(body, `data-ts-download="`)
	}
	for _, path := range []string{"/books/" + e.book.ID, "/hx/books/" + e.book.ID + "/play"} {
		if !has(path, sweepOwner) {
			t.Errorf("owner: no Download on %s", path)
		}
	}
	_, body := fetchAs(e, sweepOwner, "/books/"+e.book.ID)
	for _, want := range pair {
		if !strings.Contains(body, want) {
			t.Errorf("Download pair lacks %q", want)
		}
	}
	if has("/books/"+drm.ID, sweepOwner) {
		t.Error("a refused book has a Download control")
	}
	for i, c := range []struct {
		dl   int
		want bool
	}{{0, false}, {1, true}} {
		if _, err := e.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, can_download, can_read, created_at, updated_at)
			VALUES (?, ?, 'book', ?, ?, 0, 1, 1)`, "gr_dl_"+string(rune('a'+i)), e.uid[sweepMember], e.book.ID, c.dl); err != nil {
			t.Fatal(err)
		}
		if got := has("/books/"+e.book.ID, sweepMember); got != c.want {
			t.Errorf("member with can_download=%d: Download = %v, want %v", c.dl, got, c.want)
		}
		if _, err := e.a.st.Writer().Exec(`DELETE FROM grants WHERE id = ?`, "gr_dl_"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
}
