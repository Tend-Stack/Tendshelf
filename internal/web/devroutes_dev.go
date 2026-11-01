//go:build dev

package web

import (
	"net/http"
	"strconv"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// registerDev adds the developer pages: the kitchen sink for the utilities and the motion layer,
// and the fragments its buttons call. Release builds compile the empty version in devroutes_prod.go.
func registerDev(mux *http.ServeMux, s *server) {
	mux.HandleFunc("GET /dev/kitchen-sink", func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			// Theme switch for the visual tests: set the cookie, then show the page without the query.
			http.SetCookie(w, &http.Cookie{Name: themeCookie, Value: theme, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, "/dev/kitchen-sink", http.StatusSeeOther)
			return
		}
		var sheet templ.Component // nil: no sheet
		if r.URL.Query().Get("sheet") == "1" {
			sheet = pages.KitchenSheet()
		}
		s.pageSheet(w, r, http.StatusOK, "Kitchen sink", "", pages.KitchenSink(), sheet)
	})
	mux.HandleFunc("GET /dev/kitchen-sink/b", func(w http.ResponseWriter, r *http.Request) {
		s.page(w, r, http.StatusOK, "Kitchen sink, page B", "", pages.KitchenSinkB())
	})
	mux.HandleFunc("POST /hx/dev/toast", func(w http.ResponseWriter, r *http.Request) {
		messages := map[string]string{
			"ok":   "Bookmarked at 14:06",
			"hint": "Rewound 5 s after your break",
			"err":  "Couldn't reach your server.",
		}
		kind := r.URL.Query().Get("kind")
		msg, ok := messages[kind]
		if !ok {
			s.fragment(w, r, http.StatusBadRequest, components.ErrorToast("That isn't a toast kind."))
			return
		}
		s.fragment(w, r, http.StatusOK, components.ToastOOB(kind, msg))
	})
	mux.HandleFunc("GET /hx/dev/progress", func(w http.ResponseWriter, r *http.Request) {
		v, err := strconv.Atoi(r.URL.Query().Get("v"))
		if err != nil || v < 0 || v > 100 {
			s.fragment(w, r, http.StatusBadRequest, components.ErrorToast("Use a whole number from 0 to 100."))
			return
		}
		s.fragment(w, r, http.StatusOK, pages.KitchenBar(v))
	})
}
