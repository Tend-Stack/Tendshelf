package web

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// linkEnv is an app with an owner, a book of two playable WAV files and a copy-protected third, and
// a second book: the links are made for the first, so the second proves a link reaches one book.
type linkEnv struct {
	t         *testing.T
	a         *testApp
	owner     *http.Cookie
	ownerUser users.User
	book      library.Book
	other     library.Book
	files     []library.AudioFile // book: two playable, then the protected one
	otherFile library.AudioFile
}

func linkNewEnv(t *testing.T) *linkEnv {
	t.Helper()
	a := newTestApp(t)
	e := &linkEnv{t: t, a: a, owner: a.owner(t)}
	ctx := context.Background()
	u, err := a.srv.users.ByID(ctx, sweepUserID(t, a, e.owner))
	if err != nil {
		t.Fatal(err)
	}
	e.ownerUser = u
	_, f := librarytest.NewLibrary(t, a.lib, "Link library", t.TempDir())
	e.book = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Linkword Lantern", Authors: []string{"Ines Linkauthor"}, DurationMs: 9000, Files: 3})
	e.other = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Otherword Garden", DurationMs: 3000, Files: 1})
	e.files = e.wavs(e.book.ID, 3)
	e.otherFile = e.wavs(e.other.ID, 1)[0]
	// The third file of the first book is copy-protected by its extension.
	p := e.files[2]
	b, err := os.ReadFile(p.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path+".aax", b, 0o644); err != nil {
		t.Fatal(err)
	}
	streamExec(t, a, `UPDATE audio_files SET path=? WHERE id=?`, p.Path+".aax", p.ID)
	sha := fmt.Sprintf("%064x", 0xbeef01)
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
	peaksAttach(t, a)
	peaksWrite(t, a, e.files[0])
	return e
}

// wavs writes n 3 s WAV files for the book and makes the rows match.
func (e *linkEnv) wavs(bookID string, n int) []library.AudioFile {
	e.t.Helper()
	files, err := e.a.lib.AudioFiles(context.Background(), bookID)
	if err != nil || len(files) != n {
		e.t.Fatalf("audio files = %d, %v", len(files), err)
	}
	for i, f := range files {
		if err := wav.Write(f.Path, 3000, 440+i*100); err != nil {
			e.t.Fatal(err)
		}
		fi, err := os.Stat(f.Path)
		if err != nil {
			e.t.Fatal(err)
		}
		streamExec(e.t, e.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3000, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			i*3000, fi.Size(), fi.ModTime().UnixNano(), f.ID)
	}
	files, err = e.a.lib.AudioFiles(context.Background(), bookID)
	if err != nil {
		e.t.Fatal(err)
	}
	return files
}

// make creates a link for the book as the owner and returns it with its raw token. pw "" is none.
func (e *linkEnv) make(bookID string, days int, pw string) (share.Link, string) {
	e.t.Helper()
	ctx := context.Background()
	hash := ""
	if pw != "" {
		var err error
		if hash, err = e.a.srv.users.HashPassword(ctx, pw); err != nil {
			e.t.Fatal(err)
		}
	}
	var l share.Link
	var tok string
	err := e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		l, tok, err = e.a.srv.links.Create(ctx, tx, e.ownerUser, share.LinkSpec{BookID: bookID, Days: days, Label: "Tia"}, hash)
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return l, tok
}

func (e *linkEnv) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return e.a.do(http.MethodGet, path, nil, nil, cookies...)
}

func linkAssertHeaders(t *testing.T, rec *httptest.ResponseRecorder, cache string) {
	t.Helper()
	h := rec.Header()
	if h.Get("Referrer-Policy") != "same-origin" || h.Get("X-Robots-Tag") != "noindex" || h.Get("Cache-Control") != cache {
		t.Errorf("headers: Referrer-Policy %q, X-Robots-Tag %q, Cache-Control %q (want %q)",
			h.Get("Referrer-Policy"), h.Get("X-Robots-Tag"), h.Get("Cache-Control"), cache)
	}
}

