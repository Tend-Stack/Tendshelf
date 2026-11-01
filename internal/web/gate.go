package web

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// gate is default deny: every path except /setup and /login (the static files and /healthz never get
// here) needs a signed-in, non-disabled person. The auth middleware already dropped sessions of
// disabled accounts, so "signed in" is just "has a user in the context". With no owner yet,
// everyone is sent to /setup; otherwise anonymous visitors go to /login with a way back.
func (s *server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := auth.UserFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		exists, err := s.auth.OwnerExists(r.Context())
		if err != nil {
			slog.Error("owner check", "err", err)
			s.errorPage(w, r, http.StatusInternalServerError)
			return
		}
		if !exists {
			s.redirect(w, r, "/setup")
			return
		}
		s.redirect(w, r, loginURL(r))
	})
}

// loginURL is /login with next set to where the person was going, when that is known and safe:
// the request itself for a plain GET or HEAD, the page the browser was on for an htmx request.
func loginURL(r *http.Request) string {
	var target string
	switch {
	case r.Header.Get("HX-Request") == "true":
		if cur, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && cur.Host == r.Host && cur.Host != "" {
			target = cur.RequestURI()
		}
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		target = r.URL.RequestURI()
	}
	if next := auth.SafeNext(target); next != "/" {
		return "/login?next=" + url.QueryEscape(next)
	}
	return "/login"
}

// redirect sends the browser to loc. htmx 4 follows a 3xx inside the request and would swap the
// login page into #main, so for an htmx request the answer is 204 with HX-Redirect, which makes it
// navigate the whole window (a 204 is never swapped).
func (s *server) redirect(w http.ResponseWriter, r *http.Request, loc string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", loc)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, loc, http.StatusSeeOther)
}
