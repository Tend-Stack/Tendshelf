package web

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The Now playing panel's server side (NP1). Helpers start with "nowpanel".

func TestNowChapterLine(t *testing.T) {
	for _, c := range []struct {
		name        string
		index, n    int
		title       string
		lead, extra string
	}{
		{"no title", 13, 27, "", "Chapter 13 of 27", ""},
		{"the number as a name", 13, 27, "Chapter 13", "Chapter 13 of 27", ""},
		{"any case", 13, 27, "chapter 13", "Chapter 13 of 27", ""},
		{"just the digits", 13, 27, "13", "Chapter 13 of 27", ""},
		{"padded digits", 13, 27, " 13 ", "Chapter 13 of 27", ""},
		{"another number is a name", 13, 27, "Chapter 12", "Chapter 13 of 27", "Chapter 12"},
		{"a real name", 2, 19, "The Second Lamp", "Chapter 2 of 19", "The Second Lamp"},
		{"one chapter", 1, 1, "The Whole Book", "", ""},
		{"no chapters", 1, 0, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			lead, rest := ChapterLine(c.index, c.n, c.title)
			if lead != c.lead || rest != c.extra {
				t.Errorf("ChapterLine(%d, %d, %q) = %q, %q; want %q, %q", c.index, c.n, c.title, lead, rest, c.lead, c.extra)
			}
		})
	}
}

// nowpanelBody fetches a panel part as the owner (or the cookie's person) and returns the status and
// the body with HTML entities decoded, so plain words can be matched.
func nowpanelGet(a *testApp, path string, ck *http.Cookie) (int, string) {
	rec := browseGet(a, path, true, ck)
	return rec.Code, html.UnescapeString(rec.Body.String())
}

func nowpanelWant(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("the panel lacks %q", w)
		}
	}
}

func nowpanelNot(t *testing.T, body string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(body, w) {
			t.Errorf("the panel has %q", w)
		}
	}
}

// nowpanelSeries adds a three-book series, "Tide Two" (the open book) with a long description, a
// publisher, a year, genres and three chapters, and puts the owner 62% into it.
func nowpanelSeries(t *testing.T) (*testApp, library.Book, library.Book) {
	t.Helper()
	a, f := bookApp(t)
	long := strings.Repeat("A calm harbour at dusk. ", 30)
	spec := func(title, seq string) librarytest.BookSpec {
		return librarytest.BookSpec{Title: title, Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"},
			Series: "Harbour Lights", Sequence: seq, DurationMs: 3_600_000, AddedAt: 1_700_000_000_000}
	}
	one := librarytest.AddBook(t, a.lib, f, spec("Tide One", "1"))
	two := spec("Tide Two", "2")
	two.Description, two.Genres, two.PublishedYear = long, []string{"Mystery", "Sea stories"}, 2021
	b := librarytest.AddBook(t, a.lib, f, two)
	librarytest.AddBook(t, a.lib, f, spec("Tide Three", "3"))
	bookSetDetails(t, a, b.ID, func(x *library.Book) { x.Publisher = "Harrow & Finch"; x.Language = "English" })
	bookSeedChapters(t, a, b.ID, 3)
	uid := sweepUserID(t, a, a.owner(t))
	_, err := progress.New(a.st, nil).Save(context.Background(), progress.Update{UserID: uid, BookID: b.ID,
		PositionMs: 2_232_000, DurationMs: 3_600_000, ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "nowpanel-dev"})
	if err != nil {
		t.Fatal(err)
	}
	return a, b, one
}

