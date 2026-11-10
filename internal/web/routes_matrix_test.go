package web

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The P1b exit audit. TestRouteMatrix sends every registered route (all but the dev-only ones)
// as an anonymous visitor, the owner, an admin, a member and a guest, and as each of them without a
// CSRF token for the unsafe methods. The expectations are the access contract written down:
// a route that is registered but has no row here fails the test, so a new route cannot ship
// unreviewed. Helpers start with "sweep".

// The four signed-in roles, in the order of every [4]int below.
var sweepRoles = [4]users.Role{users.RoleOwner, users.RoleAdmin, users.RoleMember, users.RoleGuest}

var sweepRoleNames = [4]string{"owner", "admin", "member", "guest"}

const (
	sweepOwner = iota
	sweepAdmin
	sweepMember
	sweepGuest
)

// Shorthand for the expected status of owner, admin, member and guest. A 0 asserts nothing.
func sweepSame(c int) [4]int         { return [4]int{c, c, c, c} }
func sweepStaff(a, b int) [4]int     { return [4]int{a, a, b, b} } // owner and admin / member and guest
func sweepOwnerOnly(a, b int) [4]int { return [4]int{a, b, b, b} }
func sweepNotGuest(a, b int) [4]int  { return [4]int{a, a, a, b} }

// sweepEnv is one seeded app and what the rows need to name its records.
type sweepEnv struct {
	t        *testing.T
	a        *testApp
	ck       [4]*http.Cookie
	uid      [4]string
	secondS  [4]string // public id of a second session of each person
	devices  [4]string // a live device token row of each person (P3-7a)
	shelf    [3]string // the shelf of owner, admin and member; each holds the book
	audio    [2]string // the seeded book's audio file ids, by idx (P1c-2)
	bookmark [3]string // a bookmark of owner, admin and member on the book (P1c-7a)

	libID, folderID, folderDir string
	// extra is a member with no session who holds the library grant extraGrant: the person the
	// Users rows act on (P3-2a).
	extra, extraGrant string
	extraDevice       string // a live device of extra
	// inviteTok is the token of a valid admin invite (it shows no book, so no seeded word leaks);
	// inviteRot and inviteCancel are the ids of member invites and inviteAdmin the id of an admin
	// invite, all made by the owner (P3-3a).
	inviteTok                            string
	inviteRot, inviteCancel, inviteAdmin string
	// emailJob is a finished test email of the owner; inviteMail and inviteMailAdmin are invites with an
	// address, for the email rows (P3-8a).
	emailJob, inviteMail, inviteMailAdmin string
	// link holds the share links and the link book of the link rows (P3-5a).
	link                     linkSweep
	book                     library.Book
	author, narrator, series string
	job                      string
	// cn is the fake network of the lookup service and lookupJob a finished lookup of the book (M4-6).
	cn        *compareNet
	lookupJob string
	// coverID is the book's cover row, coverJob a waiting cover adoption of the book and authorJob a
	// waiting author lookup (M4-8).
	coverID, coverJob, authorJob string
	// writeJob is a waiting write-details job of the book (M4-9).
	writeJob string
	// ebookJob is a waiting ebook read of the book; ebookRefused a refused upload each for the owner and
	// the admin to clear (M5 R1).
	ebookJob     string
	ebookRefused [2]string
	// ebookPair names the ready files the pairing rows act on (M5 R2 P2), by verb; unpair has one per
	// staff person because it uses its file up.
	ebookPair map[string]string
	// rdr names what the reader rows act on (M5 R4 P2): the edition and a highlight per verb and staff person.
	rdr  rdrSweep
	tree string // a folder for the browse view, with a file name that must never be sent

	// granted marks the app of TestRouteMatrixGranted: the member and the guest hold a library
	// grant on libID, so rows answer with Granted and the leak scan is off.
	granted bool
}

func sweepUserID(t *testing.T, a *testApp, ck *http.Cookie) string {
	t.Helper()
	h, ok := auth.HashToken(ck.Value)
	if !ok {
		t.Fatal("bad session token")
	}
	var id string
	if err := a.st.Reader().QueryRow(`SELECT user_id FROM sessions WHERE token_hash = ?`, h[:]).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// sweepWriteCover writes a tiny PNG as the cover original and its three thumbnails.
func sweepWriteCover(t *testing.T, dir, sha string) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < 8; i++ {
		img.Set(i, i, color.RGBA{30, 90, 100, 255})
	}
	write := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	rel := filepath.Join("covers", sha+".png")
	write(filepath.Join(dir, rel))
	for _, box := range mediajobs.ThumbSizes {
		write(mediajobs.ThumbPath(dir, sha, box))
	}
	return rel
}

