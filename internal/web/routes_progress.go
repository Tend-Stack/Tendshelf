package web

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// progressBodyLimit bounds a progress save: a handful of short fields.
const progressBodyLimit = 4 << 10

// nowOrReal returns the injected clock, or time.Now when there is none.
func nowOrReal(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}
	return now
}

func (s *server) registerProgress(mux *http.ServeMux) {
	mux.HandleFunc("GET /progress/{bookID}", s.getProgress)
	mux.HandleFunc("POST /progress/{bookID}", s.postProgress)
	mux.HandleFunc("POST /hx/books/{id}/finished", s.postFinished)
}

// writeJSON answers a JSON body that is never cached: it belongs to one person.
func writeJSON(w http.ResponseWriter, status int, v any) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// jsonError answers {"error":{"code":..,"field":..}}; field is left out when empty.
func jsonError(w http.ResponseWriter, status int, code, field string) {
	e := map[string]string{"code": code}
	if field != "" {
		e["field"] = field
	}
	writeJSON(w, status, map[string]any{"error": e})
}

// playableFor loads a book the person may listen to. A hidden, unknown, refused or missing book is
// library.ErrNotFound, whatever the reason, so a caller can answer the same 404 for all of them.
func (s *server) playableFor(ctx context.Context, u users.User, id string) (library.Book, error) {
	b, err := s.lib.Book(ctx, id)
	if err != nil {
		return library.Book{}, err
	}
	if !s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) ||
		b.RefusedReason != "" || b.MissingSince != 0 || b.Kind == library.BookText {
		return library.Book{}, library.ErrNotFound
	}
	return b, nil
}

// playableBook loads a book the person may listen to. A hidden, unknown, refused or missing book
// answers 404 (already written) and nothing is read or written for it.
func (s *server) playableBook(w http.ResponseWriter, r *http.Request, id string) (library.Book, bool) {
	b, ok := s.loadBook(w, r, id, share.ListenBook)
	if !ok {
		return library.Book{}, false
	}
	if b.RefusedReason != "" || b.MissingSince != 0 || b.Kind == library.BookText {
		s.notFound(w, r)
		return library.Book{}, false
	}
	return b, true
}

// progressJSON is the stored progress as the client reads it.
func progressJSON(p progress.Progress) map[string]any {
	return map[string]any{
		"positionMs":      p.PositionMs,
		"durationMs":      p.DurationMs,
		"finished":        p.Finished(),
		"finishedAt":      p.FinishedAt,
		"clientUpdatedAt": p.ClientUpdatedAt,
		"serverUpdatedAt": p.ServerUpdatedAt,
		"deviceId":        p.DeviceID,
		"deviceLabel":     p.DeviceLabel,
	}
}

// getProgress answers GET /progress/{bookID}: the signed-in person's own progress, or null.
func (s *server) getProgress(w http.ResponseWriter, r *http.Request) {
	b, ok := s.playableBook(w, r, r.PathValue("bookID"))
	if !ok {
		return
	}
	p, err := s.progress.Get(r.Context(), currentUser(r).ID, b.ID)
	if errors.Is(err, progress.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"progress": nil, "serverNow": s.now().UnixMilli()})
		return
	}
	if err != nil {
		s.serverError(w, r, "load progress", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"progress": progressJSON(p), "serverNow": s.now().UnixMilli()})
}

// flag reads an optional "0"/"1" field.
func flag(v string) (val, ok bool) {
	switch v {
	case "", "0":
		return false, true
	case "1":
		return true, true
	}
	return false, false
}

// progressInput is one parsed progress save, from the form or the JSON API.
type progressInput struct {
	PositionMs, ClientUpdatedAt, ListenedMs int64
	DeviceID                                string
	Finished, Force                         bool
	Speed                                   float64
}

// saveProgress hands a parsed save for the signed-in person to progress.Service, which holds the clamp
// and the newest-wins rule. The label is the device's name for a token client, else read from the
// browser's user agent.
func (s *server) saveProgress(r *http.Request, b library.Book, in progressInput) (progress.Result, error) {
	label := deviceLabel(r.UserAgent())
	if d, ok := auth.DeviceFrom(r.Context()); ok {
		label = d.Name
	}
	return s.progress.Save(r.Context(), progress.Update{
		UserID: currentUser(r).ID, BookID: b.ID, PositionMs: in.PositionMs, DurationMs: b.DurationMs, Finished: in.Finished,
		ClientUpdatedAt: in.ClientUpdatedAt, DeviceID: in.DeviceID, DeviceLabel: label,
		Force: in.Force, Speed: in.Speed, ListenedMs: in.ListenedMs,
	})
}

