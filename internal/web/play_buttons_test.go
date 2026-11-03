package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// P1c-6: play buttons, the book play region, chapter links, the Home hero and rows.

// playBtnEnv is the progress environment with a book of two 90 s files and three 60 s chapters.
func playBtnEnv(t *testing.T) *progRouteEnv {
	t.Helper()
	e := progRouteEnvOf(t)
	for i, f := range e.files {
		streamExec(t, e.a, `UPDATE audio_files SET container='mp3', codec='mp3', duration_ms=90000, offset_ms=? WHERE id=?`, i*90_000, f.ID)
	}
	streamExec(t, e.a, `UPDATE books SET duration_ms=180000 WHERE id=?`, e.book.ID)
	chs := []library.Chapter{
		{StartMs: 0, EndMs: 60_000, Title: "One", Source: library.ChapterFile},
		{StartMs: 60_000, EndMs: 120_000, Title: "Two", Source: library.ChapterFile},
		{StartMs: 120_000, EndMs: 180_000, Title: "Three", Source: library.ChapterFile},
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	e.files, _ = e.a.lib.AudioFiles(context.Background(), e.book.ID)
	e.book, _ = e.a.lib.Book(context.Background(), e.book.ID)
	return e
}

// playBtnProgress stores a progress row: finished when finishedAt is not 0.
func playBtnProgress(t *testing.T, e *progRouteEnv, userID, bookID string, pos, finishedAt, updated int64) {
	t.Helper()
	fin := any(nil)
	if finishedAt != 0 {
		fin = finishedAt
	}
	streamExec(t, e.a, `INSERT OR REPLACE INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
VALUES (?,?,?,?,?,?,?,?,?)`, userID, bookID, pos, 180000, fin, updated, updated, "devicealpha1", "x")
}

var playBtnLibs atomic.Int64

// playBtnBook adds a one-file book of 100 s in a library of its own and makes the file playable.
func playBtnBook(t *testing.T, e *progRouteEnv, spec librarytest.BookSpec) library.Book {
	t.Helper()
	_, f := librarytest.NewLibrary(t, e.a.lib, "Shelf "+strconv.Itoa(int(playBtnLibs.Add(1))), t.TempDir())
	spec.DurationMs, spec.Files = 100_000, 1
	b := librarytest.AddBook(t, e.a.lib, f, spec)
	streamExec(t, e.a, `UPDATE audio_files SET container='mp3', codec='mp3', duration_ms=100000, offset_ms=0 WHERE book_id=?`, b.ID)
	return b
}

func playBtnGet(e *progRouteEnv, path string) string {
	return e.a.do(http.MethodGet, path, nil, nil, e.owner).Body.String()
}

func playBtnCount(body, id string) int { return strings.Count(body, `data-ts-play="`+id+`"`) }

func TestPlayButtonStates(t *testing.T) {
	e := playBtnEnv(t)
	ctx := context.Background()
	owner := users.User{ID: e.ownerID, Role: users.RoleOwner}
	f1, f2 := e.files[0].ID, e.files[1].ID
	now := e.nowMs.Load()

	prog := func(pos, fin int64) *progress.Progress {
		return &progress.Progress{PositionMs: pos, FinishedAt: fin, ClientUpdatedAt: now}
	}
	for _, c := range []struct {
		name       string
		p          *progress.Progress
		label      string
		file       string
		fileStart  int64
		start      int64
		fromStart  bool
		hrefSuffix string
	}{
		{"no row", nil, "Start listening", f1, 0, 0, false, "#t=0.000"},
		{"position 0", prog(0, 0), "Start listening", f1, 0, 0, false, "#t=0.000"},
		{"in the second file", prog(95_000, 0), "Resume", f2, 90_000, 95_000, false, "#t=5.000"},
		{"in the first file", prog(12_345, 0), "Resume", f1, 0, 12_345, false, "#t=12.345"},
		{"finished", prog(179_000, now), "Listen again", f1, 0, 0, true, "#t=0.000"},
	} {
		l, err := e.a.srv.playLinkFor(ctx, owner, e.book, c.p)
		if err != nil || l == nil {
			t.Fatalf("%s: link %v, %v", c.name, l, err)
		}
		if l.Label != c.label || l.FileID != c.file || l.FileStartMs != c.fileStart || l.StartMs != c.start || l.FromStart != c.fromStart ||
			l.Href() != "/stream/"+c.file+c.hrefSuffix || l.BookID != e.book.ID || l.Mime != "audio/mpeg" || l.MayNeedCompat {
			t.Errorf("%s: %+v href %s", c.name, *l, l.Href())
		}
	}

	// The page carries the exact attributes.
	playBtnProgress(t, e, e.ownerID, e.book.ID, 95_000, 0, now)
	body := bookMain(playBtnGet(e, "/books/"+e.book.ID))
	for _, w := range []string{
		`id="book-play"`, `hx-get="/hx/books/` + e.book.ID + `/play"`, `hx-trigger="progress-saved from:body"`,
		`href="/stream/` + f2 + `#t=5.000"`, `data-ts-play="` + e.book.ID + `"`, `data-ts-file="` + f2 + `"`,
		`data-ts-file-start-ms="90000"`, `data-ts-start-ms="95000"`, `data-ts-mime="audio/mpeg"`, `data-ts-may-need-compat="0"`,
		`>Resume</span>`, `Mark as finished`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("book page lacks %s", w)
		}
	}
	if strings.Contains(body, "data-ts-from-start") {
		t.Error("a Resume button is marked from-start")
	}
	playBtnProgress(t, e, e.ownerID, e.book.ID, 179_000, now, now)
	body = bookMain(playBtnGet(e, "/books/"+e.book.ID))
	for _, w := range []string{`>Listen again</span>`, `data-ts-from-start="1"`, `data-ts-start-ms="0"`, `href="/stream/` + f1 + `#t=0.000"`, "Mark as not started"} {
		if !strings.Contains(body, w) {
			t.Errorf("finished book page lacks %s", w)
		}
	}

	// No control when nothing can play: no playable file, a refused book, a missing book.
	streamExec(t, e.a, `UPDATE audio_files SET encrypted=1 WHERE book_id=?`, e.book.ID)
	if l, err := e.a.srv.playLinkFor(ctx, owner, e.book, nil); l != nil || err != nil {
		t.Errorf("encrypted files: %v, %v", l, err)
	}
	body = bookMain(playBtnGet(e, "/books/"+e.book.ID))
	if strings.Contains(body, "data-ts-play=") || strings.Contains(body, "Listen again") {
		t.Error("a play control is drawn for a book with no playable file")
	}
	streamExec(t, e.a, `UPDATE audio_files SET encrypted=0 WHERE book_id=?`, e.book.ID)
	for name, set := range map[string]string{
		"refused": `UPDATE books SET refused_reason='drm' WHERE id=?`,
		"missing": `UPDATE books SET missing_since=5 WHERE id=?`,
	} {
		streamExec(t, e.a, set, e.book.ID)
		b, _ := e.a.lib.Book(ctx, e.book.ID)
		if l, err := e.a.srv.playLinkFor(ctx, owner, b, nil); l != nil || err != nil {
			t.Errorf("%s: %v, %v", name, l, err)
		}
		if body := bookMain(playBtnGet(e, "/books/"+e.book.ID)); strings.Contains(body, "data-ts-play=") || strings.Contains(body, `id="book-play"`) {
			t.Errorf("%s: the page draws a play region", name)
		}
		streamExec(t, e.a, `UPDATE books SET refused_reason=NULL, missing_since=NULL WHERE id=?`, e.book.ID)
	}
	// A member who cannot see the book gets a 404 for its play region.
	if rec := e.a.do(http.MethodGet, "/hx/books/"+e.book.ID+"/play", nil, nil, e.member); rec.Code != http.StatusNotFound {
		t.Errorf("member play region: %d", rec.Code)
	}
}

