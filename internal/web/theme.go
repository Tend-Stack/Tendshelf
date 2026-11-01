package web

import "net/http"

const themeCookie = "ts_theme"

// themeFrom returns the stored theme (dark, light, sepia or contrast) or "" when the cookie is
// absent or invalid. With "", the page sets no data-theme and the CSS follows the system
// (dark by default). P1b moves this to user_settings (ui.theme).
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
