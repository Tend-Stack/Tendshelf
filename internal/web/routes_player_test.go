package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

func (e *progRouteEnv) player(path string, ck *http.Cookie) (int, string) {
	rec := e.a.do(http.MethodGet, path, nil, nil, ck)
	return rec.Code, rec.Body.String()
}

func TestPlayerDataRender(t *testing.T) {
	e := progRouteEnvOf(t)
	ctx := context.Background()
	bookSetDetails(t, e.a, e.book.ID, func(b *library.Book) { b.Title = `Q"uote <b> & more` })
	if err := e.a.srv.users.SaveSettings(ctx, e.ownerID, users.Settings{Theme: "system", Speed: 1.25, SkipBackS: 10, SkipForwardS: 60,
		AutoRewind: false, NextInSeries: true, PlayerTint: true}); err != nil {
		t.Fatal(err)
	}
	e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(2500, e.nowMs.Load(), "devicealpha1"), func(r *http.Request) { r.Header.Set("User-Agent", progRouteFF) })

	code, body := e.player("/hx/player/"+e.book.ID, e.owner)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	want := []string{
		`id="player-data"`, `class="ts-player-data"`, ` hidden`,
		`data-book-id="` + e.book.ID + `"`,
		`data-title="Q&#34;uote &lt;b&gt; &amp; more"`,
		`data-author="Pia Progauthor"`,
		`data-cover-url="/covers/` + e.book.ID + `?w=512&amp;v=abcdef`,
		`data-duration-ms="6000"`, `data-position-ms="2500"`, `data-finished="0"`,
		`data-updated-at="` + itoa(e.nowMs.Load()) + `"`,
		`data-device-id="devicealpha1"`, `data-device-label="Firefox on Linux"`,
		`data-server-now="` + itoa(e.nowMs.Load()) + `"`,
		`data-speed="1.25"`, `data-skip-back-s="10"`, `data-skip-forward-s="60"`, `data-auto-rewind="0"`,
		`data-accent-dark="#9ac9c2"`, `data-accent-light="#35615c"`, `data-accent-sepia="#7a4a1e"`,
		`data-offset-ms="0"`, `data-offset-ms="3000"`, `data-mime="audio/wav"`, `data-may-need-compat="0"`, `data-playable="1"`,
		`data-idx="0"`, `data-start-ms="0"`, `data-end-ms="2890000"`, `>Part 1</li>`, `>Part 4</li>`,
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("player data lacks %s\n%s", w, body)
		}
	}
	if n := strings.Count(body, "<li data-id="); n != 2 {
		t.Errorf("file items = %d", n)
	}
	if n := strings.Count(body, "data-start-ms="); n != 4 {
		t.Errorf("chapter items = %d", n)
	}
	for _, f := range e.files {
		if strings.Contains(body, f.Path) || strings.Contains(body, e.a.dir) {
			t.Errorf("the fragment carries a file path: %s", f.Path)
		}
	}
	for _, bad := range []string{"style=", "<script", "onclick"} {
		if strings.Contains(body, bad) {
			t.Errorf("the fragment contains %q", bad)
		}
	}

	// Tint off: no accent attributes.
	if err := e.a.srv.users.SaveSettings(ctx, e.ownerID, users.Settings{Theme: "system", Speed: 1.25, SkipBackS: 10, SkipForwardS: 60,
		NextInSeries: true, PlayerTint: false}); err != nil {
		t.Fatal(err)
	}
	if _, body = e.player("/hx/player/"+e.book.ID, e.owner); strings.Contains(body, "data-accent") {
		t.Error("accents present with PlayerTint off")
	}
	// An encrypted second file is not playable.
	streamExec(t, e.a, `UPDATE audio_files SET encrypted=1 WHERE id=?`, e.files[1].ID)
	_, body = e.player("/hx/player/"+e.book.ID, e.owner)
	if !strings.Contains(body, `data-playable="0"`) || strings.Count(body, `data-playable="1"`) != 1 {
		t.Errorf("playable flags wrong after encrypting file 2: %s", body)
	}
	// The member cannot see the book at all.
	if code, _ := e.player("/hx/player/"+e.book.ID, e.member); code != 404 {
		t.Errorf("member: %d", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestPlayerDataCurrent(t *testing.T) {
	e := progRouteEnvOf(t)
	_, f := librarytest.NewLibrary(t, e.a.lib, "Second library", t.TempDir())
	b2 := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{Title: "Progword Second", DurationMs: 6000, AddedAt: 6, Files: 1})
	now := e.nowMs.Load()

	if code, _ := e.player("/hx/player", e.owner); code != 204 {
		t.Fatalf("no progress: %d", code)
	}
	e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(1000, now, "devicealpha1"), nil)
	e.nowMs.Add(5000)
	e.post(b2.ID, e.owner, e.ownerTok, progRouteForm(1000, now+5000, "devicealpha1"), nil)
	current := func(ck *http.Cookie) string {
		code, body := e.player("/hx/player", ck)
		if code == 204 {
			return ""
		}
		if code != 200 {
			t.Fatalf("current: %d", code)
		}
		return body
	}
	if body := current(e.owner); !strings.Contains(body, `data-book-id="`+b2.ID+`"`) {
		t.Errorf("newer book is not the current one: %s", body)
	}
	// B finished: A is current again.
	e.nowMs.Add(1000)
	if rec := e.a.do(http.MethodPost, "/hx/books/"+b2.ID+"/finished", url.Values{"finished": {"1"}},
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", e.ownerTok); r.Header.Set("HX-Request", "true") }, e.owner); rec.Code != 204 {
		t.Fatalf("finish: %d", rec.Code)
	}
	if body := current(e.owner); !strings.Contains(body, `data-book-id="`+e.book.ID+`"`) {
		t.Errorf("A should be current: %s", body)
	}
	// A refused too: nothing to continue.
	streamExec(t, e.a, `UPDATE books SET refused_reason='drm' WHERE id=?`, e.book.ID)
	if body := current(e.owner); body != "" {
		t.Errorf("a refused book is current: %s", body)
	}
	streamExec(t, e.a, `UPDATE books SET refused_reason=NULL WHERE id=?`, e.book.ID)
	// A person with a progress row on a book they cannot see gets 204.
	streamExec(t, e.a, `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
VALUES (?,?,?,?,NULL,?,?,?,?)`, e.memberID, e.book.ID, 1000, 6000, now, now, "devicealpha1", "x")
	if body := current(e.member); body != "" {
		t.Errorf("hidden book reached the member: %s", body)
	}
}

