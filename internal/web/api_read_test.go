package web

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// apiToken gives a role a device token and returns it (the token is the credential; it is never
// logged by the tests).
func apiToken(t *testing.T, e *sweepEnv, role int) string {
	t.Helper()
	var tok string
	err := e.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(e.a.st, nil).Issue(context.Background(), tx, e.uid[role], "API test", auth.KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// apiGet is a GET with a bearer token.
func apiGet(e *sweepEnv, tok, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	return deviceBearerReq(e.a, http.MethodGet, path, tok, mod)
}

// apiDecode reads a JSON answer into v and fails the test on any other content type.
func apiDecode(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q, body %q", ct, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("not JSON: %v: %q", err, rec.Body)
	}
}

type apiTestList struct {
	Items []struct {
		ID, Title string
	} `json:"items"`
	Next  *string `json:"next"`
	Total *int    `json:"total"`
}

func TestAPIReadOwner(t *testing.T) {
	e := grantsLeakSeed(t)
	tok := apiToken(t, e.sweepEnv, sweepOwner)

	// Libraries: both, with their counts, and an ETag that answers 304.
	rec := apiGet(e.sweepEnv, tok, "/api/v1/libraries", nil)
	var libs struct{ Items []apiLibrary }
	apiDecode(t, rec, &libs)
	if rec.Code != 200 || len(libs.Items) != 3 {
		t.Fatalf("libraries: %d %s", rec.Code, rec.Body)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" || rec.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("libraries headers: %v", rec.Header())
	}
	again := apiGet(e.sweepEnv, tok, "/api/v1/libraries", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
	if again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("conditional libraries: %d", again.Code)
	}

	// Books: the whole list, paged one by one through the cursor.
	var seen []string
	path := "/api/v1/books?limit=1&sort=title"
	for i := 0; i < 6; i++ {
		rec := apiGet(e.sweepEnv, tok, path, nil)
		var l apiTestList
		apiDecode(t, rec, &l)
		if rec.Code != 200 || len(l.Items) != 1 || l.Total == nil || *l.Total != 3 {
			t.Fatalf("page %d: %d %s", i, rec.Code, rec.Body)
		}
		seen = append(seen, l.Items[0].ID)
		if l.Next == nil {
			break
		}
		path = "/api/v1/books?limit=1&sort=title&cursor=" + url.QueryEscape(*l.Next)
	}
	if len(seen) != 3 || seen[0] == seen[1] || seen[1] == seen[2] {
		t.Fatalf("paged books: %v", seen)
	}
	rec = apiGet(e.sweepEnv, tok, "/api/v1/libraries/"+e.libID+"/books", nil)
	var one apiTestList
	apiDecode(t, rec, &one)
	if rec.Code != 200 || len(one.Items) != 1 || one.Items[0].ID != e.book.ID || *one.Total != 1 || one.Next != nil {
		t.Fatalf("library books: %d %s", rec.Code, rec.Body)
	}

	// One book: details, files with alias URLs, the progress, and the permissions.
	rec = apiGet(e.sweepEnv, tok, "/api/v1/books/"+e.book.ID, nil)
	var d apiBookDetail
	apiDecode(t, rec, &d)
	if rec.Code != 200 || len(d.Files) != 2 || len(d.Chapters) != 4 || !d.Permissions.Listen || !d.Permissions.Download ||
		d.Progress == nil || d.Progress.PositionMs != 1500 || d.Progress.BookID != e.book.ID {
		t.Fatalf("book: %d %s", rec.Code, rec.Body)
	}
	if d.Files[0].StreamURL != "/api/v1/stream/"+e.audio[0] || d.Files[0].PeaksURL != "/api/v1/peaks/"+e.audio[0] || !d.Files[0].Playable {
		t.Fatalf("file urls: %+v", d.Files[0])
	}
	if d.Book.Cover == nil || !strings.HasPrefix(d.Book.Cover.URL, "/api/v1/covers/"+e.book.ID+"?v=") || d.Book.PublishedYear != nil {
		t.Fatalf("cover or year: %+v", d.Book)
	}
	if apiGet(e.sweepEnv, tok, "/api/v1/books/"+e.book.ID, func(r *http.Request) {
		r.Header.Set("If-None-Match", rec.Header().Get("ETag"))
	}).Code != http.StatusNotModified {
		t.Error("book is not revalidated with 304")
	}

	// The media aliases answer to the token; the originals refuse it.
	for _, p := range []string{"/api/v1/covers/" + e.book.ID + "?w=320", "/api/v1/stream/" + e.audio[0], "/api/v1/peaks/" + e.audio[0]} {
		if rec := apiGet(e.sweepEnv, tok, p, nil); rec.Code != 200 {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
	for _, p := range []string{"/covers/" + e.book.ID, "/stream/" + e.audio[0], "/peaks/" + e.audio[0]} {
		if rec := apiGet(e.sweepEnv, tok, p, nil); rec.Code != 401 || deviceErrCode(t, rec) != "unauthorized" {
			t.Errorf("GET %s with a token: %d", p, rec.Code)
		}
	}

	// Search.
	rec = apiGet(e.sweepEnv, tok, "/api/v1/search?q=Lantern", nil)
	var sr apiSearchResult
	apiDecode(t, rec, &sr)
	if rec.Code != 200 || sr.Total != 1 || len(sr.Books) != 1 || sr.Books[0].ID != e.book.ID {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	rec = apiGet(e.sweepEnv, tok, "/api/v1/search?q=Sweepauthor", nil)
	apiDecode(t, rec, &sr)
	if len(sr.People) != 1 || sr.People[0].Role != "author" {
		t.Fatalf("search people: %s", rec.Body)
	}
	rec = apiGet(e.sweepEnv, tok, "/api/v1/search?q=%20", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"books":[]`) || !strings.Contains(rec.Body.String(), `"suggestion":null`) {
		t.Fatalf("blank search: %d %s", rec.Code, rec.Body)
	}

	// Shelves, one shelf, shared.
	rec = apiGet(e.sweepEnv, tok, "/api/v1/shelves", nil)
	var shelves struct{ Own, Shared []apiShelf }
	apiDecode(t, rec, &shelves)
	if rec.Code != 200 || len(shelves.Own) != 1 || shelves.Own[0].Books != 1 || shelves.Shared == nil {
		t.Fatalf("shelves: %d %s", rec.Code, rec.Body)
	}
	rec = apiGet(e.sweepEnv, tok, "/api/v1/shelves/"+e.shelf[sweepOwner], nil)
	var shelf struct {
		Shelf apiShelf
		Books apiTestList
	}
	apiDecode(t, rec, &shelf)
	if rec.Code != 200 || shelf.Shelf.Books != 1 || len(shelf.Books.Items) != 1 {
		t.Fatalf("shelf: %d %s", rec.Code, rec.Body)
	}
	rec = apiGet(e.sweepEnv, tok, "/api/v1/shared", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"sharers":[]`) {
		t.Fatalf("shared: %d %s", rec.Code, rec.Body)
	}
}

func TestAPIReadBadQueries(t *testing.T) {
	e := sweepSeed(t)
	tok := apiToken(t, e, sweepOwner)
	for _, p := range []string{
		"/api/v1/books?limit=0", "/api/v1/books?limit=121", "/api/v1/books?limit=abc", "/api/v1/search?q=x&limit=201",
		"/api/v1/shared?limit=51",
	} {
		rec := apiGet(e, tok, p, nil)
		var body struct{ Error struct{ Code, Field string } }
		apiDecode(t, rec, &body)
		if rec.Code != 400 || body.Error.Code != "invalid" || body.Error.Field != "limit" {
			t.Errorf("GET %s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

// TestAPICursorTampering: a forged cursor is a 400 on the field cursor or a page without anything
// the person may not see, never a 500 and never a hidden book.
func TestAPICursorTampering(t *testing.T) {
	e := grantsLeakSeed(t)
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, role := range []int{sweepMember, sweepGuest} {
		tok := apiToken(t, e.sweepEnv, role)
		// A real cursor of the title sort, from the owner's list (it names the hidden book's neighbour).
		otok := apiToken(t, e.sweepEnv, sweepOwner)
		var first apiTestList
		apiDecode(t, apiGet(e.sweepEnv, otok, "/api/v1/books?limit=1&sort=title", nil), &first)
		if first.Next == nil {
			t.Fatal("no cursor to reuse")
		}
		cursors := []string{
			"!!not-base64!!", enc("not json"), enc(`{}`), enc(`{"s":"title","i":""}`), enc(`{"s":"added","n":1,"i":"x"}`),
			enc(`{"s":"title","t":"a","i":"` + e.hidden.ID + `"}`), enc(`{"s":"title","i":"` + e.hidden.ID + `"}`),
			*first.Next, strings.Repeat("A", 5000),
		}
		for _, c := range cursors {
			rec := apiGet(e.sweepEnv, tok, "/api/v1/books?sort=title&cursor="+url.QueryEscape(c), nil)
			switch rec.Code {
			case http.StatusBadRequest:
				var body struct{ Error struct{ Code, Field string } }
				apiDecode(t, rec, &body)
				if body.Error.Field != "cursor" {
					t.Errorf("%s cursor %.20q: %s", sweepRoleNames[role], c, rec.Body)
				}
			case http.StatusOK:
				e.noHidden(t, "cursor "+c[:min(len(c), 20)], rec.Body.String())
			default:
				t.Errorf("%s cursor %.20q: %d", sweepRoleNames[role], c, rec.Code)
			}
		}
		// The same on a shelf.
		if role == sweepMember {
			rec := apiGet(e.sweepEnv, tok, "/api/v1/shelves/"+e.shelf[sweepMember]+"?cursor="+url.QueryEscape(enc(`{"x":1}`)), nil)
			if rec.Code != 400 && rec.Code != 200 {
				t.Errorf("shelf cursor: %d", rec.Code)
			}
		}
	}
}

func TestAPIAuthShapes(t *testing.T) {
	e := sweepSeed(t)
	tok := apiToken(t, e, sweepOwner)

	// Anonymous and mixed credentials.
	if rec := e.a.do(http.MethodGet, "/api/v1/books", nil, nil); rec.Code != 401 || deviceErrCode(t, rec) != "unauthorized" {
		t.Errorf("anonymous: %d", rec.Code)
	}
	rec := apiGet(e, tok, "/api/v1/books", func(r *http.Request) { r.AddCookie(e.ck[sweepOwner]) })
	if rec.Code != 400 || deviceErrCode(t, rec) != "invalid" {
		t.Errorf("mixed: %d %s", rec.Code, rec.Body)
	}
	// A session works too, for reading.
	if rec := e.a.do(http.MethodGet, "/api/v1/books", nil, nil, e.ck[sweepOwner]); rec.Code != 200 {
		t.Errorf("session: %d", rec.Code)
	}

	// Every error under /api/ is JSON, never HTML: not found, a wrong method, a media 404.
	for _, c := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/v1/nothing", 404}, {"GET", "/api/v1/books/bk_unknown", 404}, {"GET", "/api/v1/covers/bk_unknown", 404},
		{"GET", "/api/v1/stream/af_unknown", 404}, {"GET", "/api/v1/peaks/af_unknown", 404},
		{"GET", "/api/v1/shelves/shf_unknown", 404}, {"GET", "/api/v1/libraries/lib_unknown/books", 404},
		{"POST", "/api/v1/covers/" + e.book.ID, 405}, {"DELETE", "/api/v1/stream/" + e.audio[0], 405},
		{"GET", "/api/v1/stream/" + e.audio[0] + "?compat=2", 400}, {"GET", "/api/v1/covers/" + e.book.ID + "?w=abc", 400},
	} {
		rec := deviceBearerReq(e.a, c.method, c.path, tok, nil)
		var body struct {
			Error struct{ Code, Message string }
		}
		apiDecode(t, rec, &body)
		if rec.Code != c.code || body.Error.Code == "" || body.Error.Message == "" || strings.Contains(rec.Header().Get("Content-Type"), "html") {
			t.Errorf("%s %s: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
	// The unknown-id answers are byte-identical to each other.
	a1 := apiGet(e, tok, "/api/v1/books/bk_one", nil)
	a2 := apiGet(e, tok, "/api/v1/books/bk_two", nil)
	if a1.Body.String() != a2.Body.String() {
		t.Error("unknown books answer differently")
	}
}

func TestAPIRateLimit(t *testing.T) {
	e := sweepSeed(t)
	e.a.srv.apiLimiter = auth.NewLimiter(3, time.Minute, 16, nil)
	tok := apiToken(t, e, sweepOwner)
	for i := 0; i < 3; i++ {
		if rec := apiGet(e, tok, "/api/v1/me", nil); rec.Code != 200 {
			t.Fatalf("request %d: %d", i, rec.Code)
		}
	}
	rec := apiGet(e, tok, "/api/v1/me", nil)
	if rec.Code != 429 || deviceErrCode(t, rec) != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("fourth: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	// A session is never limited, and another device has its own count.
	for i := 0; i < 6; i++ {
		if rec := e.a.do(http.MethodGet, "/api/v1/me", nil, nil, e.ck[sweepOwner]); rec.Code != 200 {
			t.Fatalf("session request %d: %d", i, rec.Code)
		}
	}
	if rec := apiGet(e, apiToken(t, e, sweepAdmin), "/api/v1/me", nil); rec.Code != 200 {
		t.Fatalf("other device: %d", rec.Code)
	}
}
