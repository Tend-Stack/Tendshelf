package web

import (
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// POST /hx/books/{id}/speed remembers the signed-in person's speed for one book (table
// book_settings). With make_default=1 the speed becomes the person's usual speed instead and the
// book's own is dropped, so the book follows the usual speed again. Needs the right to listen to the
// book; the person comes from the session, never from the request.
func (s *server) postBookSpeed(w http.ResponseWriter, r *http.Request) {
	b, ok := s.playableBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	speed, err := strconv.ParseFloat(r.PostForm.Get("speed"), 64)
	if err != nil || math.IsNaN(speed) || math.IsInf(speed, 0) {
		s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msgSettingInvalid))
		return
	}
	u := currentUser(r)
	if r.PostForm.Get("make_default") == "1" {
		err = s.users.UseAsUsualSpeed(r.Context(), u.ID, b.ID, speed)
	} else {
		err = s.users.SetBookSpeed(r.Context(), u.ID, b.ID, speed)
	}
	switch {
	case errors.Is(err, users.ErrInvalidSetting):
		s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(msgSettingInvalid))
	case err != nil:
		s.serverError(w, r, "save book speed", err)
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	}
}
