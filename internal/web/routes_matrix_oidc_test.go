package web

import (
	"net/http"
	"net/url"
)

// oidcSweepRows are the route matrix rows of sign-in with a provider (decision 0037). The matrix runs with the
// provider off, so every sign-in route answers as if it did not exist (404) for every role, signed out or not;
// the three /login/oidc paths are public, so a signed-out visitor sees the 404 and not a redirect to /login. The
// settings are the owner's alone (the server manage action). The on-state is covered by oidc_routes_test.go.
func oidcSweepRows() []sweepRow {
	off := sweepSame(http.StatusNotFound)
	public := func(r sweepRow, anon int) sweepRow { r.Anon = anon; return r }
	ownerOnlyPage := sweepOwnerOnly(http.StatusOK, http.StatusNotFound)
	ownerOnlyPost := sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound)
	save := sweepPost("POST /admin/sso", sweepStatic("/admin/sso"), sweepFormOf(url.Values{
		"name": {"Authelia"}, "issuer": {"https://auth.example.org"}, "client_id": {"tendshelf"},
		"server_address": {"https://shelf.example.org"}}), ownerOnlyPost, "/admin/sso?saved=1")
	return []sweepRow{
		// A signed-in person is sent on to where they were going, as /login does; a signed-out one sees the 404.
		public(sweepGet("GET /login/oidc", sweepStatic("/login/oidc"), sweepSame(http.StatusSeeOther)), http.StatusNotFound),
		public(sweepGet("GET /login/oidc/callback", sweepStatic("/login/oidc/callback?code=x&state=y"), off), http.StatusNotFound),
		public(sweepGet("GET /login/oidc/origins", sweepStatic("/login/oidc/origins"), sweepSame(http.StatusOK)), http.StatusOK),
		sweepGet("GET /me/sso", sweepStatic("/me/sso"), off),
		sweepGet("GET /me/sso/link", sweepStatic("/me/sso/link"), off),
		sweepGet("GET /me/sso/confirm", sweepStatic("/me/sso/confirm?id=oil_nothing"), off),
		sweepPost("POST /me/sso/confirm", sweepStatic("/me/sso/confirm"), sweepFormOf(url.Values{"id": {"oil_nothing"}}), off, ""),
		sweepPost("POST /me/sso/unlink", sweepStatic("/me/sso/unlink"), nil, sweepSame(http.StatusSeeOther), "/me/sso"),
		sweepGet("GET /admin/sso", sweepStatic("/admin/sso"), ownerOnlyPage),
		save,
		sweepPost("POST /admin/sso/off", sweepStatic("/admin/sso/off"), nil, ownerOnlyPost, "/admin/sso?off=1"),
	}
}
