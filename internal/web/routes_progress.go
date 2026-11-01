package web

import (
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

// playableBook loads a book the person may listen to. A hidden, unknown, refused or missing book
// answers 404 (already written) and nothing is read or written for it.
func (s *server) playableBook(w http.ResponseWriter, r *http.Request, id string) (library.Book, bool) {
	b, ok := s.loadBook(w, r, id, share.ListenBook)
	if !ok {
		return library.Book{}, false
	}
	if b.RefusedReason != "" || b.MissingSince != 0 {
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
	u := currentUser(r)
	res, err := s.progress.Save(r.Context(), progress.Update{
		UserID: u.ID, BookID: b.ID, PositionMs: pos, DurationMs: b.DurationMs, Finished: finished,
		ClientUpdatedAt: cu, DeviceID: f.Get("device_id"), DeviceLabel: deviceLabel(r.UserAgent()),
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
	b, ok := s.playableBook(w, r, r.PathValue("id"))
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
	if _, err := s.progress.SetFinished(r.Context(), currentUser(r).ID, b.ID, v == "1", b.DurationMs); err != nil {
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
