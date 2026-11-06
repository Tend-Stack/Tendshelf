//go:build dev

package web

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The page table of the accessibility audit (M3c, docs/plans/m3c-audit.md). Every full-page GET route
// the server registers is a row here or an entry in a11ySkip with the reason; TestA11yRouteCoverage
// reads the source and fails on a route that is neither.

// a11yPage is one page the audit opens. Fixture picks the app it runs against: "main" (the Harbour
// Lights library, shelves, queue, admin data), "upload" (the Add books page with its queue), "fresh"
// (no owner yet) and "setup" (an owner whose setup is not finished). Roles are the people who get a
// 200 for it: "owner", "member" (holds a library grant) and "anon". Shell marks a page inside the
// signed-in shell (navigation, player, toasts).
type a11yPage struct {
	Key, Pattern string
	Path         func(e *a11yEnv) string
	Roles        []string
	Fixture      string
	Shell        bool
}

// a11yState is a page after a click: the full player, Eyes-free, the Now playing panel. Fixture is
// "player" (the playerEnv of the browser tests). The page is loaded first, then Prepare runs.
// MinWidth is the narrowest viewport the state exists at.
type a11yState struct {
	Key, Role string
	Fixture   string // "" is "player"
	Path      func(e *a11yEnv) string
	Prepare   func(t *testing.T, tab *cdp.Tab, e *a11yEnv)
	MinWidth  int
}

// a11ySkip lists the registered GETs that are not full pages: a redirect, a file or JSON.
var a11ySkip = map[string]string{
	"/uploads/{id}":                     "JSON (the upload status the uploader polls), not a page",
	"/progress/{bookID}":                "JSON, not a page",
	"/reading/{bookID}":                 "JSON (the reading place), not a page",
	"/offline/books/{id}/read":          "the offline reader shell: the markup of /books/{id}/read without chapter text, audited there",
	"/books/{id}/read/images/{blockID}": "a picture from the book, not a page",
	"/admin/ebooks/{fileID}/file":       "the original bytes of an ebook file, as a download, not a page",
	"/books/{id}/read/marks.md":         "a download of the person's own marks (RD3)",
	"/books/{id}/read/marks.txt":        "a download of the person's own marks (RD3)",
	"/books/{id}/read/marks.json":       "a download of the person's own marks (RD3)",
	"/me/words.md":                      "a download of the person's own word list (G1)",
	"/me/words.json":                    "a download of the person's own word list (G1)",
	"/welcome":                          "redirects (303) to Home",
	"/books/{id}/text/{fileID}/file":    "the original ebook file as a download, not a page",
	"/s/{token}/stream/{audioFileID}":   "audio stream, not a page",
	"/s/{token}/peaks/{audioFileID}":    "JSON waveform, not a page",
}

func a11yStatic(p string) func(*a11yEnv) string { return func(*a11yEnv) string { return p } }

