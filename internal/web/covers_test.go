package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var coverLibs atomic.Int64

const coverSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// coverBook is a book with a cover row. The files named in files are written under the data
// directory: "orig" is the stored original, "160", "320" and "640" are thumbnails; each holds its own name.
func coverBook(t *testing.T, a *testApp, path string, files ...string) library.Book {
	t.Helper()
	_, f := librarytest.NewLibrary(t, a.lib, "Covers "+strconv.FormatInt(coverLibs.Add(1), 10), "/audio-"+strconv.FormatInt(coverLibs.Load(), 10))
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "With cover"})
	if path == "" {
		path = "covers/" + coverSHA + ".jpg"
	}
	c := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 800, Height: 800, SHA256: coverSHA, Path: path}
	err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&c); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, c.ID)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		p := filepath.Join(a.dir, path)
		if name != "orig" {
			box, err := strconv.Atoi(name)
			if err != nil {
				t.Fatal(err)
			}
			p = mediajobs.ThumbPath(a.dir, coverSHA, box)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("file-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// coverOpens replaces openCover with a recorder for the test and returns the paths opened so far.
func coverOpens(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var opened []string
	old := openCover
	openCover = func(name string) (*os.File, error) {
		mu.Lock()
		opened = append(opened, name)
		mu.Unlock()
		return old(name)
	}
	t.Cleanup(func() { openCover = old })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), opened...)
	}
}

func TestCoverSizes(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig", "160", "320", "640")
	for _, c := range []struct{ query, body, ctype string }{
		{"?w=100", "file-160", "image/jpeg"},
		{"?w=160", "file-160", "image/jpeg"},
		{"?w=161", "file-320", "image/jpeg"},
		{"?w=320", "file-320", "image/jpeg"},
		{"?w=500", "file-640", "image/jpeg"},
		{"?w=900", "file-orig", "image/png"},
		{"", "file-orig", "image/png"},
		{"?w=4096", "file-orig", "image/png"},
	} {
		rec := a.do(http.MethodGet, "/covers/"+b.ID+c.query, nil, nil, own)
		if rec.Code != http.StatusOK || rec.Body.String() != c.body || rec.Header().Get("Content-Type") != c.ctype {
			t.Errorf("%q: %d %q %q, want %q %q", c.query, rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"), c.body, c.ctype)
		}
	}
}

func TestCoverThumbFallback(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig", "320")
	for _, c := range []struct{ query, body string }{
		{"?w=100", "file-320"},  // the 160 file is absent: the next size
		{"?w=500", "file-orig"}, // 640 is absent too: the original
	} {
		rec := a.do(http.MethodGet, "/covers/"+b.ID+c.query, nil, nil, own)
		if rec.Code != http.StatusOK || rec.Body.String() != c.body {
			t.Errorf("%q: %d %q, want %q", c.query, rec.Code, rec.Body.String(), c.body)
		}
	}
	// Nothing on disk at all.
	a = newTestApp(t)
	own = a.owner(t)
	none := coverBook(t, a, "")
	if rec := a.do(http.MethodGet, "/covers/"+none.ID+"?w=100", nil, nil, own); rec.Code != http.StatusNotFound {
		t.Errorf("no files: %d", rec.Code)
	}
}

func TestCoverETag304(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig", "320")
	rec := a.do(http.MethodGet, "/covers/"+b.ID+"?w=320", nil, nil, own)
	etag := rec.Header().Get("ETag")
	if etag != `"`+coverSHA+`-320"` {
		t.Fatalf("ETag = %q", etag)
	}
	again := a.do(http.MethodGet, "/covers/"+b.ID+"?w=320", nil, func(r *http.Request) { r.Header.Set("If-None-Match", etag) }, own)
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Errorf("If-None-Match: %d, %d bytes", again.Code, again.Body.Len())
	}
	orig := a.do(http.MethodGet, "/covers/"+b.ID, nil, nil, own)
	if orig.Header().Get("ETag") != `"`+coverSHA+`-orig"` {
		t.Errorf("original ETag = %q", orig.Header().Get("ETag"))
	}
	other := a.do(http.MethodGet, "/covers/"+b.ID, nil, func(r *http.Request) { r.Header.Set("If-None-Match", etag) }, own)
	if other.Code != http.StatusOK {
		t.Errorf("another size's ETag: %d", other.Code)
	}
	ranged := a.do(http.MethodGet, "/covers/"+b.ID+"?w=320", nil, func(r *http.Request) { r.Header.Set("Range", "bytes=0-3") }, own)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "file" {
		t.Errorf("range: %d %q", ranged.Code, ranged.Body.String())
	}
}

