package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// shellCookie marks a browser window that is the desktop (or a phone) shell around Tendshelf, not a
// browser tab (decision 0020). It changes how pages look and which scripts load; it grants nothing,
// so it is not a credential and carries no secret.
const shellCookie = "ts_shell"

// shellLife is how long the mark lasts; the shell sets it again whenever it opens the server.
const shellLife = 365 * 24 * time.Hour

// shellFrom returns the shell the request comes from (desktop, ios or android), or "" for a browser
// and for any cookie value that is not one of them.
func shellFrom(r *http.Request) string {
	c, err := r.Cookie(shellCookie)
	if err != nil {
		return ""
	}
	switch c.Value {
	case "desktop", "ios", "android":
		return c.Value
	}
	return ""
}

// shellLink handles GET /?shell=<name>&next=<path>: the one link a shell opens to mark its window.
// A valid name sets the cookie and "browser" removes it; either way the answer is a 303 to next
// (checked with auth.SafeNext). Anything else falls through to the normal handler untouched.
func (s *server) shellLink(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || r.URL.Path != "/" || !q.Has("shell") {
			next.ServeHTTP(w, r)
			return
		}
		// A page on another site can send the browser here (Sec-Fetch-Site: cross-site or same-site);
		// only a request opened by the person ("none") or one without the header (the shell's own
		// navigation) may change the mark.
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" {
			next.ServeHTTP(w, r)
			return
		}
		value := q.Get("shell")
		switch value {
		case "desktop", "ios", "android", "browser":
		default:
			next.ServeHTTP(w, r)
			return
		}
		cookie := &http.Cookie{
			Name:     shellCookie,
			Value:    value,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.proxies.Secure(r),
			SameSite: http.SameSiteLaxMode,
			Expires:  time.Now().Add(shellLife),
			MaxAge:   int(shellLife / time.Second),
		}
		if value == "browser" {
			cookie.Value, cookie.Expires, cookie.MaxAge = "", time.Unix(0, 0), -1
		}
		http.SetCookie(w, cookie)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, shellNext(q.Get("next")), http.StatusSeeOther)
	})
}

// shellNext is auth.SafeNext plus one exception: the shell opens /setup?code=... for a first run,
// and SafeNext sends the sign-in pages to "/". The setup path is checked by the same function, with
// its name swapped for a harmless one, so every other rule still applies.
func shellNext(raw string) string {
	if rest, ok := strings.CutPrefix(raw, "/setup"); ok && (rest == "" || rest[0] == '?') {
		if probe := "/p" + rest; auth.SafeNext(probe) == probe {
			return raw
		}
		return "/"
	}
	return auth.SafeNext(raw)
}
