package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// This file is the one place for the access helpers every area uses. A refusal by share.Can is
// always a 404 (no existence leak); 403 stays the CSRF answer (see server.forbidden).

// currentUser returns the signed-in person, or the zero User when there is none. share.Can refuses
// the zero User.
func currentUser(r *http.Request) users.User {
	u, _ := auth.UserFrom(r.Context())
	return u
}

// scope returns the grant filters for listing queries: Books with alias b, Libraries with alias l.
func (s *server) scope(r *http.Request) library.Scope {
	u := currentUser(r)
	return library.Scope{Books: s.can.BookFilter(u, "b"), Libraries: s.can.LibraryFilter(u, "l"), Shared: s.can.SharedBooks(u)}
}

// notFound answers 404: a toast fragment for htmx, the error page otherwise.
func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	if isAPIPath(r.URL.Path) {
		api.StatusError(w, http.StatusNotFound)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, http.StatusNotFound, components.ErrorToast("We couldn't find that."))
		return
	}
	s.errorPage(w, r, http.StatusNotFound)
}

// plainError answers a short plain-text error for the media routes (covers, stream, peaks): the
// JSON shape under /api/, http.Error elsewhere so the link routes keep their exact bytes.
func (s *server) plainError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	plainError(w, r, status, msg)
}

func plainError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if isAPIPath(r.URL.Path) {
		api.StatusError(w, status)
		return
	}
	http.Error(w, msg, status)
}

// allow reports whether the signed-in person may do a to t. When not, it has already answered 404.
func (s *server) allow(w http.ResponseWriter, r *http.Request, a share.Action, t share.Target) bool {
	if s.can.Can(r.Context(), currentUser(r), a, t) {
		return true
	}
	s.notFound(w, r)
	return false
}

// loadBook loads a book the signed-in person may do a to. An unknown id and a refused action both
// answer 404; any other failure is a 500.
func (s *server) loadBook(w http.ResponseWriter, r *http.Request, id string, a share.Action) (library.Book, bool) {
	b, err := s.lib.Book(r.Context(), id)
	if errors.Is(err, library.ErrNotFound) {
		s.redirectMerged(w, r, id, a)
		return library.Book{}, false
	}
	if err != nil {
		s.serverError(w, r, "load book", err)
		return library.Book{}, false
	}
	if !s.allow(w, r, a, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) {
		return library.Book{}, false
	}
	return b, true
}

// redirectMerged answers for an id that no longer names a book. If the id belonged to a book that was merged into
// another (a text book into an audiobook, or one copy of an audiobook into another), a GET of its page, its reader
// or its API record goes (308) to the same address on the book that took over, but only when the person may do the
// same thing to that book; otherwise it is the usual 404 and nothing is revealed.
func (s *server) redirectMerged(w http.ResponseWriter, r *http.Request, id string, a share.Action) {
	page, read, api := "/books/"+id, "/books/"+id+"/read", "/api/v1/books/"+id
	if r.Method == http.MethodGet && (r.URL.Path == page || r.URL.Path == read || r.URL.Path == api) {
		if to, ok := s.lib.MergedInto(r.Context(), id); ok {
			if b, err := s.lib.Book(r.Context(), to); err == nil &&
				s.can.Can(r.Context(), currentUser(r), a, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) {
				dest := "/books/" + to + strings.TrimPrefix(r.URL.Path, page)
				if r.URL.Path == api {
					dest = "/api/v1/books/" + to
				}
				if r.URL.RawQuery != "" {
					dest += "?" + r.URL.RawQuery
				}
				http.Redirect(w, r, dest, http.StatusPermanentRedirect)
				return
			}
		}
	}
	s.notFound(w, r)
}

// followMerged returns the id of the book that took over a merged-away book, or id itself. Writes that a client
// may have queued before a merge (progress, bookmarks) use it, so an offline outbox lands on the kept book; the
// kept book's own right is then checked as usual.
func (s *server) followMerged(ctx context.Context, id string) string {
	if to, ok := s.lib.MergedInto(ctx, id); ok {
		return to
	}
	return id
}

// coverURL is the address of a book's cover: /covers/<bookID>?w=<w>&v=<sha[:8]>. w 0 omits the
// width (the full-size image); an empty sha means the book has no cover and returns "". The v
// parameter changes with the image, so the response can be cached for good.
func coverURL(bookID, sha string, w int) string {
	if sha == "" {
		return ""
	}
	v := sha
	if len(v) > 8 {
		v = v[:8]
	}
	u := "/covers/" + bookID + "?"
	if w > 0 {
		u += "w=" + strconv.Itoa(w) + "&"
	}
	return u + "v=" + v
}