func TestCoverCacheControl(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig", "320")
	for _, c := range []struct{ query, want string }{
		{"?w=320&v=" + coverSHA[:8], "private, max-age=31536000, immutable"},
		{"?w=320&v=deadbeef", "private, no-cache"},
		{"?w=320", "private, no-cache"},
		{"?v=" + coverSHA[:8], "private, max-age=31536000, immutable"},
	} {
		rec := a.do(http.MethodGet, "/covers/"+b.ID+c.query, nil, nil, own)
		if got := rec.Header().Get("Cache-Control"); rec.Code != http.StatusOK || got != c.want {
			t.Errorf("%q: %d %q, want %q", c.query, rec.Code, got, c.want)
		}
	}
}

func TestCoverBadWidth(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig")
	for _, w := range []string{"x", "0", "-5", "5000", "4097", "1.5", "%20"} {
		rec := a.do(http.MethodGet, "/covers/"+b.ID+"?w="+w, nil, nil, own)
		if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != "Bad width." {
			t.Errorf("w=%q: %d %q", w, rec.Code, rec.Body.String())
		}
	}
}

func TestCoverForbidden(t *testing.T) {
	a := newTestApp(t)
	b := coverBook(t, a, "", "orig", "320")
	opens := coverOpens(t)
	for _, role := range []users.Role{users.RoleMember, users.RoleGuest} {
		rec := a.do(http.MethodGet, "/covers/"+b.ID+"?w=320", nil, nil, a.as(t, role))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", role, rec.Code)
		}
	}
	if n := opens(); len(n) != 0 {
		t.Errorf("a refused request opened files: %v", n)
	}
	// A refusal looks exactly like an unknown book.
	member := a.as(t, users.RoleMember)
	denied := a.do(http.MethodGet, "/covers/"+b.ID, nil, nil, member)
	unknown := a.do(http.MethodGet, "/covers/bk_nope", nil, nil, member)
	if denied.Code != unknown.Code || denied.Body.String() != unknown.Body.String() {
		t.Errorf("refused (%d) differs from unknown (%d)", denied.Code, unknown.Code)
	}
}

func TestCoverAnonymous(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	b := coverBook(t, a, "", "orig")
	opens := coverOpens(t)
	rec := a.do(http.MethodGet, "/covers/"+b.ID+"?w=320", nil, nil)
	want := "/login?next=" + url.QueryEscape("/covers/"+b.ID+"?w=320")
	if rec.Code != http.StatusSeeOther || location(rec) != want {
		t.Errorf("anonymous: %d -> %q, want %q", rec.Code, location(rec), want)
	}
	if n := opens(); len(n) != 0 {
		t.Errorf("opened %v", n)
	}
}

func TestCoverNoCover404(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	_, f := librarytest.NewLibrary(t, a.lib, "Plain", "/plain")
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Bare"})
	opens := coverOpens(t)
	if rec := a.do(http.MethodGet, "/covers/"+b.ID, nil, nil, own); rec.Code != http.StatusNotFound {
		t.Errorf("no cover: %d", rec.Code)
	}
	if n := opens(); len(n) != 0 {
		t.Errorf("opened %v", n)
	}
}

func TestCoverUnknownBook404(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	if rec := a.do(http.MethodGet, "/covers/bk_unknown?w=320", nil, nil, own); rec.Code != http.StatusNotFound {
		t.Errorf("unknown book: %d", rec.Code)
	}
}

func TestCoverPathEscape(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	secret := filepath.Join(filepath.Dir(a.dir), "secret")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(secret) })
	for _, p := range []string{"../secret", "../../secret", "covers/../../secret", "/etc/hostname", "covers", "covers/.."} {
		b := coverBook(t, a, p)
		opens := coverOpens(t)
		rec := a.do(http.MethodGet, "/covers/"+b.ID, nil, nil, own)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("path %q: %d %q", p, rec.Code, rec.Body.String())
		}
		for _, o := range opens() {
			if !strings.HasPrefix(o, filepath.Join(a.dir, "covers")+string(filepath.Separator)) && !strings.HasPrefix(o, mediajobs.ThumbsDir(a.dir)) {
				t.Errorf("path %q opened %q", p, o)
			}
		}
		openCover = os.Open
	}
}

func TestCoverHEAD(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig", "320")
	rec := a.do(http.MethodHead, "/covers/"+b.ID+"?w=320", nil, nil, own)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "8" || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("HEAD: %d, %d bytes, length %q, type %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"), rec.Header().Get("Content-Type"))
	}
}

func TestCoverMethodNotAllowed(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	b := coverBook(t, a, "", "orig")
	tok := a.tokenFor(t, own)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := a.do(m, "/covers/"+b.ID, url.Values{"_csrf": {tok}}, func(r *http.Request) { r.Header.Set(csrfHeader, tok) }, own)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: %d allow %q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
}
