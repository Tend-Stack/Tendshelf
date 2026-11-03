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
	th, _, _ := s.lookFor(r)
	return th
}

// lookFor is the theme (as themeFor) and the text size and sidebar state (rail or full) a page renders with. The text size is
// "large" or "", and only a signed-in person has one: sign-in, frame and link pages render the
// standard size. One read covers both.
func (s *server) lookFor(r *http.Request) (theme, textSize string, navRail bool) {
	if u, ok := auth.UserFrom(r.Context()); ok {
		l, err := s.users.Look(r.Context(), u.ID)
		if err != nil {
			slog.Error("read theme", "err", err)
		}
		if l.TextSize == "large" {
			textSize = "large"
		}
		navRail = l.Nav == "rail"
		switch {
		case err != nil:
		case l.Theme == "system":
			return "", textSize, navRail
		case l.Theme != "":
			return l.Theme, textSize, navRail
		}
		return themeFrom(r), textSize, navRail
	}
	return themeFrom(r), "", false
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