func TestPlayButtonOnePerBook(t *testing.T) {
	e := playBtnEnv(t)
	now := e.nowMs.Load()
	// The hero's book is also the next book of a series: its row must not carry a second button.
	first := playBtnBook(t, e, librarytest.BookSpec{Title: "Saga One", Authors: []string{"Sam"}, Series: "Saga", Sequence: "1"})
	hero := playBtnBook(t, e, librarytest.BookSpec{Title: "Saga Two", Authors: []string{"Sam"}, Series: "Saga", Sequence: "2"})
	playBtnProgress(t, e, e.ownerID, first.ID, 100_000, now-2000, now-2000)
	playBtnProgress(t, e, e.ownerID, hero.ID, 30_000, 0, now)
	home := playBtnGet(e, "/")
	if n := playBtnCount(home, hero.ID); n != 1 {
		t.Errorf("Home: %d play controls for the hero book, want 1", n)
	}
	if !strings.Contains(home, `id="home-hero"`) || !strings.Contains(home, "Saga Two") {
		t.Error("Home lacks the hero")
	}
	for _, c := range regexp.MustCompile(`class="ts-card"[^>]*>`).FindAllString(home, -1) {
		if strings.Contains(c, "data-ts-play") {
			t.Errorf("a card carries a play control: %s", c)
		}
	}
	// A series whose next book is not the hero's gets its round button, once.
	other := playBtnBook(t, e, librarytest.BookSpec{Title: "Tide One", Authors: []string{"Tess"}, Series: "Tide", Sequence: "1"})
	next := playBtnBook(t, e, librarytest.BookSpec{Title: "Tide Two", Authors: []string{"Tess"}, Series: "Tide", Sequence: "2"})
	playBtnProgress(t, e, e.ownerID, other.ID, 0, now-5000, now-5000)
	home = playBtnGet(e, "/")
	if n := playBtnCount(home, next.ID); n != 1 {
		t.Errorf("Home: %d play controls for the next series book, want 1", n)
	}
	if n := strings.Count(home, `data-ts-play="`); n != 2 {
		t.Errorf("Home has %d play controls, want 2 (hero and one series row)", n)
	}
	// The book page: at most one per book on every tab.
	for _, tab := range []string{"", "/chapters", "/bookmarks", "/details"} {
		for _, id := range []string{e.book.ID, hero.ID} {
			if n := playBtnCount(playBtnGet(e, "/books/"+id+tab), id); n != 1 {
				t.Errorf("/books/%s%s: %d play controls, want 1", id, tab, n)
			}
		}
	}
}