func TestLinkPage(t *testing.T) {
	e := linkNewEnv(t)
	_, tok := e.make(e.book.ID, 7, "")
	base := "/s/" + tok
	rec := e.get(base)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d", rec.Code)
	}
	linkAssertHeaders(t, rec, "no-store")
	body := rec.Body.String()
	for _, want := range []string{"Linkword Lantern", "Ines Linkauthor", `data-ts-link="` + base + `"`, `href="` + base + "/stream/" + e.files[0].ID + `#t=0"`,
		`id="player-data"`, `data-cover-url="` + base + "/cover?w=512", base + "/cover?w=640", "This link works until", "kept in this browser only"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
	// No person, no account, no way into the rest of the app.
	for _, bad := range []string{"Ana Reyes", "ana@example.org", "Tia", `href="/books/`, `/progress`, `/hx/`, e.other.ID, "Otherword"} {
		if strings.Contains(body, bad) {
			t.Errorf("the page contains %q", bad)
		}
	}
	// The player's account-only controls are hidden.
	if !strings.Contains(body, "data-ts-full-finish hidden") {
		t.Errorf("the mark-as-finished form is not hidden")
	}
	// HEAD answers like GET.
	if h := e.a.do(http.MethodHead, base, nil, nil); h.Code != http.StatusOK {
		t.Errorf("HEAD: %d", h.Code)
	}
}

func TestLinkBrokenPagesAreIdentical(t *testing.T) {
	e := linkNewEnv(t)
	ctx := context.Background()
	unknown := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	mk := func() (share.Link, string) { return e.make(e.book.ID, 7, "") }

	_, revoked := mk()
	streamExec(t, e.a, `UPDATE share_links SET revoked_at = created_at WHERE id = (SELECT id FROM share_links ORDER BY created_at DESC, id DESC LIMIT 1)`)
	l, expired := mk()
	streamExec(t, e.a, `UPDATE share_links SET expires_at = created_at + 1 WHERE id = ?`, l.ID)
	demoted := func() string {
		admin, err := e.a.srv.users.ByID(ctx, sweepUserID(t, e.a, e.a.as(t, users.RoleAdmin)))
		if err != nil {
			t.Fatal(err)
		}
		var tok string
		if err := e.a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			var err error
			_, tok, err = e.a.srv.links.Create(ctx, tx, admin, share.LinkSpec{BookID: e.book.ID, Days: 7}, "")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		streamExec(t, e.a, `UPDATE users SET role = 'member' WHERE id = ?`, admin.ID)
		return tok
	}()
	_, refusedBook := e.make(e.other.ID, 7, "")
	streamExec(t, e.a, `UPDATE books SET refused_reason = 'drm' WHERE id = ?`, e.other.ID)
	_, missing := e.make(e.book.ID, 7, "")
	streamExec(t, e.a, `UPDATE books SET missing_since = 5 WHERE id = ?`, e.book.ID)
	// ("missing" also breaks the other links to the first book; that is the point of the case.)

	var first string
	for name, tok := range map[string]string{"unknown": unknown, "malformed": "short", "revoked": revoked, "expired": expired,
		"creator demoted": demoted, "refused book": refusedBook, "missing book": missing} {
		for _, cookies := range [][]*http.Cookie{nil, {e.owner}} {
			rec := e.get("/s/"+tok, cookies...)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s: %d", name, rec.Code)
			}
			linkAssertHeaders(t, rec, "no-store")
			if first == "" {
				first = rec.Body.String()
			} else if rec.Body.String() != first {
				t.Errorf("%s: the page differs from the others", name)
			}
			if !strings.Contains(rec.Body.String(), "This link doesn&#39;t work") && !strings.Contains(rec.Body.String(), "This link doesn't work") {
				t.Errorf("%s: wrong words: %.200s", name, rec.Body.String())
			}
		}
	}
	// A POST to a broken link is the same page.
	c, csrf := e.a.anon(t, "/login")
	rec := e.a.do(http.MethodPost, "/s/"+revoked, url.Values{"_csrf": {csrf}, "password": {"x"}}, nil, c)
	if rec.Code != http.StatusNotFound || rec.Body.String() != first {
		t.Errorf("POST to a revoked link: %d", rec.Code)
	}
}

