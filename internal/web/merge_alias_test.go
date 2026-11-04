package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// tbAliasMux serves the two addresses a merged-away book id used to have, through loadBook.
func tbAliasMux(a *testApp) http.Handler {
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter, r *http.Request) {
		if b, found := a.srv.loadBook(w, r, r.PathValue("id"), share.ViewBook); found {
			_, _ = w.Write([]byte("book " + b.Title))
		}
	}
	mux.HandleFunc("GET /books/{id}", ok)
	mux.HandleFunc("GET /books/{id}/read", ok)
	mux.HandleFunc("GET /books/{id}/about", ok)
	mux.HandleFunc("POST /books/{id}", ok)
	return auth.Middleware(a.srv.sessions, a.srv.proxies)(mux)
}

func TestMergeAlias(t *testing.T) {
	a := newTestApp(t)
	h := tbAliasMux(a)
	_, f := librarytest.NewLibrary(t, a.lib, "Books", "/audio")
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Took Over"})
	if _, err := a.st.Writer().Exec(`INSERT INTO merged_books(from_id, to_id, merged_at) VALUES ('bk_old', ?, 1)`, b.ID); err != nil {
		t.Fatal(err)
	}
	owner, member := a.as(t, users.RoleOwner), a.as(t, users.RoleMember)
	get := func(method, path string, c *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.AddCookie(c)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(http.MethodGet, "/books/bk_old", owner); rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/books/"+b.ID {
		t.Errorf("page: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(http.MethodGet, "/books/bk_old/read?x=1", owner); rec.Code != http.StatusPermanentRedirect || rec.Header().Get("Location") != "/books/"+b.ID+"/read?x=1" {
		t.Errorf("reader: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Only the two page addresses, and only GET, follow the alias.
	if rec := get(http.MethodGet, "/books/bk_old/about", owner); rec.Code != http.StatusNotFound {
		t.Errorf("other path: %d", rec.Code)
	}
	if rec := get(http.MethodPost, "/books/bk_old", owner); rec.Code != http.StatusNotFound {
		t.Errorf("POST: %d", rec.Code)
	}
	// A person who may not see the book that took over learns nothing from the alias.
	if rec := get(http.MethodGet, "/books/bk_old", member); rec.Code != http.StatusNotFound || rec.Header().Get("Location") != "" {
		t.Errorf("member: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := get(http.MethodGet, "/books/bk_nope", owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id: %d", rec.Code)
	}
}

// TestMergeTextBookNotPlayable: the listening gate treats a text book as not found (invariant 8).
func TestMergeTextBookNotPlayable(t *testing.T) {
	a := newTestApp(t)
	lib, f := librarytest.NewLibrary(t, a.lib, "Books", "/audio")
	id, err := a.lib.CreateTextBook(t.Context(), library.TextBookIn{LibraryID: lib.ID, FolderID: f.ID, RootPath: "/audio/x.epub", Title: "Words"})
	if err != nil {
		t.Fatal(err)
	}
	owner := users.User{ID: "us_owner", Role: users.RoleOwner}
	if _, err := a.srv.playableFor(t.Context(), owner, id); err == nil {
		t.Error("a text book is playable")
	}
	audio := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Sound"})
	if _, err := a.srv.playableFor(t.Context(), owner, audio.ID); err != nil {
		t.Errorf("an audiobook is not playable: %v", err)
	}
}