// sweepSeed builds the app every row runs on: the four people, setup finished, one library with
// one real folder holding audio, one book with two audio files on disk (author, narrator, series, chapters, cover and thumbnails),
// a finished scan job, and a shelf holding the book for the owner, the admin and the member.
func sweepSeed(t *testing.T) *sweepEnv {
	t.Helper()
	ctx := context.Background()
	cn := &compareNet{}
	a := newTestApp(t, compareMod(t, cn))
	e := &sweepEnv{t: t, a: a, cn: cn}
	for i, r := range sweepRoles {
		e.ck[i] = a.as(t, r)
		e.uid[i] = sweepUserID(t, a, e.ck[i])
	}

	e.folderDir = adminAudioDir(t, true)
	l, f := librarytest.NewLibrary(t, a.lib, "Sweep library", e.folderDir)
	e.libID, e.folderID = l.ID, f.ID
	e.book = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
		Title: "Sweepword Lantern", Authors: []string{"Imogen Sweepauthor"}, Narrators: []string{"Theo Sweepvoice"},
		Series: "Sweepseries Saga", Sequence: "1", DurationMs: 9 * sweepHourMs, AddedAt: 5, Files: 2,
	})
	bookSeedChapters(t, a, e.book.ID, 4)
	// Audio on disk for /stream (P1c-2).
	files, err := a.lib.AudioFiles(ctx, e.book.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("seeded audio files = %d, %v", len(files), err)
	}
	for i, f := range files {
		if err := wav.Write(f.Path, 3000, 440); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.st.Writer().ExecContext(ctx, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3000, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			i*3000, fi.Size(), fi.ModTime().UnixNano(), f.ID); err != nil {
			t.Fatal(err)
		}
		e.audio[i] = f.ID
	}
	// Waveform peaks: the first file has its peaks, the second does not (W1).
	peaksAttach(t, a)
	peaksWrite(t, a, files[0])
	sha := fmt.Sprintf("%064x", 0xc0ffee)
	rel := sweepWriteCover(t, a.dir, sha)
	cover := library.Cover{BookID: e.book.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha, Path: rel, Tint: "#1f4a52"}
	if err := a.lib.Update(ctx, func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(e.book.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	people, err := a.lib.BookPeople(ctx, e.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range people {
		if p.Role == library.RoleAuthor {
			e.author = p.PersonID
		} else {
			e.narrator = p.PersonID
		}
	}
	series, err := a.lib.BookSeries(ctx, e.book.ID)
	if err != nil || len(series) == 0 {
		t.Fatalf("series: %v %v", series, err)
	}
	e.series = series[0].SeriesID

	e.extra, e.extraGrant = "usr_sweep_extra", "gr_sweep_extra"
	if _, err := a.st.Writer().Exec(`INSERT INTO users(id, username, display_name, role, disabled, created_at) VALUES (?, 'sweepextra', 'Sweep Extra', 'member', 0, 1)`, e.extra); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
		VALUES (?, ?, 'library', ?, 0, 0, 1, 1)`, e.extraGrant, e.extra, e.libID); err != nil {
		t.Fatal(err)
	}
	deviceSeed(t, e)
	inviteSeed(t, e)
	emailSeed(t, e)
	linkSeed(t, e)
	e.lookupJob = compareSeedJob(t, e, e.book.ID, true)
	coverSeed(t, e)
	writeSeed(t, e)
	uploadSeed(t, e)
	ebookSeed(t, e)
	readerSeed(t, e)
	e.job = a.adminEnqueueScan(t, l.ID)
	a.adminSetJobState(t, e.job, "done")
	if err := db.PutSetting(ctx, a.st.Writer(), settingSetupDone, true); err != nil {
		t.Fatal(err)
	}
	e.tree = fbTree(t)

	for i := 0; i < 3; i++ {
		sh, err := a.lib.CreateShelf(ctx, e.uid[i], "Sweep shelf")
		if err != nil {
			t.Fatal(err)
		}
		if err := a.lib.AddToShelf(ctx, e.uid[i], sh.ID, e.book.ID); err != nil {
			t.Fatal(err)
		}
		e.shelf[i] = sh.ID
	}
	// Progress for /progress and /hx/player (P1c-3). Member and guest hold a row for a book they
	// cannot see: that proves nothing of it leaks.
	for i := range sweepRoles {
		if _, err := progress.New(a.st, nil).Save(ctx, progress.Update{UserID: e.uid[i], BookID: e.book.ID, PositionMs: 1500,
			DurationMs: 6000, ClientUpdatedAt: 1_700_000_000_000, DeviceID: "sweepdevice01"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range sweepRoles {
		err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			_, sess, err := auth.NewSessions(a.st, nil).Create(ctx, tx, e.uid[i], "Sweep second device")
			e.secondS[i] = auth.PublicID(sess.Hash)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Bookmarks (P1c-7a).
	for i := 0; i < 3; i++ {
		b, err := progress.New(a.st, nil).AddBookmark(ctx, e.uid[i], e.book.ID, 2000, "Sweepnote private")
		if err != nil {
			t.Fatal(err)
		}
		e.bookmark[i] = b.ID
	}
	return e
}

// ownShelf is the role's own shelf; the guest has none, so the guest is pointed at the owner's.
func (e *sweepEnv) ownShelf(role int) string {
	if role > sweepMember {
		return e.shelf[sweepOwner]
	}
	return e.shelf[role]
}

// otherShelf belongs to somebody else.
func (e *sweepEnv) otherShelf(role int) string {
	if role > sweepMember {
		return e.shelf[sweepOwner]
	}
	return e.shelf[(role+1)%3]
}

// sweepRow is one request and what each person must get back.
type sweepRow struct {
	Pattern string // the registered pattern this row covers
	Label   string // names the variant when a pattern has several rows
	Method  string
	Path    func(e *sweepEnv, role int) string
	Form    func(e *sweepEnv, role int) url.Values
	Want    [4]int // owner, admin, member, guest
	// Granted replaces Want, per person, when the member and the guest hold a grant on the library
	// (TestRouteMatrixGranted). A 0 means the grant changes nothing for that person.
	Granted [4]int
	Loc     string                   // the Location of a 303 (all roles), when it is fixed
	LocOf   func(e *sweepEnv) string // the same, for a Location that holds a seeded id; wins over Loc
	Anon    int                      // the anonymous answer; 0 is the default: sign in
	AnonLoc string                   // its Location; "" is the default
	Body    func(t *testing.T, e *sweepEnv, role int, body string)
	// NoCSRF marks an unsafe row that needs no CSRF token (the public token sign-in): sent without
	// one it gets the same answer as with one, not 403.
	NoCSRF bool
	// HX sends the request as htmx does (HX-Request: true).
	HX bool
	// Addr, when set, is the client address of the request (host:port). Public rows that cost the
	// address a failure on the shared sign-in limiter use their own, so they cannot lock out later rows.
	Addr string
}

func (r sweepRow) name() string {
	if r.Label != "" {
		return r.Pattern + " " + r.Label
	}
	return r.Pattern
}

func sweepStatic(p string) func(*sweepEnv, int) string {
	return func(*sweepEnv, int) string { return p }
}

func sweepFormOf(v url.Values) func(*sweepEnv, int) url.Values {
	return func(*sweepEnv, int) url.Values { return v }
}

// sweepGet makes a row for a GET.
func sweepGet(pattern string, path func(e *sweepEnv, role int) string, want [4]int) sweepRow {
	return sweepRow{Pattern: pattern, Method: http.MethodGet, Path: path, Want: want}
}

func sweepPost(pattern string, path func(e *sweepEnv, role int) string, form func(e *sweepEnv, role int) url.Values, want [4]int, loc string) sweepRow {
	return sweepRow{Pattern: pattern, Method: http.MethodPost, Path: path, Form: form, Want: want, Loc: loc}
}

func sweepAudioPath(idx int, suffix string) func(*sweepEnv, int) string {
	return func(e *sweepEnv, _ int) string { return "/stream/" + e.audio[idx] + suffix }
}

func sweepPeaksPath(idx int) func(*sweepEnv, int) string {
	return func(e *sweepEnv, _ int) string { return "/peaks/" + e.audio[idx] }
}

func sweepBookPath(suffix string) func(*sweepEnv, int) string {
	return func(e *sweepEnv, _ int) string { return "/books/" + e.book.ID + suffix }
}

func sweepNotContain(words ...string) func(*testing.T, *sweepEnv, int, string) {
	return func(t *testing.T, _ *sweepEnv, _ int, body string) {
		t.Helper()
		for _, w := range words {
			if strings.Contains(body, w) {
				t.Errorf("the page leaks %q", w)
			}
		}
	}
}

// sweepRows is the table. Rows for GET come first; the unsafe rows follow.
func sweepRows() []sweepRow {
	id := func(f func(e *sweepEnv) string, prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + f(e) + suffix }
	}
	libID := func(e *sweepEnv) string { return e.libID }
	folderID := func(e *sweepEnv) string { return e.folderID }
	job := func(e *sweepEnv) string { return e.job }
	bookID := func(e *sweepEnv) string { return e.book.ID }
	shelfOf := func(suffix string, other bool) func(*sweepEnv, int) string {
		return func(e *sweepEnv, role int) string {
			if other {
				return "/shelves/" + e.otherShelf(role) + suffix
			}
			return "/shelves/" + e.ownShelf(role) + suffix
		}
	}
	// The shelves routes: the owner, the admin and the member use their own shelf; nobody
	// reaches another person's.
	shelfWant := sweepNotGuest(http.StatusOK, http.StatusNotFound)
	shelfPostWant := sweepNotGuest(http.StatusSeeOther, http.StatusNotFound)
	// Adding or moving a book needs the book to be visible. The member has no grant here, so the
	// book is not found for them (404, the same as an unknown id); the plan expected 303.
	shelfBookWant := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	seeSheet := func(marker string) func(*testing.T, *sweepEnv, int, string) {
		return func(t *testing.T, _ *sweepEnv, role int, body string) {
			t.Helper()
			if role <= sweepAdmin && !strings.Contains(body, marker) {
				t.Errorf("the page lacks %q", marker)
			}
		}
	}

	rows := []sweepRow{
		// Browse
		sweepGet("GET /{$}", sweepStatic("/"), sweepSame(200)),
		// P1c-6 rows
		sweepGet("GET /hx/home/hero", sweepStatic("/hx/home/hero"), sweepSame(200)),
		sweepGet("GET /hx/books/{id}/play", id(bookID, "/hx/books/", "/play"), sweepStaff(200, 404)),
		sweepGet("GET /libraries", sweepStatic("/libraries"), sweepSame(200)),
		sweepGet("GET /libraries/{id}", id(libID, "/libraries/", ""), sweepStaff(200, 404)),
		sweepGet("GET /hx/libraries/items", sweepStatic("/hx/libraries/items"), sweepSame(200)),
		sweepGet("GET /hx/libraries/{id}/items", id(libID, "/hx/libraries/", "/items"), sweepStaff(200, 404)),
		sweepGet("GET /hx/library-facets/{kind}", sweepStatic("/hx/library-facets/author"), sweepSame(200)),
		sweepGet("GET /hx/library-facets/{id}/{kind}", id(libID, "/hx/library-facets/", "/author"), sweepStaff(200, 404)),
		sweepGet("GET /search", sweepStatic("/search?q=Lantern"), sweepSame(200)),
		// Books and people
		sweepGet("GET /books/{id}", sweepBookPath(""), sweepStaff(200, 404)),
		{Pattern: "GET /books/{id}/{tab}", Label: "about", Method: http.MethodGet, Path: sweepBookPath("/about"), Want: sweepStaff(200, 404)},
		// P1c-7a rows
		{Pattern: "GET /books/{id}/{tab}", Label: "bookmarks", Method: http.MethodGet, Path: sweepBookPath("/bookmarks"), Want: sweepStaff(200, 404)},
		{Pattern: "GET /books/{id}/{tab}", Label: "bookmarks sheet of someone else", Method: http.MethodGet, Path: func(e *sweepEnv, role int) string {
			return "/books/" + e.book.ID + "/bookmarks?sheet=bookmark&bookmark=" + e.bookmark[(role+1)%3]
		}, Want: sweepSame(404)},
		sweepGet("GET /books/{id}/bookmarks/export", sweepBookPath("/bookmarks/export?format=md"), sweepStaff(200, 404)),
		sweepGet("GET /authors/{id}", id(func(e *sweepEnv) string { return e.author }, "/authors/", ""), sweepStaff(200, 404)),
		sweepGet("GET /narrators/{id}", id(func(e *sweepEnv) string { return e.narrator }, "/narrators/", ""), sweepStaff(200, 404)),
		sweepGet("GET /series/{id}", id(func(e *sweepEnv) string { return e.series }, "/series/", ""), sweepStaff(200, 404)),
		// Podcast feeds (decision 0031): the pages need the download right, which the member and the guest lack
		// with or without the library grant.
		sweepGet("GET /books/{id}/feed", sweepBookPath("/feed"), sweepStaff(200, 404)),
		sweepGet("GET /series/{id}/feed", id(func(e *sweepEnv) string { return e.series }, "/series/", "/feed"), sweepStaff(200, 404)),
		sweepGet("GET /series/{id}/download-list", id(func(e *sweepEnv) string { return e.series }, "/series/", "/download-list"), sweepStaff(200, 404)),
		sweepGet("GET /covers/{bookID}", id(bookID, "/covers/", "?w=320"), sweepStaff(200, 404)),
		// Stream (P1c-2)
		sweepGet("GET /stream/{audioFileID}", sweepAudioPath(0, ""), sweepStaff(200, 404)),
		{Pattern: "GET /stream/{audioFileID}", Label: "second file", Method: http.MethodGet, Path: sweepAudioPath(1, ""), Want: sweepStaff(200, 404)},
		{Pattern: "GET /stream/{audioFileID}", Label: "compat without the service", Method: http.MethodGet, Path: sweepAudioPath(0, "?compat=1"), Want: sweepSame(404)},
		{Pattern: "GET /stream/{audioFileID}", Label: "unknown file", Method: http.MethodGet, Path: sweepStatic("/stream/af_unknown"), Want: sweepSame(404)},
		// Waveform peaks (W1): ready, being made, unknown
		sweepGet("GET /peaks/{audioFileID}", sweepPeaksPath(0), sweepStaff(200, 404)),
		{Pattern: "GET /peaks/{audioFileID}", Label: "not built yet", Method: http.MethodGet, Path: sweepPeaksPath(1), Want: sweepSame(404)},
		{Pattern: "GET /peaks/{audioFileID}", Label: "unknown file", Method: http.MethodGet, Path: sweepStatic("/peaks/af_unknown"), Want: sweepSame(404)},
		// Progress and player (P1c-3)
		sweepGet("GET /progress/{bookID}", id(bookID, "/progress/", ""), sweepStaff(200, 404)),
		{Pattern: "GET /progress/{bookID}", Label: "unknown book", Method: http.MethodGet, Path: sweepStatic("/progress/bk_unknown"), Want: sweepSame(404)},
		sweepGet("GET /hx/player", sweepStatic("/hx/player"), [4]int{200, 200, 204, 204}),
		{Pattern: "GET /hx/player/{bookID}", Method: http.MethodGet, Path: id(bookID, "/hx/player/", ""), Want: sweepStaff(200, 404),
			Body: func(t *testing.T, e *sweepEnv, role int, body string) {
				if role <= sweepAdmin && (!strings.Contains(body, `data-book-id="`+e.book.ID+`"`) || strings.Count(body, "<li data-id=") != 2) {
					t.Errorf("%s: the player data lacks the book or its two files", sweepRoleNames[role])
				}
			}},
		// The Now playing panel (NP1): every part needs the book to be visible; the bookmark parts also listen.
		{Pattern: "GET /hx/player/panel/{bookID}", Method: http.MethodGet, Path: id(bookID, "/hx/player/panel/", ""), Want: sweepStaff(200, 404),
			Body: func(t *testing.T, e *sweepEnv, role int, body string) {
				if role <= sweepAdmin && (!strings.Contains(body, `id="now-panel"`) || !strings.Contains(body, `data-book-id="`+e.book.ID+`"`) || strings.Contains(body, "data-ts-play")) {
					t.Errorf("%s: the panel is missing or has transport", sweepRoleNames[role])
				}
			}},
		{Pattern: "GET /hx/player/panel/{bookID}", Label: "unknown book", Method: http.MethodGet, Path: sweepStatic("/hx/player/panel/bk_unknown"), Want: sweepSame(404)},
		{Pattern: "GET /hx/player/panel/{bookID}/about", Method: http.MethodGet, Path: id(bookID, "/hx/player/panel/", "/about"), Want: sweepStaff(200, 404),
			Body: func(t *testing.T, e *sweepEnv, role int, body string) {
				if role <= sweepAdmin && !strings.Contains(body, "Summary") {
					t.Errorf("%s: the About tab lacks its summary", sweepRoleNames[role])
				}
			}},
		{Pattern: "GET /hx/player/panel/{bookID}/about", Label: "unknown book", Method: http.MethodGet, Path: sweepStatic("/hx/player/panel/bk_unknown/about"), Want: sweepSame(404)},
		{Pattern: "GET /hx/player/panel/{bookID}/bookmarks", Method: http.MethodGet, Path: id(bookID, "/hx/player/panel/", "/bookmarks"), Want: sweepStaff(200, 404),
			Body: func(t *testing.T, e *sweepEnv, role int, body string) {
				if role <= sweepAdmin && !strings.Contains(body, `id="now-panel-count"`) {
					t.Errorf("%s: the Bookmarks tab lacks its count", sweepRoleNames[role])
				}
			}},
		{Pattern: "GET /hx/player/panel/{bookID}/bookmarks", Label: "unknown book", Method: http.MethodGet, Path: sweepStatic("/hx/player/panel/bk_unknown/bookmarks"), Want: sweepSame(404)},
		{Pattern: "GET /hx/player/panel/{bookID}/count", Method: http.MethodGet, Path: id(bookID, "/hx/player/panel/", "/count"), Want: sweepStaff(200, 404),
			Body: func(t *testing.T, e *sweepEnv, role int, body string) {
				if role <= sweepAdmin && !strings.Contains(body, `<span id="now-panel-count">`) {
					t.Errorf("%s: the count is missing", sweepRoleNames[role])
				}
			}},
		{Pattern: "GET /hx/player/panel/{bookID}/count", Label: "unknown book", Method: http.MethodGet, Path: sweepStatic("/hx/player/panel/bk_unknown/count"), Want: sweepSame(404)},
		{Pattern: "GET /hx/player/panel/{bookID}/chapter/{index}", Method: http.MethodGet, Path: id(bookID, "/hx/player/panel/", "/chapter/1"), Want: sweepStaff(200, 404),
			Body: func(t *testing.T, e *sweepEnv, role int, body string) {
				if role <= sweepAdmin && !strings.Contains(body, `id="now-panel-chapter"`) {
					t.Errorf("%s: the chapter line is missing", sweepRoleNames[role])
				}
			}},
		{Pattern: "GET /hx/player/panel/{bookID}/chapter/{index}", Label: "unknown book", Method: http.MethodGet, Path: sweepStatic("/hx/player/panel/bk_unknown/chapter/0"), Want: sweepSame(404)},
		// Admin
		sweepGet("GET /admin/libraries", sweepStatic("/admin/libraries"), sweepStaff(200, 404)),
		sweepGet("GET /admin/libraries/folders/new", sweepStatic("/admin/libraries/folders/new"), sweepStaff(200, 404)),
		sweepGet("GET /admin/folders/{id}/emptied", id(folderID, "/admin/folders/", "/emptied"), sweepStaff(200, 404)),
		sweepGet("GET /hx/jobs/{id}", id(job, "/hx/jobs/", ""), sweepStaff(200, 404)),
		// Setup after it is finished
		{Pattern: "GET /setup/server", Method: http.MethodGet, Path: sweepStatic("/setup/server"), Want: sweepOwnerOnly(303, 404), Loc: "/"},
		{Pattern: "GET /setup/folder", Method: http.MethodGet, Path: sweepStatic("/setup/folder"), Want: sweepOwnerOnly(303, 404), Loc: "/"},
		sweepGet("GET /setup/done", sweepStatic("/setup/done"), sweepOwnerOnly(200, 404)),
		{Pattern: "GET /setup", Method: http.MethodGet, Path: sweepStatic("/setup"), Want: sweepSame(404), Anon: 404},
		{Pattern: "GET /login", Method: http.MethodGet, Path: sweepStatic("/login"), Want: sweepSame(303), Loc: "/", Anon: 200},
		// Me
		sweepGet("GET /me", sweepStatic("/me"), sweepSame(200)),
		sweepGet("GET /me/devices", sweepStatic("/me/devices"), sweepSame(200)),
		sweepGet("GET /me/settings", sweepStatic("/me/settings"), sweepSame(200)),
		sweepGet("GET /me/settings/{section}", sweepStatic("/me/settings/appearance"), sweepSame(200)),
		sweepGet("GET /me/password", sweepStatic("/me/password"), sweepSame(200)),
		sweepGet("GET /me/downloads", sweepStatic("/me/downloads"), sweepSame(200)),
		sweepGet("GET /me/listening", sweepStatic("/me/listening"), sweepSame(200)),
		sweepGet("GET /me/reading", sweepStatic("/me/reading"), sweepSame(200)),
		sweepGet("GET /me/words", sweepStatic("/me/words"), sweepSame(200)),
		sweepGet("GET /me/words.md", sweepStatic("/me/words.md"), sweepSame(200)),
		sweepGet("GET /me/words.json", sweepStatic("/me/words.json"), sweepSame(200)),
		sweepGet("GET /me/highlights", sweepStatic("/me/highlights"), sweepSame(200)),
		sweepGet("GET /me/highlights.md", sweepStatic("/me/highlights.md"), sweepSame(200)),
		sweepGet("GET /me/highlights.json", sweepStatic("/me/highlights.json"), sweepSame(200)),
		sweepGet("GET /me/listening/history", sweepStatic("/me/listening/history"), sweepSame(200)),
		sweepGet("GET /offline", sweepStatic("/offline"), sweepSame(200)),
		// Shelves
		sweepGet("GET /shelves", sweepStatic("/shelves"), shelfWant),
		sweepGet("GET /shelves/{id}", shelfOf("", false), shelfWant),
		sweepGet("GET /shelves/{id}/delete", shelfOf("/delete", false), shelfWant),
		// Download all (L10): a shelf's list is the owner's own; the member holds no download right here, so theirs is empty.
		sweepGet("GET /shelves/{id}/download-list", shelfOf("/download-list", false), shelfWant),

		// Query modes: the same expectations as their page
		{Pattern: "GET /admin/libraries/folders/new", Label: "?browse=", Method: http.MethodGet, Want: sweepStaff(200, 404),
			Path: func(e *sweepEnv, _ int) string {
				return "/admin/libraries/folders/new?browse=" + url.QueryEscape(e.tree)
			},
			Body: sweepNotContain("secret-title.mp3", "notes.txt")},
		{Pattern: "GET /books/{id}", Label: "?sheet=shelves", Method: http.MethodGet, Want: sweepStaff(200, 404),
			Path: sweepBookPath("?sheet=shelves"), Body: seeSheet("ts-shelf-sheet")},
		{Pattern: "GET /libraries", Label: "?sheet=filters (retired)", Method: http.MethodGet, Want: sweepSame(200),
			Path: sweepStatic("/libraries?sheet=filters"), Body: sweepNotContain("Filter and sort")},
		{Pattern: "GET /setup/folder", Label: "?job=", Method: http.MethodGet, Want: sweepOwnerOnly(200, 404),
			Path: func(e *sweepEnv, _ int) string { return "/setup/folder?job=" + e.job }},
		{Pattern: "GET /search", Label: "no query", Method: http.MethodGet, Want: sweepSame(200), Path: sweepStatic("/search")},
		{Pattern: "GET /libraries/{id}", Label: "?view=list", Method: http.MethodGet, Want: sweepStaff(200, 404),
			Path: id(libID, "/libraries/", "?view=list&sort=title")},
	}
	for _, tab := range []string{"chapters", "details"} {
		r := sweepGet("GET /books/{id}/{tab}", sweepBookPath("/"+tab), sweepStaff(200, 404))
		r.Label = tab
		rows = append(rows, r)
	}

	post := []sweepRow{
		// Admin
		sweepPost("POST /admin/libraries/scan", sweepStatic("/admin/libraries/scan"), nil, sweepStaff(303, 404), ""),
		sweepPost("POST /admin/libraries/{id}/scan", id(libID, "/admin/libraries/", "/scan"), nil, sweepStaff(303, 404), ""),
		sweepPost("POST /admin/libraries/folders", sweepStatic("/admin/libraries/folders"), func(e *sweepEnv, _ int) url.Values {
			return url.Values{"folder": {adminAudioDir(e.t, true)}, "library": {""}, "new_name": {"Sweep new library"}}
		}, sweepStaff(303, 404), "/admin/libraries"),
		sweepPost("POST /admin/folders/{id}/emptied", id(folderID, "/admin/folders/", "/emptied"), nil, sweepStaff(409, 404), ""),
		// Setup
		sweepPost("POST /setup/server", sweepStatic("/setup/server"), sweepFormOf(url.Values{"name": {"Sweep shelf"}}), sweepOwnerOnly(303, 404), "/"),
		sweepPost("POST /setup/folder", sweepStatic("/setup/folder"), func(e *sweepEnv, _ int) url.Values {
			return url.Values{"folder": {adminAudioDir(e.t, false)}}
		}, sweepOwnerOnly(303, 404), "/"),
		{Pattern: "POST /setup", Method: http.MethodPost, Path: sweepStatic("/setup"), Form: func(*sweepEnv, int) url.Values { return setupForm("") },
			Want: sweepSame(409), Anon: 409},
		{Pattern: "POST /login", Method: http.MethodPost, Path: sweepStatic("/login"),
			Form: sweepFormOf(url.Values{"username": {"nobody"}, "password": {"wrong passphrase here"}}), Want: sweepSame(422), Anon: 422},
		sweepPost("POST /logout", sweepStatic("/logout"), nil, sweepSame(303), "/login?signed_out=1"),
		// Me
		sweepPost("POST /me/settings", sweepStatic("/me/settings"), sweepFormOf(url.Values{
			"theme": {"dark"}, "theme_auto": {"0"}, "speed": {"1.25"}, "skip_back": {"10"}, "skip_forward": {"60"},
			"auto_rewind": {"0", "1"}, "next_in_series": {"0"}, "player_tint": {"0", "1"}, "big_buttons": {"0", "1"}, "text_size": {"standard"}}), sweepSame(303), ""),
		sweepPost("POST /me/reader-settings", sweepStatic("/me/reader-settings"), sweepFormOf(url.Values{"size": {"20"}, "word": {"0", "1"}}), sweepSame(303), "/"),
		sweepPost("POST /me/nav", sweepStatic("/me/nav"), sweepFormOf(url.Values{"nav": {"full"}}), sweepSame(303), ""),
		sweepPost("POST /me/library-view", sweepStatic("/me/library-view"), sweepFormOf(url.Values{"view": {"grid"}, "next": {"/libraries?sort=title"}}), sweepSame(303), "/libraries?sort=title"),
		sweepPost("POST /me/words/{id}/delete", sweepStatic("/me/words/wd_none/delete"), nil, sweepSame(404), ""),
		sweepPost("POST /me/reading/goal", sweepStatic("/me/reading/goal"), sweepFormOf(url.Values{"goal_books": {"12"}}), sweepSame(303), "/me/reading"),
		sweepPost("POST /hx/me/nav", sweepStatic("/hx/me/nav"), sweepFormOf(url.Values{"nav": {"full"}}), sweepSame(200), ""),
		sweepPost("POST /books/{id}/feed", sweepBookPath("/feed"), nil, sweepStaff(200, 404), ""),
		sweepPost("POST /series/{id}/feed", id(func(e *sweepEnv) string { return e.series }, "/series/", "/feed"), nil, sweepStaff(200, 404), ""),
		sweepPost("POST /me/feeds/{id}/revoke", sweepStatic("/me/feeds/zz-none/revoke"), nil, sweepSame(404), ""),
		sweepPost("POST /me/app-keys", sweepStatic("/me/app-keys"), sweepFormOf(url.Values{"kind": {"web"}}), sweepSame(400), ""),
		sweepPost("POST /me/password", sweepStatic("/me/password"), sweepFormOf(url.Values{"current": {"not the password at all"}, "password": {"a brand new passphrase"}}), sweepSame(422), ""),
		sweepPost("POST /me/sessions/{sid}/signout", func(e *sweepEnv, role int) string { return "/me/sessions/" + e.secondS[role] + "/signout" }, nil, sweepSame(303), "/me/devices"),
		sweepPost("POST /me/sessions/{sid}/name", func(e *sweepEnv, role int) string { return "/me/sessions/" + e.secondS[role] + "/name" },
			sweepFormOf(url.Values{"name": {"Kitchen laptop"}}), sweepSame(303), "/me/devices"),
		sweepPost("POST /me/sessions/signout-others", sweepStatic("/me/sessions/signout-others"), nil, sweepSame(303), ""),
		// Shelves, the person's own
		sweepPost("POST /shelves", sweepStatic("/shelves"), sweepFormOf(url.Values{"name": {"Sweep new shelf"}}), shelfPostWant, ""),
		sweepPost("POST /books/{id}/to-read", sweepBookPath("/to-read"), nil, shelfBookWant, ""),
		sweepPost("POST /shelves/{id}/rename", shelfOf("/rename", false), sweepFormOf(url.Values{"name": {"Renamed"}}), shelfPostWant, ""),
		// Only staff share their own shelf; a member's own shelf and a guest's are 404.
		sweepPost("POST /shelves/{id}/share", shelfOf("/share", false), sweepFormOf(url.Values{"shared": {"1"}}), sweepStaff(http.StatusSeeOther, http.StatusNotFound), ""),
		sweepPost("POST /shelves/{id}/delete", shelfOf("/delete", false), nil, shelfPostWant, ""),
		sweepPost("POST /shelves/{id}/items", shelfOf("/items", false), func(e *sweepEnv, _ int) url.Values { return url.Values{"book_id": {e.book.ID}} }, shelfBookWant, ""),
		sweepPost("POST /shelves/{id}/items/{bookID}/remove", func(e *sweepEnv, role int) string {
			return "/shelves/" + e.ownShelf(role) + "/items/" + e.book.ID + "/remove"
		}, nil, shelfPostWant, ""),
		sweepPost("POST /shelves/{id}/items/{bookID}/move", func(e *sweepEnv, role int) string {
			return "/shelves/" + e.ownShelf(role) + "/items/" + e.book.ID + "/move"
		}, sweepFormOf(url.Values{"dir": {"up"}}), shelfBookWant, ""),
	}
	// Bookmarks (P1c-7a): the owner and the admin use their own bookmark; the member owns one on a
	// book they cannot listen to (404 proves the book check); the guest points at the owner's.
	bookmarkOf := func(suffix string, other bool) func(*sweepEnv, int) string {
		return func(e *sweepEnv, role int) string {
			i := role
			switch {
			case other:
				i = (role + 1) % 3
			case role > sweepMember:
				i = sweepOwner
			}
			return "/bookmarks/" + e.bookmark[i] + suffix
		}
	}
	noteForm := sweepFormOf(url.Values{"note": {"x"}})
	post = append(post,
		sweepPost("POST /books/{id}/bookmarks", sweepBookPath("/bookmarks"), sweepFormOf(url.Values{"position_ms": {"1000"}}), sweepStaff(303, 404), ""),
		sweepPost("POST /bookmarks/{id}/note", bookmarkOf("/note", false), noteForm, sweepStaff(303, 404), ""),
		sweepPost("POST /bookmarks/{id}/delete", bookmarkOf("/delete", false), nil, sweepStaff(303, 404), ""),
	)
	for _, p := range []struct {
		pattern, suffix string
		form            func(*sweepEnv, int) url.Values
	}{{"POST /bookmarks/{id}/note", "/note", noteForm}, {"POST /bookmarks/{id}/delete", "/delete", nil}} {
		r := sweepPost(p.pattern, bookmarkOf(p.suffix, true), p.form, sweepSame(404), "")
		r.Label = "someone else's bookmark"
		post = append(post, r)
	}
	// Somebody else's session id and somebody else's shelf: 404 for everyone.
	other := sweepPost("POST /me/sessions/{sid}/signout", func(e *sweepEnv, role int) string {
		return "/me/sessions/" + e.secondS[(role+1)%4] + "/signout"
	}, nil, sweepSame(404), "")
	other.Label = "someone else's session"
	post = append(post, other)
	otherName := sweepPost("POST /me/sessions/{sid}/name", func(e *sweepEnv, role int) string {
		return "/me/sessions/" + e.secondS[(role+1)%4] + "/name"
	}, sweepFormOf(url.Values{"name": {"Stolen"}}), sweepSame(404), "")
	otherName.Label = "someone else's session"
	post = append(post, otherName)
	for _, p := range []struct {
		pattern, suffix string
		form            func(*sweepEnv, int) url.Values
	}{
		{"POST /shelves/{id}/rename", "/rename", sweepFormOf(url.Values{"name": {"Stolen"}})},
		{"POST /shelves/{id}/share", "/share", sweepFormOf(url.Values{"shared": {"1"}})},
		{"POST /shelves/{id}/delete", "/delete", nil},
		{"POST /shelves/{id}/items", "/items", func(e *sweepEnv, _ int) url.Values { return url.Values{"book_id": {e.book.ID}} }},
	} {
		r := sweepPost(p.pattern, shelfOf(p.suffix, true), p.form, sweepSame(404), "")
		r.Label = "someone else's shelf"
		post = append(post, r)
	}
	for _, g := range []struct {
		suffix string
		form   func(*sweepEnv, int) url.Values
	}{{"remove", nil}, {"move", sweepFormOf(url.Values{"dir": {"up"}})}} {
		suffix := g.suffix
		r := sweepPost("POST /shelves/{id}/items/{bookID}/"+suffix, func(e *sweepEnv, role int) string {
			return "/shelves/" + e.otherShelf(role) + "/items/" + e.book.ID + "/" + suffix
		}, g.form, sweepSame(404), "")
		r.Label = "someone else's shelf"
		post = append(post, r)
	}
	other = sweepGet("GET /shelves/{id}", shelfOf("", true), sweepSame(404))
	other.Label = "someone else's shelf"
	rows = append(rows, other)
	other = sweepGet("GET /shelves/{id}/delete", shelfOf("/delete", true), sweepSame(404))
	other.Label = "someone else's shelf"
	rows = append(rows, other)
	other = sweepGet("GET /shelves/{id}/download-list", shelfOf("/download-list", true), sweepSame(404))
	other.Label = "someone else's shelf"
	rows = append(rows, other)

	// The method guard of /covers/{bookID}: only the owner's answer is part of the contract.
	cover := sweepPost("/covers/{bookID}", id(bookID, "/covers/", ""), nil, [4]int{http.StatusMethodNotAllowed, 0, 0, 0}, "")
	cover.Label = "POST"
	post = append(post, cover)
	stream := sweepPost("/stream/{audioFileID}", sweepAudioPath(0, ""), nil, [4]int{http.StatusMethodNotAllowed, 0, 0, 0}, "")
	stream.Label = "POST"
	post = append(post, stream)
	offline := sweepPost("/offline", sweepStatic("/offline"), nil, [4]int{http.StatusMethodNotAllowed, 0, 0, 0}, "")
	offline.Label = "POST"
	post = append(post, offline)
	peaks := sweepPost("/peaks/{audioFileID}", sweepPeaksPath(0), nil, [4]int{http.StatusMethodNotAllowed, 0, 0, 0}, "")
	peaks.Label = "POST"
	post = append(post, peaks)
	// Progress (P1c-3)
	post = append(post,
		sweepPost("POST /progress/{bookID}", id(bookID, "/progress/", ""), sweepFormOf(url.Values{
			"position_ms": {"2000"}, "client_updated_at": {"1800000000000"}, "device_id": {"sweepdevice02"}}), sweepStaff(200, 404), ""),
		sweepPost("POST /hx/books/{id}/finished", id(bookID, "/hx/books/", "/finished"), sweepFormOf(url.Values{
			"finished": {"1"}, "next": {"/"}}), sweepStaff(303, 404), "/"),
		sweepPost("POST /hx/books/{id}/speed", id(bookID, "/hx/books/", "/speed"), sweepFormOf(url.Values{"speed": {"1.25"}}), sweepStaff(204, 404), ""))
	// Up next (Q1): autoplay with nothing queued is 204 for every role; the form is empty.
	post = append(post,
		sweepPost("POST /hx/player/next", sweepStatic("/hx/player/next"), nil, sweepSame(204), ""),
		// Up next (Q2): the person's own list is open to everyone signed in (hidden books never show);
		// the changes need the book: staff 303, a member and a guest without a grant 404. Move and
		// remove find the book already queued (sweepQueuedPath), a hidden one too for those two.
		sweepGet("GET /queue", sweepStatic("/queue"), sweepSame(200)),
		sweepGet("GET /hx/queue", sweepStatic("/hx/queue"), sweepSame(200)),
		sweepPost("POST /queue", sweepStatic("/queue"), func(e *sweepEnv, _ int) url.Values {
			return url.Values{"book_id": {e.book.ID}, "where": {"end"}}
		}, sweepStaff(http.StatusSeeOther, http.StatusNotFound), ""),
		sweepPost("POST /queue/{bookID}/move", sweepQueuedPath("/move"),
			sweepFormOf(url.Values{"dir": {"down"}}), sweepStaff(http.StatusSeeOther, http.StatusNotFound), ""),
		sweepPost("POST /queue/{bookID}/delete", sweepQueuedPath("/delete"),
			nil, sweepStaff(http.StatusSeeOther, http.StatusNotFound), ""))
	rows = append(rows, post...)
	rows = append(rows, usersSweepRows()...)
	rows = append(rows, signInSweepRows()...)
	rows = append(rows, invitesSweepRows()...)
	rows = append(rows, shareSweepRows()...)
	rows = append(rows, detailsSweepRows()...)
	rows = append(rows, coverSweepRows()...)
	rows = append(rows, writeSweepRows()...)
	rows = append(rows, chapterSweepRows()...)
	rows = append(rows, uploadSweepRows()...)
	rows = append(rows, ebookSweepRows()...)
	rows = append(rows, ebookInboxSweepRows()...)
	rows = append(rows, devicesSweepRows()...)
	rows = append(rows, pairSweepRows()...)
	rows = append(rows, emailSweepRows()...)
	rows = append(rows, oidcSweepRows()...)
	rows = append(rows, readerSweepRows()...)
	rows = append(rows, backupsSweepRows()...)
	rows = append(rows, watchSweepRows()...)
	rows = append(rows, dupSweepRows()...)
	rows = append(rows, healthSweepRows()...)
	rows = append(rows, activitySweepRows()...)
	rows = append(rows, apiSweepRows()...)
	return append(rows, linksSweepRows()...)
}

// sweepLeakWords are strings of the seeded book that nobody without access may ever see.
func (e *sweepEnv) leakWords() []string {
	return []string{"Sweepword", "Sweepauthor", "Sweepvoice", "Sweepseries", "Sweepnote", e.book.ID}
}

// send makes the request of a row as a role (role -1 is the anonymous visitor) and returns the answer.
func (e *sweepEnv) send(t *testing.T, row sweepRow, role int, token bool) *httptest.ResponseRecorder {
	t.Helper()
	pathRole := max(role, 0)
	path := row.Path(e, pathRole)
	var hx, anon func(*http.Request)
	if row.HX {
		hx = func(r *http.Request) { r.Header.Set("HX-Request", "true") }
	}
	if row.Addr != "" {
		anon = func(r *http.Request) { r.RemoteAddr = row.Addr }
		prev := hx
		hx = func(r *http.Request) {
			if prev != nil {
				prev(r)
			}
			anon(r)
		}
	}
	if row.Method == http.MethodGet {
		if role < 0 {
			return e.a.do(http.MethodGet, path, nil, anon)
		}
		return e.a.do(http.MethodGet, path, nil, hx, e.ck[role])
	}
	form := url.Values{}
	if row.Form != nil {
		for k, v := range row.Form(e, pathRole) {
			form[k] = v
		}
	}
	if role < 0 {
		c, tok := e.a.anon(t, "/login")
		if token {
			form.Set("_csrf", tok)
		}
		return e.a.do(row.Method, path, form, anon, c)
	}
	if token {
		form.Set("_csrf", e.a.tokenFor(t, e.ck[role]))
	}
	return e.a.do(row.Method, path, form, hx, e.ck[role])
}

// check compares one answer with the row's expectation.
func (e *sweepEnv) check(t *testing.T, row sweepRow, role int, rec *httptest.ResponseRecorder) {
	t.Helper()
	who := "anonymous"
	want, wantLoc := row.Anon, row.AnonLoc
	if role >= 0 {
		who, want, wantLoc = sweepRoleNames[role], row.Want[role], ""
		if e.granted && row.Granted[role] != 0 {
			want = row.Granted[role]
		}
		if want == http.StatusSeeOther {
			wantLoc = row.Loc
			if row.LocOf != nil {
				wantLoc = row.LocOf(e)
			}
		}
	} else if want == 0 {
		want = http.StatusSeeOther
		if wantLoc == "" {
			wantLoc = "/login"
			if row.Method == http.MethodGet {
				p := row.Path(e, 0)
				if p != "/" && !strings.HasPrefix(p, "/setup") {
					wantLoc = "/login?next=" + url.QueryEscape(p)
				}
			}
		}
	}
	if want == 0 {
		return
	}
	if rec.Code != want {
		t.Errorf("%s %s %s: got %d, want %d", who, row.Method, row.Path(e, max(role, 0)), rec.Code, want)
		return
	}
	if wantLoc != "" && location(rec) != wantLoc {
		t.Errorf("%s %s %s: Location %q, want %q", who, row.Method, row.Path(e, max(role, 0)), location(rec), wantLoc)
	}
	if role >= sweepMember && !e.granted {
		body := rec.Body.String()
		for _, w := range e.leakWords() {
			if strings.Contains(body, w) {
				t.Errorf("%s %s %s: the answer contains %q", who, row.Method, row.Path(e, role), w)
			}
		}
	}
	if role >= 0 && row.Body != nil && rec.Code == http.StatusOK && !e.granted {
		row.Body(t, e, role, rec.Body.String())
	}
}

// sweepRegistered lists the patterns the server registers outside the dev tag.
func sweepRegistered(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?:HandleFunc|Handle)\("([^"]+)"`)
	seen := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, "_templ.go") ||
			f == "routes_kosync.go" { // KOReader's own protocol under /kosync, no cookie: kosync_test.go covers it
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(src), -1) {
			p := m[1]
			path := p
			if i := strings.IndexByte(p, ' '); i >= 0 {
				path = p[i+1:]
			}
			switch {
			case p == "/", p == "GET /static/", p == "GET /healthz", p == "/sw.js", p == "/manifest.webmanifest",
				strings.HasPrefix(path, "/dev/"), strings.HasPrefix(path, "/hx/dev/"),
				path == "/opds" || strings.HasPrefix(path, "/opds/"): // app-key sign-in, not a cookie: opds_test.go covers it
				continue
			case path == "/feeds/" || strings.HasPrefix(path, "/feeds/"):
				continue // feed token in the path, not a cookie: feeds_test.go covers it
			}
			seen[p] = true
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func TestRouteMatrix(t *testing.T) {
	rows := sweepRows()

	// Completeness: a registered route without a row fails.
	covered := map[string]bool{}
	for _, r := range rows {
		covered[r.Pattern] = true
	}
	registered := sweepRegistered(t)
	var missing []string
	for _, p := range registered {
		if !covered[p] {
			missing = append(missing, p)
		}
	}
	if len(missing) > 0 {
		t.Errorf("registered routes with no row in the route matrix: %s", strings.Join(missing, ", "))
	}
	for p := range covered {
		if i := sort.SearchStrings(registered, p); i == len(registered) || registered[i] != p {
			t.Errorf("the matrix has a row for %q, which is not registered", p)
		}
	}
	gets := 0
	for _, r := range rows {
		if r.Method == http.MethodGet {
			gets++
		}
	}
	// Each GET row is sent 5 times (four people and a visitor); each unsafe row 10 times (also without a token).
	t.Logf("%d registered patterns, %d rows (%d GET, %d unsafe), %d requests", len(registered), len(rows), gets, len(rows)-gets, gets*5+(len(rows)-gets)*10)

	// Safe rows share one seeded app: nothing here writes.
	shared := sweepSeed(t)
	for _, row := range rows {
		if row.Method != http.MethodGet {
			continue
		}
		t.Run(row.name(), func(t *testing.T) {
			for role := range sweepRoles {
				shared.check(t, row, role, shared.send(t, row, role, true))
			}
			shared.check(t, row, -1, shared.send(t, row, -1, true))
		})
	}

	// One htmx variant per area: a visitor who is not signed in is sent away without a swap.
	for _, p := range []string{"/libraries", "/hx/libraries/items", "/admin/libraries"} {
		rec := shared.a.do(http.MethodGet, p, nil, hx)
		if rec.Code != http.StatusNoContent || !strings.HasPrefix(rec.Header().Get("HX-Redirect"), "/login") {
			t.Errorf("anonymous htmx GET %s: %d, HX-Redirect %q", p, rec.Code, rec.Header().Get("HX-Redirect"))
		}
	}

	// Unsafe rows: a fresh seeded app for every person and row, so no row sees another's effects.
	// Seeding is the slow part and touches only the row's own apps, so the rows run in parallel; the
	// requests to the one shared app go one at a time, as they did when the rows ran in sequence.
	var sharedMu sync.Mutex
	for _, row := range rows {
		if row.Method == http.MethodGet {
			continue
		}
		t.Run(row.name(), func(t *testing.T) {
			t.Parallel()
			for role := range sweepRoles {
				e := sweepSeed(t)
				e.check(t, row, role, e.send(t, row, role, true))
			}
			sharedMu.Lock()
			defer sharedMu.Unlock()
			shared.check(t, row, -1, shared.send(t, row, -1, true))
			// Without the token nothing happens, whoever asks.
			if row.NoCSRF {
				for role := range sweepRoles {
					shared.check(t, row, role, shared.send(t, row, role, false))
				}
				shared.check(t, row, -1, shared.send(t, row, -1, false))
				return
			}
			for role := range sweepRoles {
				if rec := shared.send(t, row, role, false); rec.Code != http.StatusForbidden {
					t.Errorf("%s %s without a token: %d, want 403", sweepRoleNames[role], row.Path(shared, role), rec.Code)
				}
			}
			if rec := shared.send(t, row, -1, false); rec.Code != http.StatusForbidden {
				t.Errorf("anonymous %s without a token: %d, want 403", row.Path(shared, 0), rec.Code)
			}
		})
	}
}

func TestShelvesEmptyShelfPlaceholder(t *testing.T) {
	a, _, books := shelfApp(t)
	ck := a.session
	empty := shelfMake(t, a, ck, "Nothing yet")
	full := shelfMake(t, a, ck, "Has a book")
	shelfAdd(t, a, ck, full, books[0].ID)
	_ = empty

	body := browseGet(a, "/shelves", false, ck).Body.String()
	rowOf := func(name string) string {
		for _, r := range strings.Split(body, `<a class="ts-row ts-shelf-row"`)[1:] {
			r = r[:strings.Index(r, "</a>")]
			if strings.Contains(r, ">"+name+"<") {
				return r
			}
		}
		t.Fatalf("no row for %q", name)
		return ""
	}
	blank := `<span class="ts-shelf-row__blank" aria-hidden="true">`
	e := rowOf("Nothing yet")
	if !strings.Contains(e, `<span class="ts-shelf-row__stack">`+blank) || !strings.Contains(e, "<svg") || strings.Contains(e, `class="ts-cover`) {
		t.Errorf("an empty shelf lacks the placeholder in the stack: %s", e)
	}
	if f := rowOf("Has a book"); strings.Contains(f, "ts-shelf-row__blank") || !strings.Contains(f, `class="ts-cover`) {
		t.Errorf("a shelf with a book shows the placeholder: %s", f)
	}
	css, err := os.ReadFile("static/css/pages-shelves.css")
	if err != nil {
		t.Fatal(err)
	}
	rule := string(css[strings.Index(string(css), ".ts-shelf-row__blank"):])
	rule = rule[:strings.Index(rule, "}")]
	for _, want := range []string{"inline-size: 2.25rem", "block-size: 2.25rem", "var(--surface)", "var(--line)", "var(--radius-cover)"} {
		if !strings.Contains(rule, want) {
			t.Errorf("the placeholder rule lacks %q", want)
		}
	}
}

// sweepQueuedPath is /queue/{book}{suffix} once the role's queue holds the book. The member and the
// guest cannot see it, so their row proves a hidden queued book is 404, not "not queued".
func sweepQueuedPath(suffix string) func(*sweepEnv, int) string {
	return func(e *sweepEnv, role int) string {
		if _, err := e.a.lib.QueueAdd(e.t.Context(), e.uid[role], e.book.ID, false); err != nil {
			e.t.Fatal(err)
		}
		return "/queue/" + e.book.ID + suffix
	}
}
