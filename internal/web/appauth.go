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
	return s.keyGuard(kind, basicKeyCreds, keyReplies{refuse: s.keyRefuseBasic, say: keyText}, next)
}

// keyCreds reads the user name and the stored-form hash of the key from a request. present says
// credentials were sent at all; valid says they have a usable shape.
type keyCreds func(r *http.Request) (name string, hash [32]byte, present, valid bool)

// keyReplies are the answers a surface gives: 401 (counted says whether it was a wrong attempt) and any other
// refusal in its own words and format.
type keyReplies struct {
	refuse func(w http.ResponseWriter, ip string, counted bool)
	say    func(w http.ResponseWriter, status int, msg string)
}

func basicKeyCreds(kind auth.DeviceKind) keyCreds {
	return func(r *http.Request) (string, [32]byte, bool, bool) {
		name, secret, present := r.BasicAuth()
		if !present || !token.IsTypable(secret) {
			return "", [32]byte{}, present, false
		}
		return name, auth.KeyHash(kind, secret), true, true
	}
}

// keyGuard is the shared check behind every app-key surface: the per-address failure limit, the lookup of a live
// key of this kind, the user name match and the per-device limit. creds and replies give the surface its protocol.
func (s *server) keyGuard(kind auth.DeviceKind, creds func(auth.DeviceKind) keyCreds, replies keyReplies, next http.Handler) http.Handler {
	read := creds(kind)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := s.proxies.ClientIP(r).String()
		if ok, wait := s.keyLimiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			replies.say(w, http.StatusTooManyRequests, "Too many wrong keys. Wait a little and try again.")
			return
		}
		name, hash, present, valid := read(r)
		if !valid {
			replies.refuse(w, ip, present)
			return
		}
		dev, u, err := s.devices.LookupKey(r.Context(), kind, hash)
		switch {
		case errors.Is(err, auth.ErrNoDevice):
			replies.refuse(w, ip, true)
			return
		case err != nil:
			slog.Error("app key lookup", "err", err) // never the key
			replies.say(w, http.StatusServiceUnavailable, "Something went wrong on our side. Try again in a moment.")
			return
		}
		// The user name must match too, so a key seen in one log line is not enough on its own.
		if !strings.EqualFold(strings.TrimSpace(name), u.Username) {
			replies.refuse(w, ip, true)
			return
		}
		if ok, wait := s.apiLimiter.Allow(dev.ID); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			replies.say(w, http.StatusTooManyRequests, "Too many requests from this app. Wait a little and try again.")
			return
		}
		s.apiLimiter.Fail(dev.ID)
		next.ServeHTTP(w, r.WithContext(auth.WithDevice(r.Context(), dev, u)))
	})
}

// keyRefuseBasic answers 401 with the Basic challenge; counted says whether this was a wrong attempt (credentials
// were sent) rather than a first visit that has not been offered the challenge yet.
func (s *server) keyRefuseBasic(w http.ResponseWriter, ip string, counted bool) {
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