func TestPlayerDataPlayTarget(t *testing.T) {
	book := library.Book{RootPath: "/lib/book"}
	f := func(id string, off, dur int64, encrypted bool) library.AudioFile {
		return library.AudioFile{ID: id, Path: "/lib/book/" + id + ".wav", Container: "wav", Codec: "pcm_s16le", OffsetMs: off, DurationMs: dur, Encrypted: encrypted, ProbeJSON: []byte("{}")}
	}
	two := []library.AudioFile{f("f0", 0, 3000, false), f("f1", 3000, 3000, false)}
	cases := []struct {
		name   string
		files  []library.AudioFile
		pos    int64
		id     string
		off    int64
		wantOK bool
	}{
		{"start", two, 0, "f0", 0, true},
		{"second file", two, 3500, "f1", 500, true},
		{"past the end", two, 99_000, "f1", 2999, true},
		{"negative", two, -5, "f0", 0, true},
		{"first encrypted", []library.AudioFile{f("f0", 0, 3000, true), f("f1", 3000, 3000, false)}, 100, "f1", 0, true},
		{"gap", []library.AudioFile{f("f0", 0, 3000, false), f("f1", 4000, 3000, false)}, 3500, "f1", 0, true},
		{"all unplayable", []library.AudioFile{f("f0", 0, 3000, true), f("f1", 3000, 3000, true)}, 100, "", 0, false},
		{"zero duration skipped", []library.AudioFile{f("f0", 0, 0, false), f("f1", 0, 3000, false)}, 10, "f1", 10, true},
		{"none", nil, 0, "", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, off, ok := playTarget(book, c.files, c.pos)
			if id != c.id || off != c.off || ok != c.wantOK {
				t.Errorf("playTarget = (%q, %d, %v), want (%q, %d, %v)", id, off, ok, c.id, c.off, c.wantOK)
			}
		})
	}
}

func TestPlayerDataMime(t *testing.T) {
	for _, c := range [][3]string{
		{"mp4", "aac", `audio/mp4; codecs="mp4a.40.2"`},
		{"mp4", "alac", `audio/mp4; codecs="alac"`},
		{"mp4", "ac3", "audio/mp4"},
		{"mp3", "mp3", "audio/mpeg"},
		{"flac", "flac", "audio/flac"},
		{"ogg", "opus", `audio/ogg; codecs="opus"`},
		{"ogg", "vorbis", `audio/ogg; codecs="vorbis"`},
		{"webm", "opus", `audio/webm; codecs="opus"`},
		{"webm", "vorbis", `audio/webm; codecs="vorbis"`},
		{"wav", "pcm_s16le", "audio/wav"},
		{"aac", "aac", "audio/aac"},
		{"ogg", "flac", "audio/ogg"},
		{"wma", "wmav2", "application/octet-stream"},
	} {
		if got := playerMime(c[0], c[1]); got != c[2] {
			t.Errorf("playerMime(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
}
