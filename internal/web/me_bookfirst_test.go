package web

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// Profile puts the person's books first (decision 0021, PR2): the hero, "Also in progress", "Finished
// lately", then the side column. These tests use the two-format lab of formats_test.go: an audiobook
// with an ebook, a text-only book, an owner, members with and without the right to read.

// bookfirstAudio adds an audiobook an hour long, ready to play.
func bookfirstAudio(t *testing.T, l *fmt2Lab, title string) library.Book {
	t.Helper()
	b := librarytest.AddBook(t, l.a.lib, library.Folder{ID: l.book.FolderID, LibraryID: l.book.LibraryID, Path: l.a.dir},
		librarytest.BookSpec{Title: title, Authors: []string{"Imogen Hale"}, DurationMs: sweepHourMs, Files: 1})
	if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET duration_ms = ?, container = 'mp3', codec = 'mp3' WHERE book_id = ?`, sweepHourMs, b.ID); err != nil {
		t.Fatal(err)
	}
	return b
}

// bookfirstSave stores a listening place (posMs of an hour) at the client time at.
func bookfirstSave(t *testing.T, l *fmt2Lab, ck *http.Cookie, bookID string, posMs, at int64, finished bool) {
	t.Helper()
	_, err := l.a.srv.progress.Save(context.Background(), progress.Update{UserID: fmt2Uid(t, l, ck), BookID: bookID,
		PositionMs: posMs, DurationMs: sweepHourMs, Finished: finished, ClientUpdatedAt: at, DeviceID: "device-test-0001"})
	if err != nil {
		t.Fatal(err)
	}
}

// bookfirstProfile is the main region of /me for a person.
func bookfirstProfile(l *fmt2Lab, ck *http.Cookie) string {
	return bookMain(l.get(ck, "/me", false).Body.String())
}

// bookfirstTiles cuts the "Also in progress" entries out of a page.
func bookfirstTiles(body string) []string {
	parts := strings.Split(body, `<li class="ts-pbook">`)
	var out []string
	for _, p := range parts[1:] {
		if i := strings.Index(p, "</li>"); i >= 0 {
			p = p[:i]
		}
		out = append(out, p)
	}
	return out
}

// bookfirstHero cuts the hero section out of a page.
func bookfirstHero(body string) string {
	i := strings.Index(body, `<section class="ts-hero ts-home__hero"`)
	if i < 0 {
		return ""
	}
	body = body[i:]
	return body[:strings.Index(body, "</section>")]
}

// bookfirstControls counts the resume controls of one book on a page: play links plus reader links.
func bookfirstControls(body, bookID string) int {
	return strings.Count(body, `data-ts-play="`+bookID+`"`) + strings.Count(body, `href="/books/`+bookID+`/read"`)
}

// bookfirstHeroBook is a newest audiobook so every other book of a test is drawn as a tile.
func bookfirstHeroBook(t *testing.T, l *fmt2Lab, cks ...*http.Cookie) library.Book {
	t.Helper()
	h := bookfirstAudio(t, l, "Hero Book")
	for _, ck := range cks {
		bookfirstSave(t, l, ck, h.ID, 600_000, 900_000, false)
	}
	return h
}

func TestBookFirstOrder(t *testing.T) {
	l := fmt2Seed(t)
	hero := bookfirstHeroBook(t, l, l.owner)
	bookfirstSave(t, l, l.owner, l.book.ID, 600_000, 5_000, false)
	done := bookfirstAudio(t, l, "Done Book")
	bookfirstSave(t, l, l.owner, done.ID, 3_600_000, 1_000, true)
	body := bookfirstProfile(l, l.owner)
	marks := []string{`id="pf-menu-account"`, `id="pf-menu-settings"`, hero.Title, `id="pf-also"`, `id="pf-done"`, `id="pf-stats"`, `id="pf-shelves"`}
	last := -1
	for _, m := range marks {
		i := strings.Index(body, m)
		if i < 0 || i < last {
			t.Fatalf("%q at %d, after %d; the order of the page is wrong", m, i, last)
		}
		last = i
	}
	if n := strings.Count(body, "<h1"); n != 1 {
		t.Errorf("%d h1 on Profile, want 1", n)
	}
}

func TestBookFirstNoShell(t *testing.T) {
	l := fmt2Seed(t)
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "member": l.reader, "guest": l.guest} {
		body := l.get(ck, "/me", false).Body.String()
		if strings.Contains(body, "ts-prefs__nav") || strings.Contains(body, "ts-prefs__frame") {
			t.Errorf("%s: Profile sits inside the preferences window", name)
		}
		if !strings.Contains(body, `href="/me/settings/playback"`) {
			t.Errorf("%s: no Settings menu link", name)
		}
	}
	settings := l.get(l.owner, "/me/settings", false).Body.String()
	if !strings.Contains(settings, `class="ts-prefs__who"`) || !strings.Contains(settings, `href="/me"`) {
		t.Error("Settings lost its person card linking to Profile")
	}
}

func TestBookFirstFormats(t *testing.T) {
	type tc struct {
		name   string
		who    func(l *fmt2Lab) *http.Cookie
		setup  func(t *testing.T, l *fmt2Lab, ck *http.Cookie)
		book   func(l *fmt2Lab) library.Book
		want   string // the one control's aria-label
		other  string // text that must not appear on the tile
		noCtrl bool
	}
	owner := func(l *fmt2Lab) *http.Cookie { return l.owner }
	noRead := func(l *fmt2Lab) *http.Cookie { return l.noRead }
	audioBook := func(l *fmt2Lab) library.Book { return l.book }
	textBook := func(l *fmt2Lab) library.Book { return l.tbook }
	cases := []tc{
		{name: "audio last", who: owner, book: audioBook, want: "Resume Textword Lantern", other: "Continue reading",
			setup: func(t *testing.T, l *fmt2Lab, ck *http.Cookie) {
				bookfirstSave(t, l, ck, l.book.ID, 600_000, 5_000, false)
			}},
		{name: "text last", who: owner, book: textBook, want: "Continue reading Plain Prose", other: "Resume",
			setup: func(t *testing.T, l *fmt2Lab, ck *http.Cookie) { fmt2Reading(t, l, ck, l.tbook.ID, 4200, 5_000) }},
		{name: "both, audio newer", who: owner, book: audioBook, want: "Resume Textword Lantern", other: "Continue reading",
			setup: func(t *testing.T, l *fmt2Lab, ck *http.Cookie) {
				fmt2Reading(t, l, ck, l.book.ID, 4200, 5_000)
				bookfirstSave(t, l, ck, l.book.ID, 600_000, 8_000, false)
			}},
		{name: "both, text newer", who: owner, book: audioBook, want: "Continue reading Textword Lantern", other: "Resume",
			setup: func(t *testing.T, l *fmt2Lab, ck *http.Cookie) {
				bookfirstSave(t, l, ck, l.book.ID, 600_000, 5_000, false)
				fmt2Reading(t, l, ck, l.book.ID, 4200, 8_000)
			}},
		{name: "text last, no right to read", who: noRead, book: audioBook, want: "Resume Textword Lantern", other: "Continue reading",
			setup: func(t *testing.T, l *fmt2Lab, ck *http.Cookie) {
				bookfirstSave(t, l, ck, l.book.ID, 600_000, 5_000, false)
				fmt2Reading(t, l, ck, l.book.ID, 4200, 8_000)
			}},
		{name: "no right at all", who: noRead, book: textBook, noCtrl: true,
			setup: func(t *testing.T, l *fmt2Lab, ck *http.Cookie) { fmt2Reading(t, l, ck, l.tbook.ID, 4200, 5_000) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := fmt2Seed(t)
			ck := c.who(l)
			bookfirstHeroBook(t, l, ck)
			c.setup(t, l, ck)
			b := c.book(l)
			body := bookfirstProfile(l, ck)
			var tile string
			for _, p := range bookfirstTiles(body) {
				if strings.Contains(p, `href="/books/`+b.ID+`"`) {
					tile = p
				}
			}
			if tile == "" {
				t.Fatalf("%s is not among the tiles:\n%.600s", b.Title, body)
			}
			if c.noCtrl {
				if n := bookfirstControls(tile, b.ID); n != 0 || strings.Contains(tile, `<a class="ts-iconbtn`) {
					t.Errorf("a control for a book the person may neither play nor read: %s", tile)
				}
				return
			}
			if n := strings.Count(tile, `<a class="ts-iconbtn`); n != 1 || bookfirstControls(tile, b.ID) != 1 {
				t.Errorf("%d controls on the tile, want 1: %s", n, tile)
			}
			if !strings.Contains(tile, `aria-label="`+c.want+`"`) {
				t.Errorf("tile lacks aria-label %q: %s", c.want, tile)
			}
			if strings.Contains(tile, c.other) || strings.Contains(tile, ">Listen<") || strings.Contains(tile, ">Read<") {
				t.Errorf("tile carries a secondary or other-format control (%q): %s", c.other, tile)
			}
		})
	}
}

