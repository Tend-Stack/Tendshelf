package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// removeTextBookmark takes away the person's own bookmarks on one sentence (the page ribbon's second press). Having
// none is success. Only the signed-in person's rows are looked at, so nobody else's bookmark can be touched.
func (s *server) removeTextBookmark(w http.ResponseWriter, r *http.Request, rc readerCtx, ch int, sid string) {
	ctx, u := r.Context(), currentUser(r)
	list, err := s.progress.TextBookmarks(ctx, u.ID, rc.book.ID)
	if err != nil {
		s.serverError(w, r, "text bookmarks", err)
		return
	}
	for _, b := range list {
		if b.SID != sid || b.EditionID != rc.ed.ID {
			continue
		}
		if err := s.progress.DeleteBookmark(ctx, u.ID, b.ID); err != nil && !errors.Is(err, progress.ErrNotFound) {
			s.serverError(w, r, "remove text bookmark", err)
			return
		}
	}
	if r.Header.Get("HX-Request") == "true" {
		s.bookmarkAnswer(w, r, rc, ch, sid, readerBmRemoved)
		return
	}
	to := rc.href("ch", strconv.Itoa(ch), "at", sid) + "#s-" + sid
	if next := r.PostFormValue("next"); next != "" && auth.SafeNext(next) == next {
		to = next
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// bookmarkAnswer is the htmx answer to a text bookmark made or removed: the toast, and the sentence's block redrawn
// out of band so the little flag in the text and the ribbon on the page agree with the database.
func (s *server) bookmarkAnswer(w http.ResponseWriter, r *http.Request, rc readerCtx, ch int, sid, toast string) {
	var parts []templ.Component
	chap, err := s.readerChapter(r.Context(), currentUser(r), rc, ch, 0)
	if err != nil {
		s.serverError(w, r, "bookmark block", err)
		return
	}
	if bv, ok := chap.blocks[rdrBlockOf(sid)]; ok {
		parts = append(parts, pages.ReaderBlock(bv, true))
	}
	parts = append(parts, components.ToastOOB("ok", toast))
	s.fragment(w, r, http.StatusOK, templ.Join(parts...))
}

// postTextBookmark saves a bookmark made in the text: edition_id and sid name the sentence. It needs
// share.ReadText (not ListenBook), and a text bookmark stores no audio moment (position_ms is 0).
func (s *server) postTextBookmark(w http.ResponseWriter, r *http.Request, edID, sid string) {
	ctx, u := r.Context(), currentUser(r)
	if edID == "" || sid == "" {
		s.bookmarkRefuse(w, r, http.StatusBadRequest, bookmarkErrPosition)
		return
	}
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.ReadText)
	if !ok {
		return
	}
	if s.ebooks == nil {
		s.notFound(w, r)
		return
	}
	rc, found, err := s.readerLoad(ctx, u, b, edID)
	if err != nil {
		s.serverError(w, r, "text bookmark edition", err)
		return
	}
	ch, exists := 0, false
	if found {
		ch, exists = rdrSIDExists(rc.doc, sid)
	}
	if !found || !exists {
		s.notFound(w, r)
		return
	}
	if r.PostFormValue("remove") == "1" {
		s.removeTextBookmark(w, r, rc, ch, sid)
		return
	}
	bm, _, err := s.progress.AddTextBookmark(ctx, u.ID, b.ID, rc.ed.ID, sid, 0, r.PostFormValue("note"), r.PostFormValue("client_id"))
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
	case errors.Is(err, progress.ErrNotFound), errors.Is(err, progress.ErrClientIDTaken):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "add text bookmark", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.bookmarkAnswer(w, r, rc, ch, sid, readerBookmarked)
		return
	}
	to := rc.href("ch", strconv.Itoa(ch), "at", bm.SID) + "#s-" + bm.SID
	if next := r.PostFormValue("next"); next != "" && auth.SafeNext(next) == next {
		to = next
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
