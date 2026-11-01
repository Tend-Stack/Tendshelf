package auth

import (
	"net/url"
	"strings"
)

// SafeNext returns raw when it is a same-origin, path-only redirect target
// that is not itself one of the sign-in pages, and "/" otherwise. It guards
// every redirect that comes from a query parameter or a form field.
func SafeNext(raw string) string {
	if raw == "" || len(raw) > 2048 || raw[0] != '/' || strings.HasPrefix(raw, "//") || strings.Contains(raw, `\`) {
		return "/"
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return "/"
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	// The decoded path must be as harmless as the raw one.
	if strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, `\`) || strings.IndexFunc(u.Path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "/"
	}
	switch p := u.Path; {
	case p == "/login", p == "/logout", p == "/setup", strings.HasPrefix(p, "/setup/"):
		return "/"
	}
	return raw
}
