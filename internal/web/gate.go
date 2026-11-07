package web

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// gate is default deny: every path except /setup, /login and the invite, sign-in and share links (the static files and /healthz never get
// here) needs a signed-in, non-disabled person. The auth middleware already dropped sessions of
// disabled accounts, so "signed in" is just "has a user in the context". With no owner yet,
// everyone is sent to /setup; otherwise anonymous visitors go to /login with a way back.
func (s *server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/setup" || r.URL.Path == "/login" || isInvitePath(r.URL.Path) || isPairPath(r.URL.Path) || isSignInPath(r.URL.Path) || isLinkPath(r.URL.Path) {
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

// isAPIPath reports whether p is under /api/, the JSON area, or is one of the JSON routes of the
// upload client (decision 0015): it gets JSON errors and a 401 instead of a redirect to /login.
func isAPIPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/uploads/") ||
		(strings.HasPrefix(p, "/libraries/") && strings.HasSuffix(p, "/uploads"))
}

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

// answerPage ends an unsafe request from a signed-in page in the normal layout. A plain form is
// redirected to loc (303). An htmx request must not be (HX-Redirect loads the whole window and
// stops the audio that plays in the layout), so it gets 200 with the page GET loc would render,
// which the boosted form swaps into #main, and HX-Push-Url moves the address bar to loc.
func (s *server) answerPage(w http.ResponseWriter, r *http.Request, loc string) {
	if r.Header.Get("HX-Request") != "true" {
		s.redirect(w, r, loc)
		return
	}
	u, err := url.Parse(loc)
	if err != nil || !strings.HasPrefix(u.Path, "/") {
		s.serverError(w, r, "answer page", err)
		return
	}
	r2 := r.Clone(r.Context())
	r2.Method, r2.URL, r2.Body, r2.ContentLength = http.MethodGet, &url.URL{Path: u.Path, RawQuery: u.RawQuery}, http.NoBody, 0
	r2.RequestURI = r2.URL.RequestURI()
	r2.Header.Del("Content-Type")
	w.Header().Set("HX-Push-Url", loc)
	s.routes.ServeHTTP(w, r2)
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

// pairPrefix starts the public pairing paths: /pair/{token}, /pair/wait and /pair/finish. Each
// checks its own proof (the token, or the claim cookie) and changes nothing on a GET.
const pairPrefix = "/pair/"

// isPairPath reports whether p is /pair/ plus one non-empty segment.
func isPairPath(p string) bool {
	rest, ok := strings.CutPrefix(p, pairPrefix)
	return ok && rest != "" && !strings.Contains(rest, "/")
}

// signInPrefix starts the public sign-in link path (decision 0030): /signin/{token}. A GET only checks
// the link; the POST spends it. Every failure gives the same page.
const signInPrefix = "/signin/"

// isSignInPath reports whether p is /signin/ plus one non-empty segment.
func isSignInPath(p string) bool {
	rest, ok := strings.CutPrefix(p, signInPrefix)
	return ok && rest != "" && !strings.Contains(rest, "/")
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
	if isPairPath(r.URL.Path) {
		return pairPrefix + "…" // the token is the secret
	}
	if isSignInPath(r.URL.Path) {
		return signInPrefix + "…" // the token is the secret
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
