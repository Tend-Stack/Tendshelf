package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
)

// WithSession returns ctx carrying the signed-in session and user.
func WithSession(ctx context.Context, s Session, u users.User) context.Context {
	ctx = context.WithValue(ctx, sessionKey, s)
	return context.WithValue(ctx, userKey, u)
}

// UserFrom returns the signed-in user, if any.
func UserFrom(ctx context.Context) (users.User, bool) {
	u, ok := ctx.Value(userKey).(users.User)
	return u, ok
}

// SessionFrom returns the current session, if any.
func SessionFrom(ctx context.Context) (Session, bool) {
	s, ok := ctx.Value(sessionKey).(Session)
	return s, ok
}

// SetCookie sends the session cookie. It is HttpOnly and SameSite=Lax;
// Secure is set when the request reached the server over HTTPS.
func SetCookie(w http.ResponseWriter, tok string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: tok, Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: int(SessionTTL.Seconds()),
	})
}

// ClearCookie tells the browser to forget the session cookie.
func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// Middleware puts the signed-in session and user into the request context. A
// refreshed session gets its cookie re-sent; a cookie that no longer names a
// valid session is cleared. It never rejects a request: the gate decides what
// an anonymous visitor may see.
func Middleware(s *Sessions, p Proxies) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(CookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			sess, u, refreshed, err := s.Lookup(r.Context(), c.Value)
			switch {
			case errors.Is(err, ErrNoSession):
				ClearCookie(w, p.Secure(r))
				next.ServeHTTP(w, r)
				return
			case err != nil:
				slog.Error("session lookup failed", "err", err)
				http.Error(w, "Something went wrong on our side. Try again in a moment.", http.StatusServiceUnavailable)
				return
			}
			if refreshed {
				SetCookie(w, c.Value, p.Secure(r))
			}
			next.ServeHTTP(w, r.WithContext(WithSession(r.Context(), sess, u)))
		})
	}
}
