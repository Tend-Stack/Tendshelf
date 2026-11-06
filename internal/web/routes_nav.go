package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// registerNav wires the sidebar's collapse toggle: the plain form post and its htmx twin, which
// answers with the re-rendered sidebar alone so the player keeps playing.
func (s *server) registerNav(mux *http.ServeMux) {
	mux.HandleFunc("POST /me/nav", s.postNav)
	mux.HandleFunc("POST /hx/me/nav", s.postNavHX)
}

// saveNav stores the ui.nav value of the form; it reports false after answering a bad value.
func (s *server) saveNav(w http.ResponseWriter, r *http.Request) (rail, ok bool) {
	if !s.parseForm(w, r) {
		return false, false
	}
	mode := r.PostForm.Get("nav")
	if mode != "full" && mode != "rail" {
		s.errorPage(w, r, http.StatusBadRequest)
		return false, false
	}
	u := currentUser(r)
	cur, err := s.users.LoadSettings(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, "load settings", err)
		return false, false
	}
	cur.Nav = mode
	if err := s.users.SaveSettings(r.Context(), u.ID, cur); err != nil {
		s.serverError(w, r, "save nav", err)
		return false, false
	}
	return mode == "rail", true
}

func (s *server) postNav(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.saveNav(w, r); !ok {
		return
	}
	http.Redirect(w, r, sameOriginPath(r.Referer(), r.Host), http.StatusSeeOther)
}

func (s *server) postNavHX(w http.ResponseWriter, r *http.Request) {
	rail, ok := s.saveNav(w, r)
	if !ok {
		return
	}
	showUsers, waiting := s.usersLink(r)
	current := navKeyFor(pathOf(r.Header.Get("HX-Current-URL")))
	admin := s.can.Can(r.Context(), currentUser(r), share.ManageLibraries, share.Target{Kind: share.TargetServer})
	s.fragment(w, r, http.StatusOK, components.Sidebar(current, admin,
		components.UsersLink{Show: showUsers, Waiting: waiting}, s.sidebarShelves(r), rail, s.csrf.TokenFrom(r.Context()), false, shellFrom(r)))
}

// sameOriginPath is where the no-JavaScript toggle returns: the path and query of the referrer when
// it names this host (or is a plain path), else the home page. It never returns an address that
// leaves the site.
func sameOriginPath(referer, host string) string {
	u, err := url.Parse(referer)
	if err != nil || referer == "" {
		return "/"
	}
	if u.Host != "" && u.Host != host {
		return "/"
	}
	p := u.EscapedPath()
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(u.Path, `\`) {
		return "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

// pathOf is the path of an absolute URL, "" when it does not parse.
func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}

// navKeyFor is the Page.Current key of a path, so the re-rendered sidebar marks the page the person
// is on (the shell script does this after boosted swaps, but the fragment is rendered by the server).
func navKeyFor(path string) string {
	best, bestLen := "", 0
	for _, it := range components.MainNav {
		match := path == it.Href || (it.Href != "/" && strings.HasPrefix(path, it.Href+"/"))
		if match && len(it.Href) > bestLen {
			best, bestLen = it.Key, len(it.Href)
		}
	}
	switch {
	case path == "/shelves":
		return "shelves"
	case strings.HasPrefix(path, "/shelves/"):
		return "shelf-" + strings.Split(strings.TrimPrefix(path, "/shelves/"), "/")[0]
	case path == "/admin/users" || strings.HasPrefix(path, "/admin/users/"):
		return "admin-users"
	case path == "/admin/libraries" || strings.HasPrefix(path, "/admin/libraries/"):
		return "admin-libraries"
	}
	return best
}
