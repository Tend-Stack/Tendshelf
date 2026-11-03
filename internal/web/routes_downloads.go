package web

import (
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

func (s *server) registerDownloads(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/downloads", s.getDownloads)
}

// getDownloads is the Downloads page. Offline saving is not built yet, so it is an honest empty state.
func (s *server) getDownloads(w http.ResponseWriter, r *http.Request) {
	s.page(w, r, http.StatusOK, "Downloads", "downloads", pages.Downloads())
}