func TestNowPanelFull(t *testing.T) {
	a, b, _ := nowpanelSeries(t)
	code, body := nowpanelGet(a, "/hx/player/panel/"+b.ID+"", nil)
	if code != http.StatusOK {
		t.Fatalf("panel: %d", code)
	}
	nowpanelWant(t, body,
		`<aside id="now-panel" class="ts-np" aria-label="Now playing: about this book" data-book-id="`+b.ID+`" data-book-href="/books/`+b.ID+`">`,
		`data-ts-now-by`, `read by Nan Narrator`, `data-ts-now-menu`, `role="tablist"`, `id="now-tab-about"`, `id="now-tab-bookmarks"`,
		`hx-get="/hx/player/panel/`+b.ID+`/bookmarks"`, `hx-target="#now-panel-tab"`, `role="tabpanel"`,
		`id="now-tab-queue"`, `hx-get="/hx/queue?in=panel"`,
		`Written by`, `Ann Author`, `Read by`, `Nan Narrator`,
		// series
		`Book 2 of 3`, `You're here · 62%`, `aria-current="true"`, `Up next · 1 h 0 m`, `Not started`,
		// summary and Read more
		`<details class="ts-np__more">`, `Read more`, `Show less`,
		// details
		`<dl class="ts-np__credits">`, `<dt>Published</dt>`, `Harrow & Finch · 2021`, `Unabridged`, `<dt>Length</dt>`, `1 h 0 m · 3 chapters`,
		`<dt>Language</dt>`, `Added `, `<dt>On the shelf</dt>`,
		// genres and the rail
		`/libraries?genre=Mystery`, `Also read by Nan Narrator`, `Tide One`, `Tide Three`,
		// the menu
		`Add to shelf`, `href="/books/`+b.ID+`/share"`)
	// The bar is the only transport.
	nowpanelNot(t, body, "data-ts-play", `type="range"`, "data-ts-full-toggle", "data-ts-full-", `id="now-tab-chapters"`, "Unknown", "—", "style=", "<script")
	if got := regexp.MustCompile(`aria-current="true"`).FindAllString(body, -1); len(got) != 1 {
		t.Errorf("%d rows are current, want 1", len(got))
	}
	if !regexp.MustCompile(`Up next\s*<span aria-hidden="true">·</span>\s*<span id="now-panel-queue-count">0</span>`).MatchString(body) {
		t.Error("the Up next tab lacks its count")
	}
	// The open book's tab, with its count.
	if !regexp.MustCompile(`Bookmarks\s*<span aria-hidden="true">·</span>\s*<span id="now-panel-count">0</span>`).MatchString(body) {
		t.Error("the Bookmarks tab lacks its count")
	}
	// The top of the player is the player's own: no hero, close button, chapter line or finish form.
	nowpanelNot(t, body, "ts-np__hero", "data-ts-close-player", `id="now-panel-chapter"`, "Mark as finished", "Book page")
}

// A fragment, not a page: nothing but the panel.
func TestNowPanelIsAFragment(t *testing.T) {
	a, b, _ := nowpanelSeries(t)
	for _, part := range []string{"", "/about", "/bookmarks", "/count", "/chapter/0"} {
		rec := browseGet(a, "/hx/player/panel/"+b.ID+part, true, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", part, rec.Code)
		}
		if s := rec.Body.String(); strings.Contains(s, "<html") || strings.Contains(s, "<body") {
			t.Errorf("%s is a whole page", part)
		}
	}
}

func TestNowPanelRoles(t *testing.T) {
	e := sweepSeedGranted(t)
	want := map[int]struct{ share, shelf bool }{
		sweepOwner: {true, true}, sweepAdmin: {true, true}, sweepMember: {false, true}, sweepGuest: {false, false},
	}
	for role, w := range want {
		t.Run(sweepRoleNames[role], func(t *testing.T) {
			code, body := fetchAs(e, role, "/hx/player/panel/"+e.book.ID)
			if code != http.StatusOK {
				t.Fatalf("panel: %d", code)
			}
			body = html.UnescapeString(body)
			if has := strings.Contains(body, `href="/books/`+e.book.ID+`/share"`); has != w.share {
				t.Errorf("Share present = %v, want %v", has, w.share)
			}
			if has := strings.Contains(body, "Add to shelf"); has != w.shelf {
				t.Errorf("Add to shelf present = %v, want %v", has, w.shelf)
			}
			nowpanelNot(t, body, "data-ts-play", `type="range"`, "data-ts-full-toggle", `id="now-tab-chapters"`)
		})
	}
}