// postProgress answers POST /progress/{bookID}. The person comes from the session and the duration
// from the book; the newest-wins rule and the clamp live in progress.Service.
func (s *server) postProgress(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		jsonError(w, http.StatusUnsupportedMediaType, "unsupported", "")
		return
	}
	b, ok := s.playableBook(w, r, r.PathValue("bookID"))
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
	bad := func(field string) { jsonError(w, http.StatusBadRequest, "invalid", field) }

	pos, err := strconv.ParseInt(f.Get("position_ms"), 10, 64)
	if err != nil || pos < 0 {
		bad("position_ms")
		return
	}
	cu, err := strconv.ParseInt(f.Get("client_updated_at"), 10, 64)
	if err != nil || cu <= 0 {
		bad("client_updated_at")
		return
	}
	if f.Get("device_id") == "" {
		bad("device_id")
		return
	}
	finished, ok := flag(f.Get("finished"))
	if !ok {
		bad("finished")
		return
	}
	force, ok := flag(f.Get("force"))
	if !ok {
		bad("force")
		return
	}
	var speed float64
	if v := f.Get("speed"); v != "" {
		speed, err = strconv.ParseFloat(v, 64)
		if err != nil || math.IsNaN(speed) || math.IsInf(speed, 0) {
			bad("speed")
			return
		}
	}
	var listened int64
	if v := f.Get("listened_ms"); v != "" {
		listened, err = strconv.ParseInt(v, 10, 64)
		if err != nil || listened < 0 {
			bad("listened_ms")
			return
		}
	}
	res, err := s.saveProgress(r, b, progressInput{
		PositionMs: pos, ClientUpdatedAt: cu, DeviceID: f.Get("device_id"), Finished: finished,
		Force: force, Speed: speed, ListenedMs: listened,
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
		s.serverError(w, r, "save progress", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"applied": res.Applied, "progress": progressJSON(res.Progress), "serverNow": s.now().UnixMilli(),
	})
}

// postFinished answers POST /hx/books/{id}/finished with finished=1 or 0: htmx gets 204 and a
// trigger, a plain form is sent on to next (a safe local path) or back to the book.
func (s *server) postFinished(w http.ResponseWriter, r *http.Request) {
	var b library.Book
	var ok bool
	if tb, err := s.lib.Book(r.Context(), r.PathValue("id")); err == nil && tb.Kind == library.BookText {
		// A text book is marked on the reading side: it needs the right to read and a ready edition.
		if b, ok = s.loadBook(w, r, tb.ID, share.ReadText); ok && !s.readyOr404(w, r, b) {
			return
		}
	} else {
		b, ok = s.playableBook(w, r, r.PathValue("id"))
	}
	if !ok {
		return
	}
	htmx := r.Header.Get("HX-Request") == "true"
	r.Body = http.MaxBytesReader(w, r.Body, formLimit)
	v := ""
	if err := r.ParseForm(); err == nil {
		v = r.PostForm.Get("finished")
	}
	if v != "1" && v != "0" {
		if htmx {
			s.fragment(w, r, http.StatusBadRequest, components.ErrorToast("That did not work. Try again."))
			return
		}
		http.Error(w, "That did not work. Try again.", http.StatusBadRequest)
		return
	}
	var err error
	if b.Kind == library.BookText {
		_, err = s.progress.SetReadingFinished(r.Context(), currentUser(r).ID, b.ID, v == "1")
	} else {
		_, err = s.progress.SetFinished(r.Context(), currentUser(r).ID, b.ID, v == "1", b.DurationMs)
	}
	if err != nil {
		s.serverError(w, r, "set finished", err)
		return
	}
	if htmx {
		w.Header().Set("HX-Trigger", "progress-saved")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	dest := "/books/" + b.ID
	if next := r.PostForm.Get("next"); next != "" && auth.SafeNext(next) == next {
		dest = next
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

// readyOr404 answers 404 for a text book with no ready edition to read, and says whether the caller may go on.
func (s *server) readyOr404(w http.ResponseWriter, r *http.Request, b library.Book) bool {
	ok, err := s.mayReadBook(r.Context(), currentUser(r), b)
	if err != nil {
		s.serverError(w, r, "ready edition", err)
		return false
	}
	if !ok {
		s.notFound(w, r)
	}
	return ok
}
