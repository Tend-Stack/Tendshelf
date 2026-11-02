package web

import (
	"cmp"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Bookmarks and notes (P1c-7a). A bookmark is its owner's alone: every route takes the person from
// the session, asks the progress service for their own bookmark (another person's id and an unknown
// one both answer 404), and then checks that they can still listen to the book. Writes are plain
// POST forms with the CSRF field that answer 303 (the note sheet posts through htmx and gets a toast
// and the refreshed list instead, so the book keeps playing); the notes never reach a log.

// bookmarkNow is the clock of the day words; tests replace it.
var bookmarkNow = time.Now

func (s *server) registerBookmarks(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/bookmarks", s.postBookmark)
	mux.HandleFunc("POST /bookmarks/{id}/note", s.postBookmarkNote)
	mux.HandleFunc("POST /bookmarks/{id}/delete", s.postBookmarkDelete)
	mux.HandleFunc("GET /books/{id}/bookmarks/export", s.getBookmarkExport)
}

// bookmarkRefuse answers a refused form in plain words: a toast fragment for htmx, text otherwise.
func (s *server) bookmarkRefuse(w http.ResponseWriter, r *http.Request, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
		return
	}
	http.Error(w, msg, status)
}

func (s *server) postBookmark(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.ListenBook)
	if !ok {
		return
	}
	pos, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("position_ms")), 10, 64)
	if err != nil || pos < 0 {
		s.bookmarkRefuse(w, r, http.StatusBadRequest, bookmarkErrPosition)
		return
	}
	if b.DurationMs > 0 && pos > b.DurationMs {
		pos = b.DurationMs
	}
	bm, err := s.progress.AddBookmark(ctx, currentUser(r).ID, b.ID, pos, r.PostFormValue("note"))
	var invalid *progress.InvalidError
	switch {
	case errors.As(err, &invalid) && invalid.Field == "note":
		s.bookmarkRefuse(w, r, http.StatusBadRequest, bookmarkErrTooLong)
		return
	case errors.As(err, &invalid):
		s.bookmarkRefuse(w, r, http.StatusBadRequest, bookmarkErrPosition)
		return
	case errors.Is(err, progress.ErrLimit):
		s.bookmarkRefuse(w, r, http.StatusConflict, bookmarkErrLimit)
		return
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "add bookmark", err)
		return
	}
	if strings.HasPrefix(r.Header.Get("Accept"), "application/json") {
		d, err := s.lib.Chapters(ctx, b.ID)
		if err != nil {
			s.serverError(w, r, "bookmark chapters", err)
			return
		}
		_, clock := bookmarkWhere(d, bm.PositionMs)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			ID         string `json:"id"`
			PositionMs int64  `json:"positionMs"`
			Label      string `json:"label"`
			NoteHref   string `json:"noteHref"` // the server owns addresses; the player's toast links here
		}{bm.ID, bm.PositionMs, clock, bookmarkSheetHref(b.ID, bm.ID)})
		return
	}
	to := bookmarkTabHref(b.ID) + "?added=" + url.QueryEscape(bm.ID)
	if next := r.PostFormValue("next"); next != "" && auth.SafeNext(next) == next {
		to = next
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ownBookmark loads the person's own live bookmark named by the path and checks they can still
// listen to its book. Anything else has answered 404.
func (s *server) ownBookmark(w http.ResponseWriter, r *http.Request) (progress.Bookmark, bool) {
	bm, err := s.progress.Bookmark(r.Context(), currentUser(r).ID, r.PathValue("id"))
	if errors.Is(err, progress.ErrNotFound) {
		s.notFound(w, r)
		return bm, false
	}
	if err != nil {
		s.serverError(w, r, "load bookmark", err)
		return bm, false
	}
	if _, ok := s.loadBook(w, r, bm.BookID, share.ListenBook); !ok {
		return bm, false
	}
	return bm, true
}

func (s *server) postBookmarkNote(w http.ResponseWriter, r *http.Request) {
	bm, ok := s.ownBookmark(w, r)
	if !ok {
		return
	}
	_, err := s.progress.SetBookmarkNote(r.Context(), currentUser(r).ID, bm.ID, r.PostFormValue("note"))
	var invalid *progress.InvalidError
	switch {
	case errors.As(err, &invalid) && r.Header.Get("HX-Request") == "true":
		s.bookmarkRefuse(w, r, http.StatusBadRequest, bookmarkErrTooLong)
		return
	case errors.As(err, &invalid):
		http.Redirect(w, r, bookmarkSheetHref(bm.BookID, bm.ID)+"&error=note_too_long", http.StatusSeeOther)
		return
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "save bookmark note", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.bookmarkSheetDone(w, r, bm.BookID, bookmarkNoteSaved)
		return
	}
	http.Redirect(w, r, bookmarkTabHref(bm.BookID), http.StatusSeeOther)
}

func (s *server) postBookmarkDelete(w http.ResponseWriter, r *http.Request) {
	bm, ok := s.ownBookmark(w, r)
	if !ok {
		return
	}
	err := s.progress.DeleteBookmark(r.Context(), currentUser(r).ID, bm.ID)
	if errors.Is(err, progress.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "delete bookmark", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.bookmarkSheetDone(w, r, bm.BookID, bookmarkDeleted)
		return
	}
	http.Redirect(w, r, bookmarkTabHref(bm.BookID), http.StatusSeeOther)
}

// bookmarkSheetDone answers a note saved or a bookmark deleted from the sheet through htmx. The
// form swaps nothing in place; the answer empties #sheet out of band, adds a toast and, when
// the Bookmarks tab is the page behind it, swaps in the refreshed chips and list out of band. The
// page is never reloaded, so the audio element keeps playing. A refresh that fails only logs: the
// change is saved and the toast says so.
func (s *server) bookmarkSheetDone(w http.ResponseWriter, r *http.Request, bookID, msg string) {
	parts := []templ.Component{components.SheetClosedOOB(), components.ToastOOB("ok", msg)}
	items, err := s.bookmarkItemsOOB(r, bookID)
	if err != nil {
		slog.Error("refresh bookmark list", "err", err)
	} else if items != nil {
		parts = append(parts, items)
	}
	s.fragment(w, r, http.StatusOK, templ.Join(parts...))
}

// bookmarkItemsOOB renders the chips and the list for the person's current filter when the htmx
// request came from the book's Bookmarks tab; any other page has no such region and gets nil.
func (s *server) bookmarkItemsOOB(r *http.Request, bookID string) (templ.Component, error) {
	cur, err := url.Parse(r.Header.Get("HX-Current-URL"))
	if err != nil || cur.Path != bookmarkTabHref(bookID) {
		return nil, nil
	}
	chapters, err := s.lib.Chapters(r.Context(), bookID)
	if err != nil {
		return nil, err
	}
	// The list answers to the filter only; the sheet and "added" parts of the address are gone.
	req := r.Clone(r.Context())
	req.URL = &url.URL{Path: cur.Path}
	if cur.Query().Get("only") == "notes" {
		req.URL.RawQuery = "only=notes"
	}
	v, _, err := s.bookmarksPanel(req, library.BookDetail{Book: library.Book{ID: bookID}, Chapters: chapters})
	if err != nil {
		return nil, err
	}
	return pages.BookmarkItems(v, true), nil
}

func (s *server) getBookmarkExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.ListenBook)
	if !ok {
		return
	}
	format := r.URL.Query().Get("format")
	if format != "txt" && format != "md" {
		http.Error(w, "Choose plain text or Markdown.", http.StatusBadRequest)
		return
	}
	list, err := s.progress.Bookmarks(ctx, currentUser(r).ID, b.ID, false)
	if err != nil {
		s.serverError(w, r, "export bookmarks", err)
		return
	}
	chapters, err := s.lib.Chapters(ctx, b.ID)
	if err != nil {
		s.serverError(w, r, "export bookmark chapters", err)
		return
	}
	slices.SortStableFunc(list, func(a, b progress.Bookmark) int {
		return cmp.Or(cmp.Compare(a.PositionMs, b.PositionMs), cmp.Compare(a.CreatedAt, b.CreatedAt), strings.Compare(a.ID, b.ID))
	})
	md := format == "md"
	ctype := "text/plain; charset=utf-8"
	if md {
		ctype = "text/markdown; charset=utf-8"
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", `attachment; filename="`+bookmarkSlug(b.Title)+`-bookmarks.`+format+`"`)
	h.Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(bookmarkExportBody(b.Title, chapters, list, md)))
}
