package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// gate is default deny: every path except /setup, /login and the invite and share links (the static files and /healthz never get
// here) needs a signed-in, non-disabled person. The auth middleware already dropped sessions of
// disabled accounts, so "signed in" is just "has a user in the context". With no owner yet,
// everyone is sent to /setup; otherwise anonymous visitors go to /login with a way back.
func (s *server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" || r.URL.Path == "/login" || isInvitePath(r.URL.Path) || isLinkPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == apiTokenPath {
			next.ServeHTTP(w, r) // token sign-in: the password in the body is the credential
			return
		}
		if _, ok := auth.UserFrom(r.Context()); ok {
			next.ServeHTTP(w, r)
			return
		}
		if isAPIPath(r.URL.Path) {
			api.Unauthorized(w, "Sign in first.") // JSON clients never get a redirect to /login
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

// isAPIPath reports whether p is under /api/, the JSON area.
func isAPIPath(p string) bool { return strings.HasPrefix(p, "/api/") }

// loginURL is /login with next set to where the person was going, when that is known and safe:
// the request itself for a plain or boosted GET or HEAD (a boosted link click is a navigation), the
// page the browser was on for any other htmx request.
func loginURL(r *http.Request) string {
	var target string
	isGet := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case isGet && r.Header.Get("HX-Boosted") == "true":
		target = r.URL.RequestURI()
	case r.Header.Get("HX-Request") == "true":
		if cur, err := url.Parse(r.Header.Get("HX-Current-URL")); err == nil && cur.Host == r.Host && cur.Host != "" {
			target = cur.RequestURI()
		}
	case isGet:
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

// invitePrefix starts the only other public paths: /invite/{token} and /invite/{token}/cover.
const invitePrefix = "/invite/"

// isInvitePath reports whether p is /invite/{token} or /invite/{token}/cover, with one non-empty
// token segment. Anything else under /invite/ stays behind the gate.
func isInvitePath(p string) bool {
	rest, ok := strings.CutPrefix(p, invitePrefix)
	if !ok || rest == "" {
		return false
	}
	tok, tail, more := strings.Cut(rest, "/")
	if tok == "" {
		return false
	}
	return !more || tail == "cover"
}

// linkPrefix starts the share-link paths, public like the invite pages and for the same reason: the
// token in the path is the credential.
const linkPrefix = "/s/"

// isLinkPath reports whether p is /s/{token}, /s/{token}/cover, /s/{token}/stream/{id} or
// /s/{token}/peaks/{id}, every segment non-empty and nothing after. Anything else under /s/ stays
// behind the gate.
func isLinkPath(p string) bool {
	rest, ok := strings.CutPrefix(p, linkPrefix)
	if !ok || rest == "" {
		return false
	}
	parts := strings.Split(rest, "/")
	for _, seg := range parts {
		if seg == "" {
			return false
		}
	}
	switch len(parts) {
	case 1:
		return true
	case 2:
		return parts[1] == "cover"
	case 3:
		return parts[1] == "stream" || parts[1] == "peaks"
	}
	return false
}

// logPath is the request path for a log line, with an invite or share-link token replaced by an
// ellipsis: the token is the secret, so it must never reach a log.
func logPath(r *http.Request) string {
	if rest, ok := strings.CutPrefix(r.URL.Path, linkPrefix); ok {
		_, tail, more := strings.Cut(rest, "/")
		if more {
			return linkPrefix + "…/" + tail
		}
		return linkPrefix + "…"
	}
	rest, ok := strings.CutPrefix(r.URL.Path, invitePrefix)
	if !ok {
		return r.URL.Path
	}
	_, tail, more := strings.Cut(rest, "/")
	if more {
		return invitePrefix + "…/" + tail
	}
	return invitePrefix + "…"
}
