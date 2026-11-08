package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// postLibraryView saves the Library view the person chose (Grid, List or Shelf, library.view in
// user_settings) and returns to the page they were on. The page drops view= from the address it
// sends, so the redirect opens in the saved view. The write is a post with the CSRF token, like
// every preference; the choice works without JavaScript.
func (s *server) postLibraryView(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	view := r.PostForm.Get("view")
	if !users.ValidLibraryView(view) {
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	if err := s.users.SaveLibraryView(r.Context(), currentUser(r).ID, view); err != nil {
		s.serverError(w, r, "save library view", err)
		return
	}
	http.Redirect(w, r, libraryViewReturn(r.PostForm.Get("next"), r.Host), http.StatusSeeOther)
}

// libraryViewReturn is where the choice goes back to: a Library page of this site, with no view=
// (the saved one now applies) and no cursor. Anything else returns to /libraries.
func libraryViewReturn(next, host string) string {
	p := sameOriginPath(next, host)
	u, err := url.Parse(p)
	if err != nil || (u.Path != "/libraries" && !strings.HasPrefix(u.Path, "/libraries/")) {
		return "/libraries"
	}
	q := u.Query()
	q.Del("view")
	q.Del("cursor")
	if enc := q.Encode(); enc != "" {
		return u.Path + "?" + enc
	}
	return u.Path
}