func TestBookFirstOneResume(t *testing.T) {
	l := fmt2Seed(t)
	ids := []string{}
	for i := 0; i < 8; i++ {
		b := bookfirstAudio(t, l, fmt.Sprintf("Under Way %d", i))
		bookfirstSave(t, l, l.owner, b.ID, 600_000, int64(10_000+i), false)
		ids = append(ids, b.ID)
	}
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 50_000)
	ids = append(ids, l.tbook.ID)
	body := bookfirstProfile(l, l.owner)
	for _, id := range ids {
		if n := bookfirstControls(body, id); n != 1 {
			t.Errorf("book %s has %d resume controls on Profile, want 1", id, n)
		}
	}
	if n := len(bookfirstTiles(body)); n != 8 {
		t.Errorf("%d tiles, want 8", n)
	}
}

func TestBookFirstScope(t *testing.T) {
	l := fmt2Seed(t)
	hidden := bookfirstAudio(t, l, "Hidden Away")
	other := bookfirstAudio(t, l, "Someone Elses")
	for _, ck := range []*http.Cookie{l.owner, l.reader} {
		bookfirstSave(t, l, ck, hidden.ID, 600_000, 20_000, false)
		bookfirstSave(t, l, ck, l.book.ID, 600_000, 10_000, false)
	}
	bookfirstSave(t, l, l.owner, other.ID, 600_000, 30_000, false)
	bookfirstSave(t, l, l.owner, hidden.ID, 3_600_000, 1_000, true)
	if body := bookfirstProfile(l, l.reader); strings.Contains(body, "Someone Elses") {
		t.Error("another person's progress shows on Profile")
	}
	// Take the book out of the reader's reach: a library grant is the only way in, so drop it from the library.
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET missing_since = 1 WHERE id = ?`, hidden.ID); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "member": l.reader} {
		if body := bookfirstProfile(l, ck); strings.Contains(body, "Hidden Away") {
			t.Errorf("%s: a book out of reach shows on Profile", name)
		}
	}
	if body := bookfirstProfile(l, l.guest); strings.Contains(body, `id="pf-shelves"`) {
		t.Error("a guest sees the shelves card")
	}
	if body := bookfirstProfile(l, l.owner); !strings.Contains(body, `id="pf-shelves"`) {
		t.Error("the owner lacks the shelves card")
	}
}

func TestBookFirstCaps(t *testing.T) {
	count := func(under, finished int) (string, []string) {
		l := fmt2Seed(t)
		for i := 0; i < under; i++ {
			b := bookfirstAudio(t, l, fmt.Sprintf("Cap %02d", i))
			bookfirstSave(t, l, l.owner, b.ID, 600_000, int64(10_000+i), false)
		}
		for i := 0; i < finished; i++ {
			b := bookfirstAudio(t, l, fmt.Sprintf("Fin %02d", i))
			bookfirstSave(t, l, l.owner, b.ID, 3_600_000, int64(1_000+i), true)
		}
		body := bookfirstProfile(l, l.owner)
		return body, bookfirstTiles(body)
	}
	body, tiles := count(12, 0)
	if len(tiles) != 8 || !strings.Contains(body, "/libraries?show=unfinished") {
		t.Errorf("12 under way: %d tiles, See all %v", len(tiles), strings.Contains(body, "show=unfinished"))
	}
	body, tiles = count(6, 0)
	if len(tiles) != 5 || strings.Contains(body, "show=unfinished") {
		t.Errorf("6 under way: %d tiles, See all %v", len(tiles), strings.Contains(body, "show=unfinished"))
	}
	body, _ = count(1, 0)
	if strings.Contains(body, "Also in progress") || !strings.Contains(body, "ts-hero") {
		t.Error("1 under way: a hero and no Also in progress")
	}
	body, _ = count(0, 0)
	if !strings.Contains(body, "Nothing in progress") || !strings.Contains(body, "Start a book and it will wait for you here.") || strings.Contains(body, "ts-hero__top") {
		t.Errorf("0 under way: the empty state is missing:\n%.400s", body)
	}
	body, _ = count(0, 8)
	if n := strings.Count(bookfirstDone(body), `class="ts-card"`); n != 6 || !strings.Contains(body, "/libraries?show=finished") {
		t.Errorf("8 finished: %d shown, want 6 with See all", n)
	}
}

// bookfirstDone cuts the "Finished lately" strip out of a page.
func bookfirstDone(body string) string {
	i := strings.Index(body, `class="ts-profile__done"`)
	if i < 0 {
		return ""
	}
	body = body[i:]
	return body[:strings.Index(body, "</section>")]
}

func TestBookFirstLeftLine(t *testing.T) {
	l := fmt2Seed(t)
	bookfirstHeroBook(t, l, l.owner)
	b := bookfirstAudio(t, l, "Left Line")
	bookfirstSave(t, l, l.owner, b.ID, 600_000, 5_000, false)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 3700, 4_000)
	body := bookfirstProfile(l, l.owner)
	if !strings.Contains(body, "50 m left") {
		t.Errorf("no '50 m left' at speed 1:\n%.800s", body)
	}
	if !strings.Contains(body, "Chapter 2 · 37 %") {
		t.Errorf("no 'Chapter 2 · 37 %%' for the text book")
	}
	set, err := l.a.srv.users.LoadSettings(context.Background(), fmt2Uid(t, l, l.owner))
	if err != nil {
		t.Fatal(err)
	}
	set.Speed = 1.5
	if err := l.a.srv.users.SaveSettings(context.Background(), fmt2Uid(t, l, l.owner), set); err != nil {
		t.Fatal(err)
	}
	if body = bookfirstProfile(l, l.owner); !strings.Contains(body, "33 m left") || strings.Contains(body, "50 m left") {
		t.Error("speed 1.5 did not divide the time left")
	}
	// A book of unknown length has no line.
	if got := bookLeftText(0, 0, 1); got != "0 m" {
		t.Errorf("bookLeftText at the end = %q", got)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET duration_ms = 0 WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE progress SET duration_ms = 0 WHERE book_id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	for _, tile := range bookfirstTiles(bookfirstProfile(l, l.owner)) {
		if strings.Contains(tile, "Left Line") && strings.Contains(tile, " left") {
			t.Errorf("a book of unknown length has a line: %s", tile)
		}
	}
}

func TestBookFirstStats(t *testing.T) {
	l := fmt2Seed(t)
	body := bookfirstProfile(l, l.owner)
	if !strings.Contains(body, "Only you see these.") {
		t.Error("the privacy line is gone")
	}
	links := regexp.MustCompile(`(?s)<div class="ts-stats__links">(.*?)</div>`)
	if m := links.FindStringSubmatch(body); m == nil || strings.Count(m[1], "ts-dash__more") != 1 {
		t.Fatalf("before any reading the links wrapper holds only the dashboard link: %v", m)
	}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO reading_sessions(id, user_id, book_id, device_id, started_at, ended_at, chars_read, active_ms)
VALUES ('rds_bf', ?, ?, 'dev', 1, 2, 10, 1)`, fmt2Uid(t, l, l.owner), l.tbook.ID); err != nil {
		t.Fatal(err)
	}
	body = bookfirstProfile(l, l.owner)
	m := links.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no links wrapper under Your listening")
	}
	for _, want := range []string{`href="/me/listening"`, "Your reading", "Your word list"} {
		if !strings.Contains(m[1], want) {
			t.Errorf("the links lack %q", want)
		}
	}
	// The admin person page does not change: no links wrapper, no private numbers beyond its own panel.
	id := fmt2Uid(t, l, l.reader)
	rec := l.get(l.owner, "/admin/users/"+id, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin person page: %d", rec.Code)
	}
	page := rec.Body.String()
	if strings.Contains(page, "ts-stats__links") {
		t.Error("the admin person page gained the Profile links wrapper")
	}
}

