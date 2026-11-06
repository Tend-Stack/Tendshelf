package web

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// opdsLab is an app with three books in two libraries and a person for each right:
//
//	lib1: bk1 (ready epub), bk2 (only a refused file)   lib2: bk3 (ready epub)
//	m_ok: lib1, download and read   m_dl: lib1, download only   m_rd: lib1, read only
//	m_two: lib2 only                m_none: nothing
type opdsLab struct {
	a            *testApp
	svc          *ebook.Service
	owner        *http.Cookie
	ownerID      string
	bk1, bk2     library.Book
	bk3          library.Book
	f1, f2, f3   string // file ids
	lib1, lib2   library.Library
	cookies      map[string]*http.Cookie
	ids, secrets map[string]string
}

const opdsBody1 = "epub-bytes-of-book-one"

func newOPDSLab(t *testing.T) *opdsLab {
	t.Helper()
	a := newTestApp(t)
	l := &opdsLab{a: a, cookies: map[string]*http.Cookie{}, ids: map[string]string{}, secrets: map[string]string{}}
	l.owner = a.owner(t)
	l.ownerID = sweepUserID(t, a, l.owner)
	l.svc = ebook.New(a.st, a.lib, a.runner, a.dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	a.srv.ebooks = l.svc
	var f1, f2 library.Folder
	l.lib1, f1 = librarytest.NewLibrary(t, a.lib, "Opds one", adminAudioDir(t, true))
	l.lib2, f2 = librarytest.NewLibrary(t, a.lib, "Opds two", adminAudioDir(t, true))
	l.bk1 = librarytest.AddBook(t, a.lib, f1, librarytest.BookSpec{Title: "Opdsword Alpha", Authors: []string{"Ann Author"}, Description: "About <alpha> & more", DurationMs: 1000, AddedAt: 10, Files: 1})
	l.bk2 = librarytest.AddBook(t, a.lib, f1, librarytest.BookSpec{Title: "Opdsword Beta", DurationMs: 1000, AddedAt: 20, Files: 1})
	l.bk3 = librarytest.AddBook(t, a.lib, f2, librarytest.BookSpec{Title: "Opdsword Gamma", DurationMs: 1000, AddedAt: 30, Files: 1})
	l.f1 = l.putFile(t, l.bk1, "ready", "", opdsBody1)
	l.f2 = l.putFile(t, l.bk2, "refused", "drm", "drm-bytes")
	l.f3 = l.putFile(t, l.bk3, "ready", "", "epub-bytes-of-book-three")
	// A book whose only file is still pending is not in any feed.
	bk4 := librarytest.AddBook(t, a.lib, f1, librarytest.BookSpec{Title: "Opdsword Delta", DurationMs: 1000, AddedAt: 40, Files: 1})
	l.putFile(t, bk4, "pending", "", "pending-bytes")

	grant := func(name string, lib library.Library, dl, rd int) {
		ck := a.as(t, users.RoleMember)
		id := sweepUserID(t, a, ck)
		l.cookies[name], l.ids[name] = ck, id
		if lib.ID == "" {
			return
		}
		if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
			VALUES (?, ?, 'library', ?, ?, ?, 1, 1)`, "gr_"+id, id, lib.ID, dl, rd); err != nil {
			t.Fatal(err)
		}
	}
	grant("m_ok", l.lib1, 1, 1)
	grant("m_dl", l.lib1, 1, 0)
	grant("m_rd", l.lib1, 0, 1)
	grant("m_two", l.lib2, 1, 1)
	grant("m_none", library.Library{}, 0, 0)
	l.cookies["owner"], l.ids["owner"] = l.owner, l.ownerID
	for name := range l.ids {
		l.secrets[name] = l.key(t, name, auth.KindOPDS)
	}
	return l
}

// putFile stores a file row of an upload and its bytes at the place the service reads from.
func (l *opdsLab) putFile(t *testing.T, book library.Book, state, reason, body string) string {
	t.Helper()
	id := "ebf_opds_" + book.ID
	sha := fmt.Sprintf("%064x", len(id)*104729+len(body))
	var rr any
	if reason != "" {
		rr = reason
	}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, paired_by, ext, size, mtime_ns, quick_hash,
		sha256, state, refused_reason, original_name, created_at, updated_at)
		VALUES (?, 'upload', ?, ?, 'upload', 'epub', ?, 0, ?, ?, ?, ?, ?, 1, 1)`,
		id, book.LibraryID, book.ID, len(body), sha, sha, state, rr, "book.epub"); err != nil {
		t.Fatal(err)
	}
	p := l.svc.FilePath(ebook.File{Origin: "upload", SHA256: sha, Ext: "epub"})
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return id
}

// key issues a key of a kind for a lab person and returns it.
func (l *opdsLab) key(t *testing.T, who string, kind auth.DeviceKind) string {
	t.Helper()
	var secret string
	err := l.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		secret, _, err = l.a.srv.devices.IssueKey(context.Background(), tx, l.ids[who], "", kind)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func (l *opdsLab) username(t *testing.T, who string) string {
	t.Helper()
	var n string
	if err := l.a.st.Reader().QueryRow(`SELECT username FROM users WHERE id = ?`, l.ids[who]).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// get sends a GET with Basic credentials (none when user is empty) and any extra header.
func (l *opdsLab) get(path, user, pass string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return l.a.do(http.MethodGet, path, nil, func(r *http.Request) {
		if user != "" || pass != "" {
			r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
		}
		if mod != nil {
			mod(r)
		}
	})
}

// as reads a path as a lab person with their own key.
func (l *opdsLab) as(t *testing.T, who, path string) *httptest.ResponseRecorder {
	t.Helper()
	return l.get(path, l.username(t, who), l.secrets[who], nil)
}

type opdsParsed struct {
	XMLName xml.Name `xml:"feed"`
	Total   string   `xml:"totalResults"`
	Links   []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Entries []struct {
		ID      string `xml:"id"`
		Title   string `xml:"title"`
		Summary string `xml:"summary"`
		Links   []struct {
			Rel  string `xml:"rel,attr"`
			Href string `xml:"href,attr"`
			Type string `xml:"type,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

func (l *opdsLab) feed(t *testing.T, who, path string) opdsParsed {
	t.Helper()
	rec := l.as(t, who, path)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %s = %d %s", who, path, rec.Code, rec.Body.String())
	}
	var f opdsParsed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("%s %s is not XML: %v\n%s", who, path, err, rec.Body.String())
	}
	return f
}

func (f opdsParsed) titles() string {
	var out []string
	for _, e := range f.Entries {
		out = append(out, e.Title)
	}
	return strings.Join(out, ",")
}

// TestOPDSRefusesWithoutAKey: nothing but a Basic user name and a matching opds key gets in.
func TestOPDSRefusesWithoutAKey(t *testing.T) {
	l := newOPDSLab(t)
	user, key := l.username(t, "owner"), l.secrets["owner"]
	koKey := l.key(t, "owner", auth.KindKOReader)
	cases := map[string]*httptest.ResponseRecorder{
		"no credentials":        l.get("/opds", "", "", nil),
		"login password":        l.get("/opds", user, testPassword, nil),
		"wrong user, right key": l.get("/opds", "someone", key, nil),
		"koreader key":          l.get("/opds", user, koKey, nil),
		"key shape, unknown":    l.get("/opds", user, "abcdefghijklmnopqrstuvwx", nil),
		"bearer alone": l.get("/opds", "", "", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+key)
		}),
		"session cookie alone":        l.a.do(http.MethodGet, "/opds", nil, nil, l.owner),
		"cookie and no key on a file": l.a.do(http.MethodGet, "/opds/books/"+l.bk1.ID+"/file/"+l.f1, nil, nil, l.owner),
	}
	for name, rec := range cases {
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Basic realm=") {
			t.Errorf("%s: challenge %q", name, got)
		}
	}
	if rec := l.as(t, "owner", "/opds"); rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Type"), "kind=navigation") ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("good key = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}
	if rec := l.get("/opds", strings.ToUpper(user), key, nil); rec.Code != http.StatusOK {
		t.Errorf("user name in another case = %d", rec.Code)
	}
}

// TestOPDSKeyOpensNothingElse: the same key on the app and the API, as Basic or Bearer, gets nothing.
func TestOPDSKeyOpensNothingElse(t *testing.T) {
	l := newOPDSLab(t)
	user, key := l.username(t, "owner"), l.secrets["owner"]
	basic := func(r *http.Request) {
		r.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+key)))
	}
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+key) }
	for _, p := range []string{"/api/v1/me", "/api/v1/books"} {
		for name, mod := range map[string]func(*http.Request){"basic": basic, "bearer": bearer} {
			if rec := l.a.do(http.MethodGet, p, nil, mod); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s = %d, want 401", name, p, rec.Code)
			}
		}
	}
	for _, p := range []string{"/me", "/", "/books/" + l.bk1.ID} {
		rec := l.a.do(http.MethodGet, p, nil, basic)
		if rec.Code == http.StatusOK {
			t.Errorf("a key opened %s", p)
		}
	}
	// A POST to the catalogue is not a thing, with a key or without.
	if rec := l.a.do(http.MethodPost, "/opds/all", nil, basic); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /opds/all with a key = %d, want 405", rec.Code)
	}
}

// TestOPDSRevokedAndDisabledKey: turning the key off, or the person, stops the next request.
func TestOPDSRevokedAndDisabledKey(t *testing.T) {
	l := newOPDSLab(t)
	if rec := l.as(t, "m_ok", "/opds/all"); rec.Code != http.StatusOK {
		t.Fatalf("before = %d", rec.Code)
	}
	var devID string
	if err := l.a.st.Reader().QueryRow(`SELECT id FROM devices WHERE user_id = ? AND kind = 'opds'`, l.ids["m_ok"]).Scan(&devID); err != nil {
		t.Fatal(err)
	}
	ck := l.cookies["m_ok"]
	rec := l.a.do(http.MethodPost, "/me/devices/"+devID+"/signout", map[string][]string{"_csrf": {l.a.tokenFor(t, ck)}}, nil, ck)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("turn off = %d", rec.Code)
	}
	if rec := l.as(t, "m_ok", "/opds/all"); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked key = %d, want 401", rec.Code)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, l.ids["m_dl"]); err != nil {
		t.Fatal(err)
	}
	if rec := l.as(t, "m_dl", "/opds/all"); rec.Code != http.StatusUnauthorized {
		t.Errorf("disabled person = %d, want 401", rec.Code)
	}
}

// TestOPDSLimiter: ten wrong keys from one address, then 429, even for the right key.
func TestOPDSLimiter(t *testing.T) {
	l := newOPDSLab(t)
	user := l.username(t, "owner")
	for i := 0; i < keyFailMax; i++ {
		if rec := l.get("/opds", user, "abcdefghijklmnopqrstuvwx", nil); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key %d = %d", i, rec.Code)
		}
	}
	rec := l.as(t, "owner", "/opds")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after the limit = %d (Retry-After %q)", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// TestOPDSFeedsFilterByGrants: each person's feed lists only the books they may download as text, a
// refused file never appears, and a file path of a book they may not have answers 404.
func TestOPDSFeedsFilterByGrants(t *testing.T) {
	l := newOPDSLab(t)
	want := map[string]string{
		"owner":  "Opdsword Alpha,Opdsword Gamma",
		"m_ok":   "Opdsword Alpha",
		"m_two":  "Opdsword Gamma",
		"m_dl":   "", // download without read: no copy to take
		"m_rd":   "", // read without download
		"m_none": "",
	}
	for who, titles := range want {
		for _, p := range []string{"/opds/all", "/opds/new", "/opds/search?q=opdsword"} {
			f := l.feed(t, who, p)
			parts := strings.Split(f.titles(), ",")
			sort.Strings(parts) // the order is checked elsewhere; here only who sees what
			got := strings.TrimPrefix(strings.Join(parts, ","), ",")
			if got != titles {
				t.Errorf("%s %s = %q, want %q", who, p, got, titles)
			}
		}
	}
	if got := l.feed(t, "owner", "/opds/new").titles(); got != "Opdsword Gamma,Opdsword Alpha" {
		t.Errorf("newest first = %q", got)
	}
	if got := l.feed(t, "owner", "/opds/all").titles(); got != "Opdsword Alpha,Opdsword Gamma" {
		t.Errorf("by title = %q", got)
	}
	f := l.feed(t, "m_ok", "/opds/all")
	if len(f.Entries) != 1 || f.Entries[0].ID != "urn:tendshelf:book:"+l.bk1.ID || f.Entries[0].Summary != "About <alpha> & more" {
		t.Fatalf("entry = %+v", f.Entries)
	}
	var acq string
	for _, k := range f.Entries[0].Links {
		if k.Rel == relAcquire {
			acq = k.Href
			if k.Type != "application/epub+zip" {
				t.Errorf("type = %q", k.Type)
			}
		}
	}
	if acq != "/opds/books/"+l.bk1.ID+"/file/"+l.f1 {
		t.Fatalf("acquisition link = %q", acq)
	}

	// Downloads.
	if rec := l.as(t, "m_ok", acq); rec.Code != http.StatusOK || rec.Body.String() != opdsBody1 {
		t.Errorf("m_ok download = %d %q", rec.Code, rec.Body.String())
	}
	for _, c := range []struct{ who, path string }{
		{"m_dl", acq}, {"m_rd", acq}, {"m_none", acq}, {"m_two", acq},
		{"owner", "/opds/books/" + l.bk2.ID + "/file/" + l.f2},                       // refused file
		{"owner", "/opds/books/" + l.bk1.ID + "/file/" + l.f3},                       // another book's file
		{"m_ok", "/opds/books/" + l.bk3.ID + "/file/" + l.f3},                        // a book they may not see
		{"owner", "/opds/books/nope/file/" + l.f1},                                   // unknown book
		{"m_dl", "/opds/covers/" + l.bk1.ID}, {"m_none", "/opds/covers/" + l.bk1.ID}, // covers follow the same right
	} {
		if rec := l.as(t, c.who, c.path); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", c.who, c.path, rec.Code)
		}
	}
}

// TestOPDSPagingAndSearch: pages of 50 with next and previous, search words escape, odd input is safe.
func TestOPDSPagingAndSearch(t *testing.T) {
	l := newOPDSLab(t)
	_, folder := librarytest.NewLibrary(t, l.a.lib, "Opds many", adminAudioDir(t, true))
	for i := 0; i < 55; i++ {
		b := librarytest.AddBook(t, l.a.lib, folder, librarytest.BookSpec{Title: fmt.Sprintf("Pagebook %03d", i), SortTitle: fmt.Sprintf("pagebook %03d", i), DurationMs: 1, AddedAt: int64(100 + i), Files: 1})
		l.putFile(t, b, "ready", "", fmt.Sprintf("body-%d", i))
	}
	rel := func(f opdsParsed, r string) string {
		for _, k := range f.Links {
			if k.Rel == r {
				return k.Href
			}
		}
		return ""
	}
	p1 := l.feed(t, "owner", "/opds/all")
	if len(p1.Entries) != 50 || p1.Total != "57" || rel(p1, "next") != "/opds/all?page=2" || rel(p1, "previous") != "" {
		t.Fatalf("page 1: %d entries, total %q, next %q", len(p1.Entries), p1.Total, rel(p1, "next"))
	}
	p2 := l.feed(t, "owner", "/opds/all?page=2")
	if len(p2.Entries) != 7 || rel(p2, "next") != "" || rel(p2, "previous") != "/opds/all" {
		t.Fatalf("page 2: %d entries, next %q, previous %q", len(p2.Entries), rel(p2, "next"), rel(p2, "previous"))
	}
	for _, bad := range []string{"0", "-3", "x", "999999999"} {
		if f := l.feed(t, "owner", "/opds/all?page="+bad); len(f.Entries) != 50 && bad != "999999999" {
			t.Errorf("page=%s gave %d entries", bad, len(f.Entries))
		}
	}
	s := l.feed(t, "owner", "/opds/search?q=pagebook")
	if len(s.Entries) != 50 || rel(s, "next") != "/opds/search?q=pagebook&page=2" {
		t.Errorf("search page 1: %d entries, next %q", len(s.Entries), rel(s, "next"))
	}
	if f := l.feed(t, "owner", `/opds/search?q=%3Cscript%3E%26%22+AND+`); len(f.Entries) != 0 {
		t.Errorf("hostile search found %d", len(f.Entries))
	}
	if f := l.feed(t, "owner", "/opds/search"); len(f.Entries) != 0 {
		t.Errorf("empty search found %d", len(f.Entries))
	}
	rec := l.as(t, "owner", "/opds/search.xml")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/opds/search?q={searchTerms}") {
		t.Errorf("search description = %d %s", rec.Code, rec.Body.String())
	}
	if rec := l.as(t, "owner", "/opds/"); rec.Code != http.StatusOK {
		t.Errorf("/opds/ = %d", rec.Code)
	}
}

// TestOPDSNotInServiceWorker: the service worker never answers /opds.
func TestOPDSNotInServiceWorker(t *testing.T) {
	b, err := os.ReadFile("static/js/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "opds") || strings.Contains(string(b), "kosync") {
		t.Error("sw.js mentions an app-key surface")
	}
}
