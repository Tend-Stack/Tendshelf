package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// apiPrefix is the only area where a device token is accepted.
const apiPrefix = "/api/v1/"

// bearerToken returns the token of an "Authorization: Bearer <token>" header (scheme in any case)
// and whether the header uses the Bearer scheme at all. An empty or malformed token is still
// "present", so it is refused rather than quietly treated as anonymous.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	scheme, rest, _ := strings.Cut(h, " ")
	if !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// refuseMixed answers 400 when a request carries both a bearer token and a browser session cookie,
// before any lookup: two credentials make it unclear who is asking, and a cookie-sending browser
// must not be able to ride on a token (or the other way round).
func (s *server) refuseMixed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, present := bearerToken(r); present {
			if _, err := r.Cookie(auth.CookieName); err == nil {
				api.Error(w, http.StatusBadRequest, api.CodeInvalid, "Send either a device token or a browser session, not both.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// bearer authenticates a device token. Without one the request passes on untouched. A token is
// accepted only under /api/v1/ and is looked up in the database on every request, so revoking a
// device, disabling a person or changing a password takes effect on the next call.
func (s *server) bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, present := bearerToken(r)
		if !present {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, apiPrefix) {
			api.Unauthorized(w, "Device tokens work only for /api/v1.")
			return
		}
		dev, u, err := s.devices.Lookup(r.Context(), tok)
		switch {
		case errors.Is(err, auth.ErrNoDevice):
			api.Unauthorized(w, "This device is signed out. Sign in again.")
			return
		case err != nil:
			slog.Error("device lookup", "err", err) // never the token
			api.Error(w, http.StatusServiceUnavailable, api.CodeUnavailable, "Something went wrong on our side. Try again in a moment.")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithDevice(r.Context(), dev, u)))
	})
}
