package web

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

const themeCookie = "ts_theme"

// themeFrom returns the theme in the ts_theme cookie (dark, light, sepia or contrast) or "" when
// the cookie is absent or invalid. With "", the page sets no data-theme and the CSS follows the
// system. It is the theme of the sign-in pages and the fallback for a person who has not chosen
// one; themeFor decides for a signed-in person.
func themeFrom(r *http.Request) string {
	ck, err := r.Cookie(themeCookie)
	if err != nil {
		return ""
	}
	switch ck.Value {
	case "dark", "light", "sepia", "contrast":
		return ck.Value
	}
	return ""
}

// themeFor is the theme a page renders with. A signed-in person's ui.theme setting wins ("system"
// means no data-theme, so the CSS follows the device even if a cookie is left over); without one
// the ts_theme cookie applies; without that, the system. A failed read is logged and falls back to
// the cookie, so a database hiccup never breaks a page.
func (s *server) themeFor(r *http.Request) string {
	if u, ok := auth.UserFrom(r.Context()); ok {
		th, err := s.users.Theme(r.Context(), u.ID)
		switch {
		case err != nil:
			slog.Error("read theme", "err", err)
		case th == "system":
			return ""
		case th != "":
			return th
		}
	}
	return themeFrom(r)
}

// setThemeCookie mirrors the saved theme into the cookie, so the sign-in pages match the person's
// choice on this browser. "system" deletes it.
func (s *server) setThemeCookie(w http.ResponseWriter, r *http.Request, theme string) {
	ck := &http.Cookie{
		Name: themeCookie, Value: theme, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: s.proxies.Secure(r), MaxAge: int((365 * 24 * time.Hour).Seconds()),
	}
	if theme == "system" || theme == "" {
		ck.Value, ck.MaxAge = "", -1
	}
	http.SetCookie(w, ck)
}
