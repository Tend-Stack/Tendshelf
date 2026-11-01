package web

import (
	"errors"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

func (s *server) registerPlayer(mux *http.ServeMux) {
	mux.HandleFunc("GET /hx/player", s.getCurrentPlayer)
	mux.HandleFunc("GET /hx/player/{bookID}", s.getPlayer)
}

// getPlayer answers GET /hx/player/{bookID}: the data-only PlayerData fragment for a book the
// person may listen to.
func (s *server) getPlayer(w http.ResponseWriter, r *http.Request) {
	b, ok := s.playableBook(w, r, r.PathValue("bookID"))
	if !ok {
		return
	}
	s.renderPlayer(w, r, b)
}

func (s *server) renderPlayer(w http.ResponseWriter, r *http.Request, b library.Book) {
	v, err := s.playerView(r.Context(), currentUser(r), s.scope(r), b)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "build player", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.PlayerData(v))
}

// getCurrentPlayer answers GET /hx/player: the person's newest unfinished visible book, or 204 when
// there is none (htmx and fetch both leave the page alone).
func (s *server) getCurrentPlayer(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ls, err := s.lib.ContinueListening(r.Context(), s.scope(r), u.ID, 1)
	if err != nil {
		s.serverError(w, r, "continue listening", err)
		return
	}
	if len(ls) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b, err := s.lib.Book(r.Context(), ls[0].Book.ID)
	if errors.Is(err, library.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.serverError(w, r, "load book", err)
		return
	}
	if !s.can.Can(r.Context(), u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) ||
		b.RefusedReason != "" || b.MissingSince != 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.renderPlayer(w, r, b)
}
