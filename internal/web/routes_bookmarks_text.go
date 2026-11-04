package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// postTextBookmark saves a bookmark made in the text (M5 R4 P2): edition_id and sid name the sentence. It needs
// share.ReadText (not ListenBook), and the audio moment is the sentence's start, the end of the previous timed
// sentence when the sentence is a gap, and 0 with no alignment.
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
	var pos int64
	if row := rc.rows[ch]; rc.hasAl && row.State == align.ChapterAligned && row.ChapterKey != "" {
		if t, err := align.ReadTiming(s.readerAlignDir(), row.ChapterKey); err == nil {
			pos = rdrStartAt(t, sid)
		}
	}
	if b.DurationMs > 0 && pos > b.DurationMs {
		pos = b.DurationMs
	}
	bm, _, err := s.progress.AddTextBookmark(ctx, u.ID, b.ID, rc.ed.ID, sid, pos, r.PostFormValue("note"), r.PostFormValue("client_id"))
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
		s.fragment(w, r, http.StatusOK, templ.Join(components.ToastOOB("ok", readerBookmarked)))
		return
	}
	to := rc.href("ch", strconv.Itoa(ch), "at", bm.SID) + "#s-" + bm.SID
	if next := r.PostFormValue("next"); next != "" && auth.SafeNext(next) == next {
		to = next
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// rdrStartAt is the audio start of a sentence: its own start when it is timed, else the end of the nearest
// timed sentence before it, else 0.
func rdrStartAt(t *align.Timing, sid string) int64 {
	var prev int64
	for i, id := range t.SID {
		if id == sid && t.Gap[i] == nil && t.SS[i] >= 0 {
			return t.SS[i]
		}
		if ebook.CompareSID(id, sid) >= 0 {
			break
		}
		if t.Gap[i] == nil && t.SE[i] >= 0 {
			prev = t.SE[i]
		}
	}
	return prev
}