func TestNowPanelLookupLink(t *testing.T) {
	l := newWriteLab(t, 1, false)
	o := Options{Library: l.a.lib, Store: l.a.st, Jobs: l.a.runner}
	compareMod(t, &compareNet{})(&o)
	l.a.srv.details = o.Details
	href := `href="/books/` + l.book.ID + `/details?sheet=lookup"`
	panel := "/hx/player/panel/" + l.book.ID
	has := func(ck *http.Cookie) bool {
		rec := l.get(ck, panel, true)
		if rec.Code != http.StatusOK {
			return false
		}
		body := html.UnescapeString(rec.Body.String())
		return strings.Contains(body, href) && strings.Contains(body, "Look up details online")
	}
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin} {
		if !has(ck) {
			t.Errorf("%s: no lookup link", name)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest} {
		if has(ck) {
			t.Errorf("%s: has the lookup link", name)
		}
	}
	if err := metadata.SetOnlineEnabled(context.Background(), l.a.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if has(l.owner) {
		t.Error("online off: has the lookup link")
	}
	if err := metadata.SetOnlineEnabled(context.Background(), l.a.st, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET description = 'A story.' WHERE id = ?`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	if has(l.owner) {
		t.Error("with a description: has the lookup link")
	}
}

func TestNowPanelSparse(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Bare Book", Authors: []string{"Solo Author"}, AddedAt: 1_700_000_000_000})
	code, body := nowpanelGet(a, "/hx/player/panel/"+b.ID, nil)
	if code != http.StatusOK {
		t.Fatalf("panel: %d", code)
	}
	nowpanelWant(t, body, "No description for this book yet.", `<dt>Written by</dt>`)
	nowpanelNot(t, body, "Unknown", "—", "<dd></dd>", "<dt></dt>", `id="now-series"`, `ts-np__genres`, `id="now-more"`, "Read more",
		"<dt>Published</dt>", "<dt>Language</dt>", "Look up details online")
	for _, empty := range regexp.MustCompile(`<d[dt]>\s*</d[dt]>`).FindAllString(body, -1) {
		t.Errorf("empty row %q", empty)
	}
	nowpanelNot(t, body, "data-ts-now-by") // no narrator, no "read by"
}

func TestNowPanelBookmarks(t *testing.T) {
	f := bookmarkEnv(t)
	base := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	for i := 1; i <= 7; i++ {
		bookmarkAdd(t, f, f.ownerID, f.book.ID, int64(i)*600_000, fmt.Sprintf("npnote%d", i), base.Add(time.Duration(i)*time.Hour))
	}
	path := "/hx/player/panel/" + f.book.ID
	code, body := nowpanelGet(f.a, path+"/bookmarks", f.owner)
	if code != http.StatusOK {
		t.Fatalf("bookmarks: %d", code)
	}
	if n := strings.Count(body, `class="ts-mark"`); n != 5 {
		t.Errorf("%d rows, want 5", n)
	}
	if strings.Index(body, "npnote7") < 0 || strings.Index(body, "npnote7") > strings.Index(body, "npnote6") || strings.Contains(body, "npnote2") {
		t.Error("the rows are not the newest five, newest first")
	}
	nowpanelWant(t, body, `href="/books/`+f.book.ID+`/bookmarks"`, "All 7 bookmarks and notes", `<span id="now-panel-count" hx-swap-oob="outerHTML">7</span>`)
	code, body = nowpanelGet(f.a, path+"/count", f.owner)
	if code != http.StatusOK || strings.TrimSpace(body) != `<span id="now-panel-count">7</span>` {
		t.Errorf("count: %d %q", code, body)
	}
	_, body = nowpanelGet(f.a, path, f.owner)
	if !regexp.MustCompile(`Bookmarks\s*<span aria-hidden="true">·</span>\s*<span id="now-panel-count">7</span>`).MatchString(body) {
		t.Error("the panel's Bookmarks tab does not say 7")
	}
	// Bookmarks are the person's own: the admin has none.
	_, body = nowpanelGet(f.a, path+"/bookmarks", f.admin)
	nowpanelWant(t, body, "No bookmarks yet.", `hx-swap-oob="outerHTML">0</span>`)
	nowpanelNot(t, body, "All 0", "npnote")
}

func TestNowPanelChapterRoute(t *testing.T) {
	f := bookmarkEnv(t)
	path := "/hx/player/panel/" + f.book.ID + "/chapter/"
	for _, c := range []struct {
		idx  string
		code int
		want string
		not  string
	}{
		{"1", 200, "Chapter 2 of 4", ""},
		{"1", 200, "<em>The Harbour</em>", ""},
		{"2", 200, `class="ts-np__where">`, "<em>"}, // untitled: only the lead
		{"abc", 404, "", ""},
		{"-1", 404, "", ""},
		{"4", 404, "", ""},
		{"99", 404, "", ""},
	} {
		code, body := nowpanelGet(f.a, path+c.idx, f.owner)
		if code != c.code {
			t.Errorf("chapter/%s: %d, want %d", c.idx, code, c.code)
			continue
		}
		if c.want != "" && !strings.Contains(body, c.want) {
			t.Errorf("chapter/%s lacks %q: %s", c.idx, c.want, body)
		}
		if c.not != "" && strings.Contains(body, c.not) {
			t.Errorf("chapter/%s has %q", c.idx, c.not)
		}
	}
}

// Someone with no grant sees nothing of the book, in any part, and no title leaks.
func TestNowPanelHidden(t *testing.T) {
	f := bookmarkEnv(t)
	for _, part := range []string{"", "/about", "/bookmarks", "/count", "/chapter/0"} {
		for name, ck := range map[string]*http.Cookie{"member": f.member} {
			code, body := nowpanelGet(f.a, "/hx/player/panel/"+f.book.ID+part, ck)
			if code != http.StatusNotFound || strings.Contains(body, "Lantern Tide") || strings.Contains(body, "Imogen Hale") {
				t.Errorf("%s %s: %d", name, part, code)
			}
		}
	}
}

// A person who may view a book but not listen to it gets no Bookmarks tab, and no finish form. The
// checker grants view and listen together today, so this proves the view builder alone.
func TestNowPanelNoListenView(t *testing.T) {
	f := bookmarkEnv(t)
	d, err := f.a.lib.BookDetail(context.Background(), library.Scope{Books: share.Filter{SQL: "1 = 1"}, Libraries: share.Filter{SQL: "1 = 1"}}, f.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	v := nowPanelView(d, false, 0, 0)
	if len(v.Tabs) != 1 || v.Tabs[0].Key != "about" {
		t.Errorf("tabs %+v, want About only", v.Tabs)
	}
	v = nowPanelView(d, true, 3, 2)
	if len(v.Tabs) != 3 || v.Tabs[1].Count != "3" || v.Tabs[2].Key != "queue" || v.Tabs[2].Count != "2" || v.Tabs[2].CountID != "now-panel-queue-count" {
		t.Errorf("tabs %+v, want About, Bookmarks · 3 and Up next · 2", v.Tabs)
	}
}

func TestNowPanelHeroButton(t *testing.T) {
	render := func(shows bool) string {
		var b bytes.Buffer
		err := components.PlayButton(components.PlayButtonView{BookID: "bk", FileID: "f", Href: "/stream/f", Label: "Resume", ShowsPlayer: shows}).Render(context.Background(), &b)
		if err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	if !strings.Contains(render(true), " data-ts-show-player") || strings.Contains(render(false), "data-ts-show-player") {
		t.Error("data-ts-show-player is not exactly the hero's")
	}
}