// a11yPages is the table. A pattern that stands for several pages (the book tabs, the settings
// sections, two searches) has one row for each.
func a11yPages() []a11yPage {
	both := []string{"owner", "member"}
	owner := []string{"owner"}
	anon := []string{"anon"}
	row := func(key, pattern string, path func(*a11yEnv) string, roles []string, fixture string, shell bool) a11yPage {
		return a11yPage{Key: key, Pattern: pattern, Path: path, Roles: roles, Fixture: fixture, Shell: shell}
	}
	book := func(suffix string) func(*a11yEnv) string {
		return func(e *a11yEnv) string { return "/books/" + e.BookID + suffix }
	}
	rd := func(role string, q ...string) func(*a11yEnv) string {
		return func(e *a11yEnv) string { return brPath(e.ReaderLab, q...) }
	}
	pages := []a11yPage{
		row("home", "/{$}", a11yStatic("/"), both, "main", true),
		row("libraries", "/libraries", a11yStatic("/libraries"), both, "main", true),
		row("library", "/libraries/{id}", func(e *a11yEnv) string { return "/libraries/" + e.LibID }, both, "main", true),
		row("add-books", "/libraries/{id}/add", func(e *a11yEnv) string { return "/libraries/" + e.UploadLibID + "/add" }, owner, "upload", true),
		row("search-empty", "/search", a11yStatic("/search"), both, "main", true),
		row("search-query", "/search", a11yStatic("/search?q=Lighthouse"), both, "main", true),
		row("book", "/books/{id}", book(""), both, "main", true),
		row("book-about", "/books/{id}/{tab}", book("/about"), both, "main", true),
		row("book-chapters", "/books/{id}/{tab}", book("/chapters"), both, "main", true),
		row("book-bookmarks", "/books/{id}/{tab}", book("/bookmarks"), both, "main", true),
		row("book-details", "/books/{id}/{tab}", book("/details"), owner, "main", true),
		row("book-text", "/books/{id}/{tab}", book("/text"), owner, "main", true),
		// B2: a text book (the member holds can_read = 0 and sees the sentence that says so) and the library's ebooks.
		row("book-ebook", "/books/{id}", func(e *a11yEnv) string { return "/books/" + e.TextBookID }, both, "main", true),
		row("book-ebook-text", "/books/{id}/{tab}", func(e *a11yEnv) string { return "/books/" + e.TextBookID + "/text" }, owner, "main", true),
		// M5 RD4: the reader. The owner reads in pages, the admin in scroll (two people, one book); each sheet is a
		// page of its own address. The sheets, the footnote popup, the selection toolbar and the note dialog are in a11yStates.
		row("reader-pages", "/books/{id}/read", rd("owner", "ch=2"), owner, "reader", false),
		row("reader-scroll", "/books/{id}/read", rd("admin", "ch=2"), []string{"admin"}, "reader", false),
		row("libraries-ebooks", "/libraries", a11yStatic("/libraries?format=text&view=list"), both, "main", true),
		row("book-remove", "/books/{id}/remove", func(e *a11yEnv) string { return "/books/" + e.UploadBookID + "/remove" }, owner, "upload", true),
		row("book-share", "/books/{id}/share", book("/share"), owner, "main", true),
		row("author", "/authors/{id}", func(e *a11yEnv) string { return "/authors/" + e.AuthorID }, both, "main", true),
		row("narrator", "/narrators/{id}", func(e *a11yEnv) string { return "/narrators/" + e.NarratorID }, both, "main", true),
		row("series", "/series/{id}", func(e *a11yEnv) string { return "/series/" + e.SeriesID }, both, "main", true),
		row("shelves", "/shelves", a11yStatic("/shelves"), both, "main", true),
		row("shelf", "/shelves/{id}", func(e *a11yEnv) string { return "/shelves/" + e.ShelfID }, owner, "main", true),
		row("shelf-empty", "/shelves/{id}", func(e *a11yEnv) string { return "/shelves/" + e.EmptyShelfID }, owner, "main", true),
		row("shelf-delete", "/shelves/{id}/delete", func(e *a11yEnv) string { return "/shelves/" + e.ShelfID + "/delete" }, owner, "main", true),
		row("queue", "/queue", a11yStatic("/queue"), both, "main", true),
		row("me", "/me", a11yStatic("/me"), both, "main", true),
		row("me-devices", "/me/devices", a11yStatic("/me/devices"), both, "main", true),
		row("me-settings", "/me/settings", a11yStatic("/me/settings"), both, "main", true),
	}
	sections := make([]string, 0, len(settingsSections))
	for s := range settingsSections {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	for _, s := range sections {
		pages = append(pages, row("me-settings-"+s, "/me/settings/{section}", a11yStatic("/me/settings/"+s), both, "main", true))
	}
	pages = append(pages,
		row("me-password", "/me/password", a11yStatic("/me/password"), both, "main", true),
		row("me-downloads", "/me/downloads", a11yStatic("/me/downloads"), both, "main", true),
		row("me-listening", "/me/listening", a11yStatic("/me/listening"), both, "main", true),
		row("reading-stats", "/me/reading", a11yStatic("/me/reading"), both, "main", true),
		row("words", "/me/words", a11yStatic("/me/words"), both, "main", true),
		row("share", "/share", a11yStatic("/share"), owner, "main", true),
		row("admin-libraries", "/admin/libraries", a11yStatic("/admin/libraries"), owner, "main", true),
		row("admin-folder-new", "/admin/libraries/folders/new", a11yStatic("/admin/libraries/folders/new"), owner, "main", true),
		row("admin-folder-emptied", "/admin/folders/{id}/emptied", func(e *a11yEnv) string { return "/admin/folders/" + e.AdminFolderID + "/emptied" }, owner, "main", true),
		row("admin-lookups", "/admin/libraries/{id}/lookups", func(e *a11yEnv) string { return "/admin/libraries/" + e.AdminLibID + "/lookups" }, owner, "main", true),
		row("admin-ebooks", "/admin/ebooks", a11yStatic("/admin/ebooks"), owner, "main", true),
		row("admin-ebook", "/admin/ebooks/{fileID}", func(e *a11yEnv) string { return "/admin/ebooks/" + e.EbookFoundID }, owner, "main", true),
		row("admin-users", "/admin/users", a11yStatic("/admin/users"), owner, "main", true),
		row("admin-user", "/admin/users/{id}", func(e *a11yEnv) string { return "/admin/users/" + e.MemberID }, owner, "main", true),
		row("admin-user-disable", "/admin/users/{id}/disable", func(e *a11yEnv) string { return "/admin/users/" + e.MemberID + "/disable" }, owner, "main", true),
		row("admin-user-stop", "/admin/users/{id}/stop", func(e *a11yEnv) string { return "/admin/users/" + e.MemberID + "/stop" }, owner, "main", true),
		row("admin-email", "/admin/email", a11yStatic("/admin/email"), owner, "main", true),
		row("admin-backups", "/admin/backups", a11yStatic("/admin/backups"), owner, "main", true),
		row("admin-duplicates", "/admin/duplicates", a11yStatic("/admin/duplicates"), owner, "main", true),
		row("admin-duplicate-merge", "/admin/duplicates/merge", func(e *a11yEnv) string { return a11yDupMergePath(e) }, owner, "main", true),
		row("admin-duplicates-merged", "/admin/duplicates/merged", a11yStatic("/admin/duplicates/merged"), owner, "main", true),
		row("login", "/login", a11yStatic("/login"), anon, "main", false),
		row("invite", "/invite/{token}", func(e *a11yEnv) string { return "/invite/" + e.InviteToken }, anon, "main", false),
		row("link", "/s/{token}", func(e *a11yEnv) string { return "/s/" + e.LinkToken }, anon, "main", false),
		row("offline", "/offline", a11yStatic("/offline"), []string{"anon", "owner"}, "main", false),
		row("setup", "/setup", a11yStatic("/setup"), anon, "fresh", false),
		row("setup-server", "/setup/server", a11yStatic("/setup/server"), owner, "setup", false),
		row("setup-folder", "/setup/folder", a11yStatic("/setup/folder"), owner, "setup", false),
		row("setup-done", "/setup/done", a11yStatic("/setup/done"), owner, "setup", false),
		row("kitchen-sink", "/dev/kitchen-sink", a11yStatic("/dev/kitchen-sink"), owner, "main", true),
		row("kitchen-sink-b", "/dev/kitchen-sink/b", a11yStatic("/dev/kitchen-sink/b"), owner, "main", true),
	)
	return pages
}

// a11yStates are the pages after a click. They run on the playerEnv: its book plays real (generated)
// audio and the owner already has a position, so the bar is there on /libraries.
func a11yStates() []a11yState {
	openPlayer := func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
		tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
		fullpOpenPlayer(t, tab)
	}
	return []a11yState{
		{Key: "full-player", Role: "owner", Path: a11yStatic("/libraries"), Prepare: openPlayer},
		{Key: "eyes-free", Role: "owner", Path: a11yStatic("/libraries"), Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			openPlayer(t, tab, e)
			efOpenMode(t, tab)
			time.Sleep(450 * time.Millisecond)
		}, MinWidth: 0},
		// M5 RD4: the reader after a click, on the reader fixture.
		// Each sheet is the reader page with a sheet open: the page behind is inert, so these are states.
		{Key: "reader-contents", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2", "sheet=contents") }, Prepare: brSheetOpen},
		{Key: "reader-marks", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2", "sheet=marks") }, Prepare: brSheetOpen},
		{Key: "reader-settings", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2", "sheet=reading") }, Prepare: brSheetOpen},
		{Key: "reader-search", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2", "sheet=search") }, Prepare: brSheetOpen},
		{Key: "reader-footnote", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2") }, Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			tab.Eval(`document.querySelector("a[data-note-href]").click()`, nil)
			tab.WaitFor(`document.querySelector("dialog.ts-readernote--note[open]") !== null`, 8*time.Second)
		}},
		{Key: "reader-selection", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2") }, Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			rnSelect(tab, e.ReaderLab.sids()[1], 2, 12)
		}},
		{Key: "reader-note", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2") }, Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			rnSelect(tab, e.ReaderLab.sids()[1], 2, 12)
			tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="note"]').click()`, nil)
			tab.WaitFor(`document.getElementById("reader-note-dialog").open`, 5*time.Second)
		}},
		// M5 G1: the look-up popup with a definition, and the word list with a word in it.
		{Key: "reader-lookup", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2") }, Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			lkDict(t, e.ReaderLab, "audit", "lantern\tA lamp with a case and a handle.\n")
			tab.Eval(`(() => { const s = document.querySelector(".ts-s"); const w = document.createTreeWalker(s, NodeFilter.SHOW_TEXT); const n = w.nextNode(); const r = new Range(); r.setStart(n, 0); r.setEnd(n, Math.min(5, n.length)); getSelection().removeAllRanges(); getSelection().addRange(r); })()`, nil)
			tab.WaitFor(`!document.getElementById("reader-marks").hidden`, 5*time.Second)
			tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="lookup"]').click()`, nil)
			tab.WaitFor(`document.querySelector("dialog.ts-lookup[open]") !== null`, 8*time.Second)
		}},
		{Key: "words-list", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string {
			l := e.ReaderLab
			if _, _, err := l.a.srv.progress.AddWord(context.Background(), progress.Word{UserID: l.ownerID, BookID: l.book.ID, Word: "lantern", Quote: "A lantern in the window."}); err != nil {
				panic(err)
			}
			return "/me/words"
		}, Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {}},
		// M5 G4: the skim bar in use, with its tip showing.
		{Key: "reader-skim", Role: "owner", Fixture: "reader", Path: func(e *a11yEnv) string { return brPath(e.ReaderLab, "ch=2") }, Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			tab.WaitFor(`document.getElementById("reader-skim") && !document.getElementById("reader-skim").hidden`, 8*time.Second)
			// The script loads on the first touch; a key event stands for it, as the page may not hold focus here.
			tab.Eval(`(() => { const bar = document.getElementById("reader-skim"); bar.focus(); bar.dispatchEvent(new Event("keydown")); })()`, nil)
			time.Sleep(500 * time.Millisecond)
			tab.Eval(`(() => { const bar = document.getElementById("reader-skim"); bar.value = 4500; bar.dispatchEvent(new Event("input", { bubbles: true })); })()`, nil)
			tab.WaitFor(`!document.getElementById("reader-skim-tip").hidden`, 3*time.Second)
		}},
		{Key: "now-panel", Role: "owner", Path: a11yStatic("/libraries"), Prepare: func(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
			// As nowPanelOpen does: the bar plays first, then the player opens.
			tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
			bmkeyPlay(t, tab, "[data-ts-toggle]")
			fullpOpenPlayer(t, tab)
			tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__h") !== null`, 5*time.Second)
		}, MinWidth: 1280},
	}
}