func TestLinkMedia(t *testing.T) {
	e := linkNewEnv(t)
	_, tok := e.make(e.book.ID, 7, "")
	base := "/s/" + tok

	rec := e.a.do(http.MethodGet, base+"/stream/"+e.files[0].ID, nil, func(r *http.Request) { r.Header.Set("Range", "bytes=0-99") })
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 {
		t.Fatalf("ranged stream: %d, %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("Referrer-Policy") != "same-origin" || rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Errorf("stream headers: %v", rec.Header())
	}
	if rec := e.get(base + "/stream/" + e.files[1].ID); rec.Code != http.StatusOK {
		t.Errorf("second file: %d", rec.Code)
	}
	for name, path := range map[string]string{
		"another book's file":       base + "/stream/" + e.otherFile.ID,
		"the same book's protected": base + "/stream/" + e.files[2].ID,
		"an unknown file":           base + "/stream/nope",
		"another book's peaks":      base + "/peaks/" + e.otherFile.ID,
		"protected peaks":           base + "/peaks/" + e.files[2].ID,
		"an unknown token":          "/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA/stream/" + e.files[0].ID,
	} {
		rec := e.get(path)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
		linkAssertHeaders(t, rec, "no-store")
	}
	// A signed-in owner gets no more than the link gives.
	if rec := e.get(base+"/stream/"+e.otherFile.ID, e.owner); rec.Code != http.StatusNotFound {
		t.Errorf("owner, another book's file through a link: %d", rec.Code)
	}
	if rec := e.get(base + "/stream/" + e.files[0].ID + "?compat=2"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad compat: %d", rec.Code)
	}
	// Peaks.
	pk := e.get(base + "/peaks/" + e.files[0].ID)
	if pk.Code != http.StatusOK || pk.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("peaks: %d %q", pk.Code, pk.Header().Get("Cache-Control"))
	}
	// Cover.
	cv := e.get(base + "/cover?w=64")
	if cv.Code != http.StatusOK || cv.Header().Get("Cache-Control") != "private, no-store" || !strings.HasPrefix(cv.Header().Get("Content-Type"), "image/") {
		t.Errorf("cover: %d %v", cv.Code, cv.Header())
	}
	for _, w := range []string{"0", "-1", "99999", "x"} {
		if rec := e.get(base + "/cover?w=" + w); rec.Code != http.StatusBadRequest {
			t.Errorf("cover w=%s: %d", w, rec.Code)
		}
	}
	// Revoked: everything stops, for the open tab too.
	streamExec(t, e.a, `UPDATE share_links SET revoked_at = created_at`)
	for _, p := range []string{base + "/stream/" + e.files[0].ID, base + "/peaks/" + e.files[0].ID, base + "/cover?w=64", base} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("revoked %s: %d", p[len(base):], rec.Code)
		}
	}
	// A revoked link in an open tab does not count against the sign-in limiter (D9): 40 requests
	// later the address can still be used.
	for i := 0; i < 40; i++ {
		e.get(base + "/stream/" + e.files[0].ID)
	}
	if err := e.a.srv.auth.LinkAllowed(netip.MustParseAddr("192.0.2.1"), ""); err != nil {
		t.Errorf("revoked-link requests locked the address: %v", err)
	}
}

func TestLinkSessionChangesNothing(t *testing.T) {
	e := linkNewEnv(t)
	_, tok := e.make(e.book.ID, 7, "")
	member := e.a.as(t, users.RoleMember) // can see nothing
	for _, ck := range []*http.Cookie{nil, e.owner, member} {
		rec := e.get("/s/"+tok, ck)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Otherword") {
			t.Errorf("page with cookie %v: %d", ck != nil, rec.Code)
		}
	}
	// Nothing else under /s/ is public: it goes to sign-in like any other path.
	for _, p := range []string{"/s/", "/s/" + tok + "/other", "/s/" + tok + "/stream", "/s/" + tok + "/stream/a/b", "/s/" + tok + "/cover/"} {
		rec := e.get(p)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
			t.Errorf("%s: %d %q", p, rec.Code, location(rec))
		}
	}
	// The link page never touches progress or bookmarks.
	if n := e.a.count(t, `SELECT count(*) FROM progress`); n != 0 {
		t.Errorf("%d progress rows", n)
	}
}

