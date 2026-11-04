package web

import (
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The reading place API, on the /progress pattern: the same CSRF and body rules, and a form body so
// navigator.sendBeacon can post it. A reading place is a text anchor (sentence id and offset) with a quote
// and a percent, never a screen page. It is the signed-in person's own; listening progress is never touched.
// Other clients read the place with GET.

func (s *server) registerReading(mux *http.ServeMux) {
	mux.HandleFunc("GET /reading/{bookID}", s.getReading)
	mux.HandleFunc("POST /reading/{bookID}", s.postReading)
	mux.HandleFunc("POST /reading/{bookID}/finished", s.postReadingFinished)
}

// Bounds on what one save may add to the reading-speed sessions.
const (
	readingMaxCharsPerSave  = 200_000
	readingMaxActivePerSave = 10 * 60_000
)

// readingBook loads the book a reading route works on: the id is first followed through a merge, then the
// person must have share.ReadText on it. Anything else has answered 404.
func (s *server) readingBook(w http.ResponseWriter, r *http.Request) (library.Book, bool) {
	if s.ebooks == nil {
		s.notFound(w, r)
		return library.Book{}, false
	}
	id := r.PathValue("bookID")
	if to, ok := s.lib.MergedInto(r.Context(), id); ok {
		id = to
	}
	return s.loadBook(w, r, id, share.ReadText)
}

// readingJSON is a stored reading place as the client reads it.
func readingJSON(p progress.ReadingPosition) map[string]any {
	return map[string]any{
		"bookId":          p.BookID,
		"editionId":       p.EditionID,
		"chapterIdx":      p.ChapterIdx,
		"sid":             p.SID,
		"off":             p.Off,
		"quote":           p.Quote,
		"charPos":         p.CharPos,
		"percentBp":       p.PercentBP,
		"finished":        p.Finished(),
		"finishedAt":      p.FinishedAt,
		"clientUpdatedAt": p.ClientUpdatedAt,
		"serverUpdatedAt": p.ServerUpdatedAt,
		"deviceId":        p.DeviceID,
		"deviceLabel":     p.DeviceLabel,
	}
}

// getReading answers GET /reading/{bookID}: the signed-in person's own reading place, or null.
func (s *server) getReading(w http.ResponseWriter, r *http.Request) {
	b, ok := s.readingBook(w, r)
	if !ok {
		return
	}
	p, found, err := s.progress.Reading(r.Context(), currentUser(r).ID, b.ID)
	if err != nil {
		s.serverError(w, r, "load reading place", err)
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{"reading": nil, "serverNow": s.now().UnixMilli()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reading": readingJSON(p), "serverNow": s.now().UnixMilli()})
}

// postReadingFinished answers POST /reading/{bookID}/finished (finished=1, or 0 for "read again"): the person marks
// the book read at the end of the text. It writes the reading place only; listening progress is never touched
// (invariant 7), which is why the audiobook's /hx/books/{id}/finished is not used for text.
func (s *server) postReadingFinished(w http.ResponseWriter, r *http.Request) {
	b, ok := s.readingBook(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, formLimit)
	v := ""
	if err := r.ParseForm(); err == nil {
		v = r.PostForm.Get("finished")
	}
	htmx := r.Header.Get("HX-Request") == "true"
	if v != "1" && v != "0" {
		if htmx {
			s.fragment(w, r, http.StatusBadRequest, components.ErrorToast("That did not work. Try again."))
			return
		}
		http.Error(w, "That did not work. Try again.", http.StatusBadRequest)
		return
	}
	if _, err := s.progress.SetReadingFinished(r.Context(), currentUser(r).ID, b.ID, v == "1"); err != nil {
		s.serverError(w, r, "set reading finished", err)
		return
	}
	if htmx {
		s.fragment(w, r, http.StatusOK, components.ToastOOB("ok", readerFinished))
		return
	}
	http.Redirect(w, r, "/books/"+b.ID, http.StatusSeeOther)
}

// postReading answers POST /reading/{bookID}. The person comes from the session; the percent is worked out
// here from the position, so a client cannot claim one the text does not have.
func (s *server) postReading(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		jsonError(w, http.StatusUnsupportedMediaType, "unsupported", "")
		return
	}
	b, ok := s.readingBook(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, progressBodyLimit)
	if err := r.ParseForm(); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonError(w, http.StatusRequestEntityTooLarge, "too_large", "")
			return
		}
		jsonError(w, http.StatusBadRequest, "invalid", "")
		return
	}
	f := r.PostForm
	bad := func(field string) { jsonError(w, http.StatusUnprocessableEntity, "invalid", field) }
	ctx, u := r.Context(), currentUser(r)

	// The edition must be one of this book's, and its document must open: every other field is checked
	// against it.
	ed, found, err := s.ebooks.Store().Edition(ctx, f.Get("edition_id"))
	if err != nil {
		s.serverError(w, r, "reading edition", err)
		return
	}
	var file ebook.File
	if found {
		if file, found, err = s.ebooks.Store().File(ctx, ed.EbookFileID); err != nil {
			s.serverError(w, r, "reading file", err)
			return
		}
	}
	if !found || file.BookID != b.ID {
		bad("edition_id")
		return
	}
	doc, err := s.readerDocs.get(filepath.Join(s.dataDir, "text"), ed.DocSHA256)
	if errors.Is(err, fs.ErrNotExist) {
		bad("edition_id")
		return
	}
	if err != nil {
		s.serverError(w, r, "reading document", err)
		return
	}
	ch, err := strconv.Atoi(f.Get("chapter_idx"))
	if err != nil || ch < 1 || ch > len(doc.Chapters) {
		bad("chapter_idx")
		return
	}
	sid := f.Get("sid")
	off := 0
	if sid != "" {
		c, ok := rdrSIDExists(doc, sid)
		if !ok || c != ch {
			bad("sid")
			return
		}
		if off, err = strconv.Atoi(f.Get("off")); err != nil || off < 0 || off > rsClampOff(doc, sid, 1<<30) {
			bad("off")
			return
		}
	}
	quote := f.Get("quote")
	if utf8.RuneCountInString(quote) > 64 || !utf8.ValidString(quote) {
		bad("quote")
		return
	}
	charPos, err := strconv.Atoi(f.Get("char_pos"))
	if err != nil || charPos < 0 {
		bad("char_pos")
		return
	}
	_, total := rsCharsBefore(doc, 0)
	charPos = min(charPos, total)
	bp := 0
	if total > 0 {
		bp = min(10000, charPos*10000/total)
	}
	cu, err := strconv.ParseInt(f.Get("client_updated_at"), 10, 64)
	if err != nil || cu <= 0 {
		bad("client_updated_at")
		return
	}
	var chars, active int64
	for _, v := range []struct {
		name string
		to   *int64
		max  int64
	}{{"chars_read", &chars, readingMaxCharsPerSave}, {"active_ms", &active, readingMaxActivePerSave}} {
		if raw := f.Get(v.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 0 {
				bad(v.name)
				return
			}
			*v.to = min(n, v.max)
		}
	}
	label := deviceLabel(r.UserAgent())
	if d, ok := auth.DeviceFrom(ctx); ok {
		label = d.Name
	}
	pos, applied, err := s.progress.SaveReading(ctx, progress.ReadingSave{
		UserID: u.ID, BookID: b.ID, EditionID: ed.ID, ChapterIdx: ch, SID: sid, Off: off, Quote: quote,
		CharPos: charPos, PercentBP: bp, ClientUpdatedAt: cu, DeviceID: f.Get("device_id"), DeviceLabel: label,
	})
	var inv *progress.InvalidError
	switch {
	case errors.As(err, &inv):
		bad(inv.Field)
		return
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "save reading place", err)
		return
	}
	if chars > 0 || active > 0 {
		if err := s.progress.AddReadingTime(ctx, u.ID, pos.BookID, f.Get("device_id"), chars, active, s.now().UnixMilli()); err != nil {
			s.serverError(w, r, "save reading time", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"applied": applied, "reading": readingJSON(pos), "serverNow": s.now().UnixMilli()})
}
