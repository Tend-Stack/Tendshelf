package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The "already in the library" warning (decision 0045, package P4): the 409 carries the match, a held upload
// shows the same choices in its row, and every choice works as a plain form post.

var dupQuick = strings.Repeat("ab", 32)

const dupSize = 4096

// dupApp is an upload app whose service checks the library, with a person of each role and a book in the
// library whose one audio file has the size and quick hash dupQuick.
type dupApp struct {
	*uploadApp
	contributor *http.Cookie
	book        library.Book
	dataDir     string
}

func newDupApp(t *testing.T) *dupApp {
	t.Helper()
	var contributorID, dataDir string
	a := newTestApp(t, func(o *Options) {
		dataDir = o.DataDir
		ur, can := users.New(o.Store, users.Options{}), share.New(o.Store)
		svc := upload.New(upload.Config{
			DataDir: o.DataDir, ChunkSize: 1024, MaxFile: 1 << 20, MaxBook: 4 << 20, Margin: 0,
			ScopeFor: func(ctx context.Context, id string) (library.Scope, bool, error) {
				u, err := ur.ByID(ctx, id)
				if err != nil || u.Disabled {
					return library.Scope{}, false, nil
				}
				return library.Scope{Books: can.BookFilter(u, "b"), Libraries: can.LibraryFilter(u, "l"), Shared: can.SharedBooks(u)}, true, nil
			},
			MayPlace: func(_ context.Context, id, _ string) upload.Placement {
				if id == contributorID {
					return upload.PlaceOwnFolder
				}
				return upload.PlaceTop
			},
		}, o.Store, o.Library, media.Tools{}, o.Jobs, nil)
		if err := svc.Register(o.Jobs); err != nil {
			t.Fatal(err)
		}
		o.Uploads = svc
	})
	u := &uploadApp{a: a}
	u.owner = a.as(t, users.RoleOwner)
	u.admin = a.as(t, users.RoleAdmin)
	u.member, u.guest = a.as(t, users.RoleMember), a.as(t, users.RoleGuest)
	u.folderDir = t.TempDir()
	l, f := librarytest.NewLibrary(t, a.lib, "Uploads library", u.folderDir)
	u.libID, u.folderID, u.folder = l.ID, f.ID, f
	if err := a.lib.SetUploadFolder(upCtx, l.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	d := &dupApp{uploadApp: u, dataDir: dataDir}
	d.contributor = a.as(t, users.RoleMember)
	contributorID = sweepUserID(t, a, d.contributor)
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, contributorID); err != nil {
		t.Fatal(err)
	}
	offlineAGrant(t, a, contributorID, l.ID, 0)
	d.book = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Long Road", Files: 1})
	if _, err := a.st.Writer().Exec(`UPDATE audio_files SET size = ?, quick_hash = ? WHERE book_id = ?`, dupSize, dupQuick, d.book.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

// post sends a form or JSON post with the session's CSRF token (or without it).
func (d *dupApp) post(t *testing.T, path string, ck *http.Cookie, form url.Values, htmx, token bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	if ck != nil {
		r.AddCookie(ck)
		if token {
			r.Header.Set(csrfHeader, d.a.tokenFor(t, ck))
		}
	}
	rec := httptest.NewRecorder()
	d.a.h.ServeHTTP(rec, r)
	return rec
}

func (d *dupApp) quickManifest(addAnyway bool) []byte {
	b, _ := json.Marshal(upload.Manifest{Label: "Copy", AddAnyway: addAnyway,
		Files: []upload.FileIn{{Path: "Copy/01.mp3", Size: dupSize, Quick: dupQuick}}})
	return b
}

type dupBody struct {
	Error struct{ Code, Message string }
	Match *upload.Match
}

func TestUploadCreateAlreadyCarriesMatch(t *testing.T) {
	d := newDupApp(t)
	create := func(ck *http.Cookie, anyway bool) (*httptest.ResponseRecorder, dupBody) {
		rec := d.req(t, http.MethodPost, "/libraries/"+d.libID+"/uploads", "application/json", d.quickManifest(anyway), ck, true)
		var b dupBody
		_ = json.Unmarshal(rec.Body.Bytes(), &b)
		return rec, b
	}
	for name, ck := range map[string]*http.Cookie{"owner": d.owner, "admin": d.admin} {
		rec, b := create(ck, false)
		if rec.Code != http.StatusConflict || b.Error.Code != "already_in_library" || b.Match == nil {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
		if b.Match.BookID != d.book.ID || b.Match.Title != "The Long Road" || b.Match.Href != "/books/"+d.book.ID ||
			b.Match.Tier != "same_files" || !b.Match.MayAddAnyway {
			t.Errorf("%s: match = %+v", name, b.Match)
		}
		if rec, _ := create(ck, true); rec.Code != http.StatusCreated {
			t.Errorf("%s add anyway: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// A contributor is warned, may not add an exact copy, and is refused if they ask.
	rec, b := create(d.contributor, false)
	if rec.Code != http.StatusConflict || b.Match == nil || b.Match.MayAddAnyway {
		t.Errorf("contributor: %d %s", rec.Code, rec.Body.String())
	}
	if rec, b := create(d.contributor, true); rec.Code != http.StatusForbidden || b.Error.Code != "not_allowed" {
		t.Errorf("contributor add anyway: %d %s", rec.Code, rec.Body.String())
	}
	// A member and a visitor reach nothing.
	if rec, _ := create(d.member, false); rec.Code != http.StatusNotFound {
		t.Errorf("member: %d", rec.Code)
	}
	if rec, _ := create(nil, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("visitor: %d", rec.Code)
	}
}

// A person who cannot list the book is not told about it: no 409, and so no title.
func TestUploadCreateNoMatchWhenBookNotListable(t *testing.T) {
	d := newDupApp(t)
	other, f := librarytest.NewLibrary(t, d.a.lib, "Private", t.TempDir())
	hidden := librarytest.AddBook(t, d.a.lib, f, librarytest.BookSpec{Title: "Secret Title", Files: 1})
	if _, err := d.a.st.Writer().Exec(`UPDATE audio_files SET size = 5000, quick_hash = ? WHERE book_id = ?`, strings.Repeat("cd", 32), hidden.ID); err != nil {
		t.Fatal(err)
	}
	// The contributor has a grant on the upload library only; the same file in the other library is neither
	// in the target library nor listable.
	_ = other
	b, _ := json.Marshal(upload.Manifest{Label: "X", Files: []upload.FileIn{{Path: "X/01.mp3", Size: 5000, Quick: strings.Repeat("cd", 32)}}})
	rec := d.req(t, http.MethodPost, "/libraries/"+d.libID+"/uploads", "application/json", b, d.contributor, true)
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "Secret Title") {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
}

// held makes a received upload of the person look like one the check held after staging.
func (d *dupApp) held(t *testing.T, ck *http.Cookie, tier, bookID string) string {
	t.Helper()
	v, rec := d.manifest(t, ck, upload.FileIn{Path: "Copy/01.mp3", Size: 4})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := d.req(t, http.MethodPut, "/uploads/"+v.ID+"/files/"+v.Files[0].ID+"?offset=0", "application/octet-stream", []byte("abcd"), ck, true); rec.Code != http.StatusOK {
		t.Fatalf("chunk: %d", rec.Code)
	}
	if _, err := d.a.st.Writer().Exec(`UPDATE uploads SET state = 'failed', error_code = 'already_in_library', dup_book_id = ?, dup_tier = ? WHERE id = ?`,
		bookID, tier, v.ID); err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func (d *dupApp) row(t *testing.T, ck *http.Cookie, id string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/hx/uploads/"+id, nil)
	r.AddCookie(ck)
	rec := httptest.NewRecorder()
	d.a.h.ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

func TestHeldUploadRowShowsEachTier(t *testing.T) {
	d := newDupApp(t)
	href := `href="/books/` + d.book.ID + `"`
	for _, c := range []struct {
		tier string
		want []string
	}{
		{"same_files", []string{"This book is already in Uploads library: The Long Road."}},
		{"some_files", []string{"Some of these files are already in Uploads library."}},
		{"same_details", []string{"This looks like The Long Road, already in Uploads library."}},
	} {
		id := d.held(t, d.owner, c.tier, d.book.ID)
		code, out := d.row(t, d.owner, id)
		if code != http.StatusOK || !strings.Contains(out, "ts-upload__row--already") {
			t.Fatalf("%s: %d %s", c.tier, code, out)
		}
		for _, w := range append(c.want, href, "Open it", "/uploads/"+id+"/finish", `name="add_anyway"`, "Add anyway", "Do not add", `name="_csrf"`) {
			if !strings.Contains(out, w) {
				t.Errorf("%s: row lacks %q", c.tier, w)
			}
		}
		if strings.Contains(out, "Try again") {
			t.Errorf("%s: an already row offers Try again", c.tier)
		}
	}
	// A contributor on an exact copy: no Add anyway, and the sentence says who to ask. No Edit details either.
	id := d.held(t, d.contributor, "same_files", d.book.ID)
	_, out := d.row(t, d.contributor, id)
	if strings.Contains(out, "Add anyway") || !strings.Contains(out, "Ask an admin if you need a second copy.") || strings.Contains(out, "Edit details") ||
		!strings.Contains(out, "Do not add") {
		t.Errorf("contributor row: %s", out)
	}
	// A look-alike: the contributor may add it anyway.
	id = d.held(t, d.contributor, "same_details", d.book.ID)
	if _, out := d.row(t, d.contributor, id); !strings.Contains(out, "Add anyway") {
		t.Errorf("contributor look-alike row: %s", out)
	}
}

func TestHeldUploadRowWithoutListableBook(t *testing.T) {
	d := newDupApp(t)
	// The upload says it matched a book the person cannot list (no such book for them): no title, no link.
	id := d.held(t, d.contributor, "same_files", "bk_unknown")
	_, out := d.row(t, d.contributor, id)
	if !strings.Contains(out, "This book may already be in the library.") || strings.Contains(out, "The Long Road") ||
		strings.Contains(out, `href="/books/`) || strings.Contains(out, "Add anyway") || !strings.Contains(out, "Do not add") {
		t.Errorf("row: %s", out)
	}
}

func TestAlreadyRowOfAnUploadTheScanHasNotListed(t *testing.T) {
	d := newDupApp(t)
	for _, may := range []bool{true, false} {
		v := upload.View{ID: "u9", State: upload.StateFailed, ErrorCode: upload.CodeAlreadyInLibrary, Label: "Copy",
			Match: &upload.Match{Title: "Copy", Tier: "same_files", MayAddAnyway: may}}
		rv := d.a.srv.uploadRow(upCtx, v, "Audiobooks", "/lib")
		var sb strings.Builder
		if err := components.UploadRow(rv).Render(upCtx, &sb); err != nil {
			t.Fatal(err)
		}
		out := sb.String()
		if !strings.Contains(out, "You added this a moment ago. It will show up once the library scan has seen it.") ||
			strings.Contains(out, "/books/") || strings.Contains(out, "Open it") || strings.Contains(out, "Edit details") ||
			!strings.Contains(out, "Do not add") || strings.Contains(out, "Add anyway") == !may || !strings.Contains(out, `hx-trigger="every 2s"`) {
			t.Errorf("may=%v: %s", may, out)
		}
	}
}

func TestHeldUploadChoices(t *testing.T) {
	d := newDupApp(t)
	// Add anyway: htmx gets the row back, working again.
	id := d.held(t, d.owner, "same_files", d.book.ID)
	rec := d.post(t, "/uploads/"+id+"/finish", d.owner, url.Values{"add_anyway": {"1"}}, true, true)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "ts-upload__row--already") || !strings.Contains(rec.Body.String(), "ts-upload__row--") {
		t.Errorf("owner add anyway: %d %s", rec.Code, rec.Body.String())
	}
	// A plain form post is sent back to the page.
	id = d.held(t, d.admin, "same_files", d.book.ID)
	rec = d.post(t, "/uploads/"+id+"/finish", d.admin, url.Values{"add_anyway": {"1"}, "_csrf": {d.a.tokenFor(t, d.admin)}}, false, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/libraries/"+d.libID+"/add" {
		t.Errorf("admin plain add anyway: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// A contributor may not add an exact copy: a toast for htmx, 403 text for a plain form; nothing starts.
	id = d.held(t, d.contributor, "same_files", d.book.ID)
	rec = d.post(t, "/uploads/"+id+"/finish", d.contributor, url.Values{"add_anyway": {"1"}}, true, true)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "hx-swap-oob") || !strings.Contains(rec.Body.String(), "Ask an admin if you need a second copy.") {
		t.Errorf("contributor htmx: %d %s", rec.Code, rec.Body.String())
	}
	rec = d.post(t, "/uploads/"+id+"/finish", d.contributor, url.Values{"add_anyway": {"1"}}, false, true)
	if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("contributor plain: %d %s", rec.Code, rec.Body.String())
	}
	if n := d.a.count(t, `SELECT count(*) FROM uploads WHERE id = ? AND state = 'failed'`, id); n != 1 {
		t.Errorf("a refused Add anyway changed the upload")
	}
	// Without the token nothing happens.
	if rec := d.post(t, "/uploads/"+id+"/finish", d.contributor, url.Values{"add_anyway": {"1"}}, false, false); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d", rec.Code)
	}
	if rec := d.post(t, "/uploads/"+id+"/cancel", d.contributor, nil, true, false); rec.Code != http.StatusForbidden {
		t.Errorf("cancel without CSRF token: %d", rec.Code)
	}
	// Signed out and somebody else's upload.
	if rec := d.post(t, "/uploads/"+id+"/cancel", nil, nil, true, false); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Errorf("visitor: %d", rec.Code)
	}
	if rec := d.post(t, "/uploads/"+id+"/cancel", d.admin, nil, true, true); rec.Code != http.StatusNotFound {
		t.Errorf("another admin: %d", rec.Code)
	}
	// Do not add: the staged bytes are deleted and the row goes (an empty answer for htmx).
	staging := filepath.Join(d.dataDir, "uploads", "incoming", id)
	if _, err := os.Stat(staging); err != nil {
		t.Fatalf("a held upload keeps its staged bytes: %v", err)
	}
	rec = d.post(t, "/uploads/"+id+"/cancel", d.contributor, nil, true, true)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
		t.Errorf("do not add: %d %q", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staged bytes remain: %v", err)
	}
	// Plain form.
	id = d.held(t, d.owner, "some_files", d.book.ID)
	rec = d.post(t, "/uploads/"+id+"/cancel", d.owner, url.Values{"_csrf": {d.a.tokenFor(t, d.owner)}}, false, false)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("plain do not add: %d", rec.Code)
	}
}

// The book page tells the owner and admins about a second copy of the same audio, and nobody else.
func TestBookPageSameAudioNotice(t *testing.T) {
	d := newDupApp(t)
	older := d.book
	newer := librarytest.AddBook(t, d.a.lib, d.folder, librarytest.BookSpec{Title: "The Long Road (hand copy)", Files: 1, AddedAt: older.AddedAt + 1000})
	if _, err := d.a.st.Writer().Exec(`UPDATE audio_files SET size = ?, quick_hash = ? WHERE book_id = ?`, dupSize, dupQuick, newer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.a.st.Writer().Exec(`UPDATE books SET added_at = ? WHERE id = ?`, older.AddedAt+1000, newer.ID); err != nil {
		t.Fatal(err)
	}
	merge := "/admin/duplicates/merge?keep=" + older.ID + "&amp;drop=" + newer.ID
	for name, ck := range map[string]*http.Cookie{"owner": d.owner, "admin": d.admin} {
		for _, id := range []string{older.ID, newer.ID} {
			r := httptest.NewRequest(http.MethodGet, "/books/"+id, nil)
			r.AddCookie(ck)
			rec := httptest.NewRecorder()
			d.a.h.ServeHTTP(rec, r)
			out := rec.Body.String()
			if rec.Code != http.StatusOK || !strings.Contains(out, "Another copy of this book is in the library.") || !strings.Contains(out, merge) || !strings.Contains(out, "Compare them") {
				t.Errorf("%s %s: %d notice missing (merge %s)", name, id, rec.Code, merge)
			}
		}
	}
	// The contributor may list the book but is neither owner nor admin: no notice.
	r := httptest.NewRequest(http.MethodGet, "/books/"+older.ID, nil)
	r.AddCookie(d.contributor)
	rec := httptest.NewRecorder()
	d.a.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Another copy of this book") {
		t.Errorf("contributor: %d notice shown", rec.Code)
	}
	// No other copy, no notice; a book of another library is not a copy.
	lone := librarytest.AddBook(t, d.a.lib, d.folder, librarytest.BookSpec{Title: "Alone", Files: 1})
	r = httptest.NewRequest(http.MethodGet, "/books/"+lone.ID, nil)
	r.AddCookie(d.owner)
	rec = httptest.NewRecorder()
	d.a.h.ServeHTTP(rec, r)
	if strings.Contains(rec.Body.String(), "Another copy of this book") {
		t.Errorf("a lone book shows the notice")
	}
}