func TestBookFirstNoInlineStyles(t *testing.T) {
	l := fmt2Seed(t)
	bookfirstHeroBook(t, l, l.owner)
	bookfirstSave(t, l, l.owner, l.book.ID, 600_000, 5_000, false)
	if _, err := l.a.lib.CreateShelf(context.Background(), fmt2Uid(t, l, l.owner), "Road trips"); err != nil {
		t.Fatal(err)
	}
	for _, ck := range []*http.Cookie{l.owner, l.reader, l.guest} {
		body := l.get(ck, "/me", false).Body.String()
		for _, bad := range []string{` style="`, "hx-on", "<script>", "javascript:"} {
			if strings.Contains(body, bad) {
				t.Errorf("Profile carries %q", bad)
			}
		}
	}
}

// TestBookFirstRequests: the new stylesheet costs no page a request. Every full page lists the page
// stylesheets it prefetches; /me adds pages-profile.css to its own links and pages-email.css leaves
// everyone's prefetch list.
func TestBookFirstRequests(t *testing.T) {
	l := fmt2Seed(t)
	prefetch := func(path string, ck *http.Cookie) int {
		return strings.Count(l.get(ck, path, false).Body.String(), `rel="prefetch"`)
	}
	home := prefetch("/", l.owner)
	if strings.Contains(l.get(l.owner, "/", false).Body.String(), "pages-email") {
		t.Error("pages-email.css is still prefetched")
	}
	if home == 0 {
		t.Skip("this build prefetches nothing")
	}
	if me := prefetch("/me", l.owner); me > home {
		t.Errorf("Profile prefetches %d, Home %d", me, home)
	}
	if !strings.Contains(l.get(l.owner, "/me", false).Body.String(), "pages-profile") {
		t.Error("Profile does not link pages-profile.css")
	}
}