func TestLinkPasswordFlow(t *testing.T) {
	e := linkNewEnv(t)
	l, tok := e.make(e.book.ID, 7, "correct horse")
	base := "/s/" + tok
	locked := e.get(base)
	if locked.Code != http.StatusOK {
		t.Fatalf("locked page: %d", locked.Code)
	}
	linkAssertHeaders(t, locked, "no-store")
	lb := locked.Body.String()
	for _, bad := range []string{"Linkword", "Linkauthor", "/cover", "/stream/", "player-data", e.files[0].ID} {
		if strings.Contains(lb, bad) {
			t.Errorf("the locked page contains %q", bad)
		}
	}
	for _, want := range []string{`type="password"`, `autocomplete="current-password"`, "This book has a password", `name="_csrf"`} {
		if !strings.Contains(lb, want) {
			t.Errorf("the locked page lacks %q", want)
		}
	}
	for _, p := range []string{base + "/stream/" + e.files[0].ID, base + "/peaks/" + e.files[0].ID, base + "/cover?w=64"} {
		if rec := e.get(p); rec.Code != http.StatusNotFound {
			t.Errorf("locked %s: %d", p[len(base):], rec.Code)
		}
	}
	csrf := cookieNamed(locked, csrfCookie)
	token := pageToken(t, lb)
	post := func(base, pw string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		return e.a.do(http.MethodPost, base, url.Values{"_csrf": {token}, "password": {pw}}, nil, append([]*http.Cookie{csrf}, cookies...)...)
	}

	// Wrong: 422 with the words, no cookie, nothing leaked.
	w := post(base, "nope nope nope")
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "That password isn&#39;t right.") && !strings.Contains(w.Body.String(), "That password isn't right.") {
		t.Fatalf("wrong password: %d", w.Code)
	}
	if cookieNamed(w, linkCookie) != nil || strings.Contains(w.Body.String(), "Linkword") {
		t.Errorf("a wrong password set a cookie or showed the book")
	}
	// Ten wrong close this link for everyone; another link still works.
	_, other := e.make(e.book.ID, 7, "another pass")
	for i := 1; i < 10; i++ {
		if w := post(base, "wrong "+strconv.Itoa(i)+" pass"); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("wrong #%d: %d", i+1, w.Code)
		}
	}
	for name, rec := range map[string]*httptest.ResponseRecorder{"post, right password": post(base, "correct horse"), "page": e.get(base)} {
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Errorf("closed link, %s: %d", name, rec.Code)
		}
	}
	if rec := e.get("/s/" + other); rec.Code != http.StatusOK {
		t.Errorf("another link while this one is closed: %d", rec.Code)
	}
	// Ten more wrong ones on the other link bring the address to twenty: everything is closed.
	for i := 0; i < 10; i++ {
		post("/s/"+other, "wrong "+strconv.Itoa(i)+" again")
	}
	if rec := e.get("/s/" + other); rec.Code != http.StatusTooManyRequests {
		t.Errorf("after twenty failures the address is closed: %d", rec.Code)
	}
	_ = l
}

