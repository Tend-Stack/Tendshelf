package web

import (
	"net/http"
	"testing"
)

// noReloadAllowed lists the unsafe routes (by registered pattern) that may answer an htmx request
// with a header that loads or refreshes the whole window. Every other unsafe route must answer in
// place: a full load stops the audio that plays in the layout outside #main.
var noReloadAllowed = map[string]string{
	"POST /logout":         "ends the session, so the player has nothing left to play",
	"POST /login":          "signs in: reached without a session, a new page load is correct",
	"POST /login/token":    "signs in with a token: reached without a session",
	"POST /invite/{token}": "public invite page: creates the account and its first session",
	"POST /setup/server":   "setup wizard before an account exists",
	"POST /setup/folder":   "setup wizard before an account exists",
	"POST /welcome":        "first sign-in after an invite: a new session starts",
	"POST /me/settings":    "a theme change lives on <html>, which only a reload can restyle",
}

// Every unsafe route of the route matrix, asked as htmx by a signed-in owner, answers without
// HX-Redirect, HX-Refresh or HX-Location, except the listed ones. A plain 303 is fine: htmx follows
// it inside the request and swaps the target page, so nothing reloads.
func TestUnsafeRoutesNeverReloadTheWindow(t *testing.T) {
	for _, row := range sweepRows() {
		if row.Method == http.MethodGet {
			continue
		}
		if _, ok := noReloadAllowed[row.Pattern]; ok {
			continue
		}
		t.Run(row.name(), func(t *testing.T) {
			t.Parallel()
			e := sweepSeed(t)
			form := map[string][]string{}
			if row.Form != nil {
				for k, v := range row.Form(e, sweepOwner) {
					form[k] = v
				}
			}
			form["_csrf"] = []string{e.a.tokenFor(t, e.ck[sweepOwner])}
			rec := e.a.do(row.Method, row.Path(e, sweepOwner), form, hx, e.ck[sweepOwner])
			for _, h := range []string{"HX-Redirect", "HX-Refresh", "HX-Location"} {
				if v := rec.Header().Get(h); v != "" {
					t.Errorf("%s answers htmx with %s %q (status %d)", row.name(), h, v, rec.Code)
				}
			}
		})
	}
}