func TestPlayButtonMark(t *testing.T) {
	e := playBtnEnv(t)
	ctx := context.Background()
	now := e.nowMs.Load()
	playBtnProgress(t, e, e.ownerID, e.book.ID, 50_000, 0, now)
	hx := func(r *http.Request) { r.Header.Set("X-CSRF-Token", e.ownerTok); r.Header.Set("HX-Request", "true") }

	rec := e.a.do(http.MethodPost, "/hx/books/"+e.book.ID+"/finished", url.Values{"finished": {"1"}}, hx, e.owner)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Trigger") != "progress-saved" {
		t.Fatalf("finish: %d, trigger %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	region := e.a.do(http.MethodGet, "/hx/books/"+e.book.ID+"/play", nil, nil, e.owner).Body.String()
	for _, w := range []string{`id="book-play"`, "Listen again", "Mark as not started", `value="0"`} {
		if !strings.Contains(region, w) {
			t.Errorf("after finishing the region lacks %q\n%s", w, region)
		}
	}
	if strings.Contains(region, "<html") || strings.Contains(region, "<main") {
		t.Error("the play region is not a fragment")
	}
	rec = e.a.do(http.MethodPost, "/hx/books/"+e.book.ID+"/finished", url.Values{"finished": {"0"}}, hx, e.owner)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("mark not started: %d", rec.Code)
	}
	region = e.a.do(http.MethodGet, "/hx/books/"+e.book.ID+"/play", nil, nil, e.owner).Body.String()
	if !strings.Contains(region, "Start listening") || !strings.Contains(region, "Mark as finished") || strings.Contains(region, "Resume") {
		t.Errorf("after marking not started: %s", region)
	}
	ls, err := e.a.lib.ContinueListening(ctx, bookAllScope, e.ownerID, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range ls {
		if l.Book.ID == e.book.ID {
			t.Error("a book marked not started is still in Continue listening")
		}
	}
	if rec := e.a.do(http.MethodGet, "/hx/player", nil, nil, e.owner); rec.Code != http.StatusNoContent {
		t.Errorf("/hx/player after marking not started: %d", rec.Code)
	}
	// The plain form goes back to the book.
	rec = e.a.do(http.MethodPost, "/hx/books/"+e.book.ID+"/finished", url.Values{"finished": {"1"}, "next": {"/books/" + e.book.ID}},
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", e.ownerTok) }, e.owner)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/books/"+e.book.ID {
		t.Errorf("plain form: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	page := playBtnGet(e, "/books/"+e.book.ID)
	if !strings.Contains(page, `method="post" action="/hx/books/`+e.book.ID+`/finished"`) || !strings.Contains(page, `name="_csrf"`) {
		t.Error("the mark form has no plain POST with a CSRF field")
	}
}

func TestPlayChaptersLinks(t *testing.T) {
	e := playBtnEnv(t)
	now := e.nowMs.Load()
	f1, f2 := e.files[0].ID, e.files[1].ID
	playBtnProgress(t, e, e.ownerID, e.book.ID, 70_000, 0, now)
	body := bookMain(playBtnGet(e, "/books/"+e.book.ID+"/chapters"))
	for _, w := range []string{
		`href="/stream/` + f1 + `#t=0.000"`, `data-ts-play-at="0"`, `data-ts-book="` + e.book.ID + `"`,
		`href="/stream/` + f1 + `#t=60.000"`, `data-ts-play-at="60000"`,
		`href="/stream/` + f2 + `#t=30.000"`, `data-ts-play-at="120000"`,
		`aria-label="One, played"`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("chapter list lacks %s\n%s", w, body)
		}
	}
	if n := strings.Count(body, `aria-current="true"`); n != 1 || !strings.Contains(body, `aria-current="true"`) {
		t.Errorf("aria-current on %d rows, want 1", n)
	}
	cur := regexp.MustCompile(`<a[^>]*aria-current="true"[^>]*>`).FindString(body)
	if !strings.Contains(cur, `data-ts-play-at="60000"`) {
		t.Errorf("the current chapter is not Two: %s", cur)
	}
	if strings.Count(body, "is-played") != 1 {
		t.Errorf("played rows: %d, want 1", strings.Count(body, "is-played"))
	}
	for _, a := range regexp.MustCompile(`<a[^>]*data-ts-play-at[^>]*>`).FindAllString(body, -1) {
		if strings.Contains(a, `data-ts-play="`) {
			t.Errorf("a chapter row is a resume button: %s", a)
		}
	}
	// A finished book: every row is played, none is current.
	playBtnProgress(t, e, e.ownerID, e.book.ID, 179_000, now, now)
	body = bookMain(playBtnGet(e, "/books/"+e.book.ID+"/chapters"))
	if strings.Count(body, "is-played") != 3 || strings.Contains(body, `aria-current="true"`) || !strings.Contains(body, "Three, played") {
		t.Errorf("finished book chapters: %s", body)
	}
	// Without a playable file, and for a missing book, the rows stay plain text.
	streamExec(t, e.a, `UPDATE audio_files SET encrypted=1 WHERE book_id=?`, e.book.ID)
	body = bookMain(playBtnGet(e, "/books/"+e.book.ID+"/chapters"))
	if strings.Contains(body, "data-ts-play-at") || strings.Count(body, `<li class="ts-chapter">`) != 3 {
		t.Errorf("rows are links with no playable file: %s", body)
	}
	streamExec(t, e.a, `UPDATE audio_files SET encrypted=0 WHERE book_id=?`, e.book.ID)
	streamExec(t, e.a, `UPDATE books SET missing_since=5 WHERE id=?`, e.book.ID)
	body = bookMain(playBtnGet(e, "/books/"+e.book.ID+"/chapters"))
	if strings.Contains(body, "data-ts-play-at") || strings.Count(body, `<li class="ts-chapter">`) != 3 {
		t.Errorf("rows are links for a missing book: %s", body)
	}
}

func TestHomeHeroAndRows(t *testing.T) {
	e := playBtnEnv(t)
	now := e.nowMs.Load()
	// Nothing in progress: the empty wrapper, so a first save can fill it.
	home := playBtnGet(e, "/")
	if !strings.Contains(home, `<div id="home-hero" hx-get="/hx/home/hero" hx-trigger="progress-saved from:body" hx-swap="outerHTML"></div>`) {
		t.Errorf("empty hero wrapper missing: %s", regexp.MustCompile(`<div id="home-hero"[^>]*>.{0,80}`).FindString(home))
	}
	if strings.Contains(home, "Continue listening") || strings.Contains(home, "Your series in progress") {
		t.Error("Home shows listening rows with no progress")
	}

	// Speed 1.5: 70 s into a 180 s book: chapter 2 (ends 120 s), 50 s left / 1.5 = 33 s = 1 min; book 110 s / 1.5 = 73 s = 1 m.
	if err := e.a.srv.users.SaveSettings(context.Background(), e.ownerID, users.Settings{Theme: "system", Speed: 1.5, SkipBackS: 15, SkipForwardS: 30, NextInSeries: true}); err != nil {
		t.Fatal(err)
	}
	playBtnProgress(t, e, e.ownerID, e.book.ID, 70_000, 0, now)
	home = playBtnGet(e, "/")
	for _, w := range []string{
		"Continue listening", "Progword Harbour", `Pia Progauthor`,
		"Chapter 2 · 1 min left in chapter · 1 m left in book",
		`aria-label="Book progress, chapter 2 of 3"`, `aria-valuenow="38.9"`, `class="ts-ruler__fill ts-value" data-ts-value="38.9"`,
		`class="ts-ruler__tick" data-ts-value="33.3"`, `class="ts-ruler__tick" data-ts-value="66.7"`,
		`>Resume</span>`, `href="/books/` + e.book.ID + `/chapters"`,
	} {
		if !strings.Contains(home, w) {
			t.Errorf("Home lacks %s", w)
		}
	}
	if n := strings.Count(home, `class="ts-ruler__tick"`); n != 2 {
		t.Errorf("ticks = %d, want one per chapter start after the first", n)
	}
	if hero := e.a.do(http.MethodGet, "/hx/home/hero", nil, nil, e.owner).Body.String(); !strings.Contains(home, hero) || strings.Contains(hero, "<html") {
		t.Errorf("/hx/home/hero is not the inline hero:\n%s", hero)
	}

	// Series rows: both labels, and no play control when the next book is the hero's.
	one := playBtnBook(t, e, librarytest.BookSpec{Title: "Tide One", Authors: []string{"Tess"}, Series: "Tide", Sequence: "1"})
	two := playBtnBook(t, e, librarytest.BookSpec{Title: "Tide Two", Authors: []string{"Tess"}, Series: "Tide", Sequence: "2"})
	three := playBtnBook(t, e, librarytest.BookSpec{Title: "Reed One", Authors: []string{"Rae"}, Series: "Reed", Sequence: "1"})
	playBtnProgress(t, e, e.ownerID, one.ID, 100_000, now-9000, now-9000) // finished: book two is next
	playBtnProgress(t, e, e.ownerID, three.ID, 25_000, 0, now-8000)
	home = playBtnGet(e, "/")
	for _, w := range []string{
		"Your series in progress", "Book 2 of 2 · up next: Tide Two", `aria-label="Start series Tide"`,
		"Book 1 of 1 · Reed One · 25%", `aria-label="Continue series Reed"`, `href="/series/`,
	} {
		if !strings.Contains(home, w) {
			t.Errorf("Home lacks %s", w)
		}
	}
	_ = two
	// The hero book's own series: no button on its row.
	hero := playBtnBook(t, e, librarytest.BookSpec{Title: "Gale Two", Authors: []string{"Gus"}, Narrators: []string{"Nora"}, Series: "Gale", Sequence: "2"})
	g1 := playBtnBook(t, e, librarytest.BookSpec{Title: "Gale One", Authors: []string{"Gus"}, Series: "Gale", Sequence: "1"})
	playBtnProgress(t, e, e.ownerID, g1.ID, 100_000, now-100, now-100)
	streamExec(t, e.a, `UPDATE progress SET finished_at=? WHERE book_id=?`, now-100, g1.ID)
	playBtnProgress(t, e, e.ownerID, hero.ID, 40_000, 0, now+500)
	home = playBtnGet(e, "/")
	if !strings.Contains(home, "read by Nora") {
		t.Error("the narrator line is missing")
	}
	if !strings.Contains(home, "Book 2 of 2 · Gale Two · 40%") {
		t.Error("the started label is missing for the hero's series")
	}
	if strings.Contains(home, `aria-label="Continue series Gale"`) || playBtnCount(home, hero.ID) != 1 {
		t.Error("the hero's series row carries a second button")
	}

	// Because: heading with the seed and the books like it; plain cards only.
	liked := playBtnBook(t, e, librarytest.BookSpec{Title: "Similar Book", Authors: []string{"Gus"}})
	home = playBtnGet(e, "/")
	if !strings.Contains(home, `Because you listened to <em>`) || !strings.Contains(home, "Similar Book") {
		t.Errorf("Because row missing")
	}
	if playBtnCount(home, liked.ID) != 0 {
		t.Error("a because card carries a play control")
	}

	// A member sees none of a hidden book, even with a progress row on it.
	playBtnProgress(t, e, e.memberID, e.book.ID, 70_000, 0, now)
	for _, p := range []string{"/", "/hx/home/hero"} {
		body := e.a.do(http.MethodGet, p, nil, nil, e.member).Body.String()
		if strings.Contains(body, "Progword") || strings.Contains(body, e.book.ID) || strings.Contains(body, "data-ts-play=") {
			t.Errorf("member %s shows a hidden book", p)
		}
	}
}

// Up next (Q2): the play button is split in two sections only where it plays; the second section is
// the queue menu, kept open across the region's own redraw (hx-preserve) and never a second play control.
func TestPlayButtonSplit(t *testing.T) {
	e := playBtnEnv(t)
	for name, body := range map[string]string{
		"page":     bookMain(playBtnGet(e, "/books/"+e.book.ID)),
		"fragment": playBtnGet(e, "/hx/books/"+e.book.ID+"/play"),
	} {
		for _, want := range []string{
			`<div class="ts-split" role="group">`, `<details id="book-queue-menu-` + e.book.ID + `" class="ts-split__more" data-ts-chipmenu hx-preserve>`,
			`aria-label="Queue options"`, `action="/queue"`, `hx-post="/queue"`, `hx-target="#book-queue-menu-` + e.book.ID + `"`,
			`name="where" value="next"`, `name="where" value="end"`, `>Play next</button>`, `>Add to queue</button>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s lacks %s", name, want)
			}
		}
		if n := strings.Count(body, "data-ts-play="); n != 1 {
			t.Errorf("%s has %d play controls, want 1", name, n)
		}
		for _, bad := range []string{"style=", "hx-boost=\"false\" data-ts-queue", "hx-on"} {
			if strings.Contains(body, bad) {
				t.Errorf("%s has %s", name, bad)
			}
		}
	}
}
