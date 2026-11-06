package web

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/token"
)

// App-key sign-in (decision 0025). A key opens exactly one surface, so each surface wraps its handler in
// keyAuth with its own kind. These routes sit on the outer mux beside /healthz: no cookie, no CSRF, no
// login redirect. Basic credentials are the person's user name and the key; the login password never works.

const (
	keyFailMax    = 10
	keyFailWindow = 15 * time.Minute
)

// opdsAuth guards the OPDS routes.
func (s *server) opdsAuth(next http.Handler) http.Handler { return s.keyAuth(auth.KindOPDS, next) }

// keyAuth reads Basic credentials, checks them against a live key of exactly this kind, and calls next with the
// key's owner as the current person. A failure answers 401 with a Basic challenge; too many failures from one
// client address answer 429. Every later request looks the key up again, so revoking it, disabling the person or
// letting it sit idle for 180 days stops it at once. A key that works also counts against the per-device rate limit.
func (s *server) keyAuth(kind auth.DeviceKind, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.proxies.ClientIP(r).String()
		if ok, wait := s.keyLimiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			keyText(w, http.StatusTooManyRequests, "Too many wrong keys. Wait a little and try again.")
			return
		}
		name, secret, present := r.BasicAuth()
		if !present || !token.IsTypable(secret) {
			s.keyRefuse(w, ip, present)
			return
		}
		dev, u, err := s.devices.LookupKey(r.Context(), kind, auth.KeyHash(kind, secret))
		switch {
		case errors.Is(err, auth.ErrNoDevice):
			s.keyRefuse(w, ip, true)
			return
		case err != nil:
			slog.Error("app key lookup", "err", err) // never the key
			keyText(w, http.StatusServiceUnavailable, "Something went wrong on our side. Try again in a moment.")
			return
		}
		// The user name must match too, so a key seen in one log line is not enough on its own.
		if !strings.EqualFold(strings.TrimSpace(name), u.Username) {
			s.keyRefuse(w, ip, true)
			return
		}
		if ok, wait := s.apiLimiter.Allow(dev.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			keyText(w, http.StatusTooManyRequests, "Too many requests from this app. Wait a little and try again.")
			return
		}
		s.apiLimiter.Fail(dev.ID)
		next.ServeHTTP(w, r.WithContext(auth.WithDevice(r.Context(), dev, u)))
	})
}

// keyRefuse answers 401 with the Basic challenge; counted says whether this was a wrong attempt (credentials
// were sent) rather than a first visit that has not been offered the challenge yet.
func (s *server) keyRefuse(w http.ResponseWriter, ip string, counted bool) {
	if counted {
		s.keyLimiter.Fail(ip)
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="Tendshelf", charset="UTF-8"`)
	keyText(w, http.StatusUnauthorized, "Sign in with your user name and an app key.")
}

func keyText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg + "\n"))
}