func TestLinkUnlockCookie(t *testing.T) {
	e := linkNewEnv(t)
	l, tok := e.make(e.book.ID, 7, "correct horse")
	l2, tok2 := e.make(e.book.ID, 7, "other horse")
	base := "/s/" + tok
	locked := e.get(base)
	csrf := cookieNamed(locked, csrfCookie)
	token := pageToken(t, locked.Body.String())
	rec := e.a.do(http.MethodPost, base, url.Values{"_csrf": {token}, "password": {"correct horse"}}, nil, csrf)
	if rec.Code != http.StatusSeeOther || location(rec) != base {
		t.Fatalf("right password: %d %q", rec.Code, location(rec))
	}
	ck := cookieNamed(rec, linkCookie)
	if ck == nil || ck.Path != base || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge <= 0 || ck.MaxAge > 7*24*3600 {
		t.Fatalf("cookie: %+v", ck)
	}
	linkAssertHeaders(t, rec, "no-store")
	page := e.get(base, ck)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Linkword Lantern") {
		t.Fatalf("unlocked page: %d", page.Code)
	}
	if s := e.a.do(http.MethodGet, base+"/stream/"+e.files[0].ID, nil, func(r *http.Request) { r.Header.Set("Range", "bytes=0-9") }, ck); s.Code != http.StatusPartialContent {
		t.Errorf("unlocked stream: %d", s.Code)
	}
	if c := e.get(base+"/cover?w=64", ck); c.Code != http.StatusOK {
		t.Errorf("unlocked cover: %d", c.Code)
	}
	// Forged, expired, another link's and a cut-off cookie leave it locked.
	expMs := strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)
	past := strconv.FormatInt(time.Now().Add(-time.Hour).UnixMilli(), 10)
	s := e.a.srv
	for name, val := range map[string]string{
		"forged":        expMs + ".AAAA",
		"expired":       past + "." + s.linkMAC(l, past),
		"another link":  expMs + "." + s.linkMAC(l2, expMs),
		"cut":           expMs,
		"stretched end": strconv.FormatInt(time.Now().Add(100*time.Hour).UnixMilli(), 10) + "." + s.linkMAC(l, expMs),
		"garbage":       "x.y",
	} {
		bad := &http.Cookie{Name: linkCookie, Value: val}
		if rec := e.get(base+"/stream/"+e.files[0].ID, bad); rec.Code != http.StatusNotFound {
			t.Errorf("%s cookie streams: %d", name, rec.Code)
		}
		if rec := e.get(base, bad); strings.Contains(rec.Body.String(), "Linkword") {
			t.Errorf("%s cookie shows the book", name)
		}
	}
	// The cookie of one link is worth nothing on another, even when sent to it.
	if rec := e.get("/s/"+tok2, ck); strings.Contains(rec.Body.String(), "Linkword") {
		t.Errorf("a cookie unlocked another link")
	}
}

// linkLogs captures the default logger while the test runs.
func linkLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestLinkTokenNeverLogged(t *testing.T) {
	logs := linkLogs(t)
	e := linkNewEnv(t)
	l, tok := e.make(e.book.ID, 7, "correct horse")
	base := "/s/" + tok
	locked := e.get(base)
	csrf, token := cookieNamed(locked, csrfCookie), pageToken(t, locked.Body.String())
	e.a.do(http.MethodPost, base, url.Values{"_csrf": {token}, "password": {"wrong wrong"}}, nil, csrf)
	e.get(base + "/stream/" + e.files[0].ID)
	e.get("/s/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	streamExec(t, e.a, `UPDATE share_links SET revoked_at = created_at WHERE id = ?`, l.ID)
	e.get(base)
	if strings.Contains(logs.String(), tok) || strings.Contains(logs.String(), "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") {
		t.Errorf("a token reached the log: %s", logs.String())
	}
	var rows string
	if err := e.a.st.Reader().QueryRow(`SELECT coalesce(group_concat(action || ' ' || target || ' ' || detail_json, char(10)), '') FROM audit_log`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rows, tok) || strings.Contains(rows, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") || !strings.Contains(rows, "link.create") || !strings.Contains(rows, "link.failed") {
		t.Errorf("audit rows: %s", rows)
	}
	if strings.Contains(rows, "correct horse") || strings.Contains(rows, "wrong wrong") {
		t.Errorf("a password reached the audit log")
	}
	// The path in a log line is redacted.
	for p, want := range map[string]string{"/s/SECRET": "/s/…", "/s/SECRET/cover": "/s/…/cover", "/s/SECRET/stream/f1": "/s/…/stream/f1"} {
		if got := logPath(httptest.NewRequest(http.MethodGet, p, nil)); got != want {
			t.Errorf("logPath(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestLinkPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"/s/abc": true, "/s/abc/cover": true, "/s/abc/stream/f1": true, "/s/abc/peaks/f1": true,
		"/s/": false, "/s": false, "/s//cover": false, "/s/abc/": false, "/s/abc/cover/": false, "/s/abc/stream": false,
		"/s/abc/stream/": false, "/s/abc/stream/f1/x": false, "/s/abc/other": false, "/s/abc/other/f1": false, "/sx/abc": false,
	} {
		if got := isLinkPath(p); got != want {
			t.Errorf("isLinkPath(%q) = %v, want %v", p, got, want)
		}
	}
}