// a11yRouteLiteral finds the "GET /path" strings the server registers.
var a11yRouteLiteral = regexp.MustCompile(`"GET (/[^"]*)"`)

// a11yRoutesInSource returns the full-page GET patterns registered in the non-test sources.
func a11yRoutesInSource(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	prefixes := []string{"/hx/", "/api/", "/static/", "/stream/", "/covers/", "/peaks/", "/sw.js", "/healthz", "/probe/"}
	suffixes := []string{"/cover", "/photo", "/export", "/cover/{n}", "manifest", ".png", ".ico"}
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range a11yRouteLiteral.FindAllStringSubmatch(string(b), -1) {
			p := m[1]
			skip := false
			for _, pre := range prefixes {
				skip = skip || strings.HasPrefix(p, pre)
			}
			for _, suf := range suffixes {
				skip = skip || strings.HasSuffix(p, suf)
			}
			if !skip {
				seen[p] = true
			}
		}
	}
	var out []string
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestA11yRouteCoverage fails when a registered page route is neither audited nor skipped with a reason.
func TestA11yRouteCoverage(t *testing.T) {
	audited := map[string]bool{}
	for _, p := range a11yPages() {
		audited[p.Pattern] = true
	}
	source := map[string]bool{}
	for _, p := range a11yRoutesInSource(t) {
		source[p] = true
		if !audited[p] {
			if _, ok := a11ySkip[p]; !ok {
				t.Errorf("GET %s is registered but neither in a11yPages nor in a11ySkip", p)
			}
		}
	}
	for p := range audited {
		if !source[p] {
			t.Errorf("a11yPages has %s, which the source does not register", p)
		}
	}
	for p, reason := range a11ySkip {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("a11ySkip %s has no reason", p)
		}
		if audited[p] {
			t.Errorf("a11ySkip %s is also in a11yPages", p)
		}
		if !source[p] {
			t.Errorf("a11ySkip %s, which the source does not register", p)
		}
	}
}

// a11yDupMergePath is the confirm page for the first two audiobooks of the Harbour Lights library.
func a11yDupMergePath(e *a11yEnv) string {
	rows, err := e.Main.st.Reader().Query(`SELECT id FROM books WHERE library_id = ? AND kind = 'audio' ORDER BY added_at, id LIMIT 2`, e.LibID)
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			panic(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 2 {
		panic("a11y: the Harbour Lights library needs two audiobooks for the merge page")
	}
	return "/admin/duplicates/merge?keep=" + ids[0] + "&drop=" + ids[1]
}
