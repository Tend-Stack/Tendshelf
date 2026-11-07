package web

import (
	"net/http"
	"net/url"
)

// usersSweepRows are the route matrix rows of the Users area (P3-2a). They act on e.extra, a
// member holding one library grant; "admin target" and "owner target" rows aim at the admin and the
// owner, whom an admin may never touch and nobody touches in themselves.
func usersSweepRows() []sweepRow {
	extra := func(suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/admin/users/" + e.extra + suffix }
	}
	grant := func(suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/admin/users/" + e.extra + "/grants/" + e.extraGrant + suffix }
	}
	target := func(who int, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/admin/users/" + e.uid[who] + suffix }
	}
	back := func(who int) func(*sweepEnv) string {
		return func(e *sweepEnv) string {
			if who < 0 {
				return "/admin/users/" + e.extra
			}
			return "/admin/users/" + e.uid[who]
		}
	}
	post := func(pattern, label string, path func(*sweepEnv, int) string, form url.Values, want [4]int, loc func(*sweepEnv) string) sweepRow {
		r := sweepPost(pattern, path, sweepFormOf(form), want, "")
		r.Label, r.LocOf = label, loc
		return r
	}
	get := func(pattern, label string, path func(*sweepEnv, int) string, want [4]int) sweepRow {
		r := sweepGet(pattern, path, want)
		r.Label = label
		return r
	}
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	staffPost := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	onlyOwner := [4]int{http.StatusSeeOther, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}
	ownerOnlyPage := [4]int{http.StatusOK, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}
	none := sweepSame(http.StatusNotFound)
	return []sweepRow{
		sweepGet("GET /admin/users", sweepStatic("/admin/users"), staff),
		sweepGet("GET /hx/admin/users", sweepStatic("/hx/admin/users"), staff),
		get("GET /admin/users/{id}", "", extra(""), staff),
		get("GET /admin/users/{id}", "admin target", target(sweepAdmin, ""), ownerOnlyPage),
		get("GET /admin/users/{id}", "owner target", target(sweepOwner, ""), none),
		get("GET /admin/users/{id}/disable", "", extra("/disable"), staff),
		get("GET /admin/users/{id}/disable", "owner target", target(sweepOwner, "/disable"), none),
		get("GET /admin/users/{id}/stop", "", extra("/stop"), staff),
		// The extra member added no books, so the confirm goes back to the page (303) for staff.
		get("GET /admin/users/{id}/books/remove", "", extra("/books/remove"), staffPost),
		get("GET /admin/users/{id}/books/remove", "admin target", target(sweepAdmin, "/books/remove"), onlyOwner),
		get("GET /admin/users/{id}/books/remove", "owner target", target(sweepOwner, "/books/remove"), none),

		post("POST /admin/users/{id}/role", "", extra("/role"), url.Values{"role": {"guest"}}, staffPost, back(-1)),
		post("POST /admin/users/{id}/role", "to admin", extra("/role"), url.Values{"role": {"admin"}}, onlyOwner, back(-1)),
		post("POST /admin/users/{id}/role", "admin target", target(sweepAdmin, "/role"), url.Values{"role": {"member"}}, onlyOwner, back(sweepAdmin)),
		post("POST /admin/users/{id}/role", "owner target", target(sweepOwner, "/role"), url.Values{"role": {"member"}}, none, nil),
		post("POST /admin/users/{id}/disable", "", extra("/disable"), nil, staffPost, back(-1)),
		post("POST /admin/users/{id}/disable", "owner target", target(sweepOwner, "/disable"), nil, none, nil),
		post("POST /admin/users/{id}/enable", "", extra("/enable"), nil, staffPost, back(-1)),
		post("POST /admin/users/{id}/stop", "", extra("/stop"), nil, staffPost, back(-1)),
		post("POST /admin/users/{id}/books/remove", "", extra("/books/remove"), nil, staffPost, back(-1)),
		post("POST /admin/users/{id}/books/remove", "admin target", target(sweepAdmin, "/books/remove"), nil, onlyOwner, back(sweepAdmin)),
		post("POST /admin/users/{id}/books/remove", "owner target", target(sweepOwner, "/books/remove"), nil, none, nil),
		post("POST /admin/users/{id}/grants", "", extra("/grants"), url.Values{"scope": {"all"}, "download": {"1"}}, staffPost, back(-1)),
		post("POST /admin/users/{id}/grants/{gid}", "", grant(""), url.Values{"download": {"1"}}, staffPost, back(-1)),
		post("POST /admin/users/{id}/grants/{gid}/renew", "", grant("/renew"), nil, staffPost, back(-1)),
		post("POST /admin/users/{id}/grants/{gid}/remove", "", grant("/remove"), nil, staffPost, back(-1)),
	}
}
