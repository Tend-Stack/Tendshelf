package web

import (
	"net/http"
	"net/url"
)

// shareSweepRows are the route matrix rows of the Share & invite page (P3-4a). Owner and admin may
// use it; a member, a guest and the visitor never see it (404 or the sign-in redirect). An admin
// who forges role=admin is refused with the same 404.
func shareSweepRows() []sweepRow {
	form := func(role string) func(*sweepEnv, int) url.Values {
		return sweepFormOf(url.Values{"name": {"Rosa"}, "role": {role}, "scope": {"all"}, "read": {"1"}, "expires": {"7d"}})
	}
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	post := sweepPost("POST /share", sweepStatic("/share"), form("guest"), staff, "")
	adminRole := sweepPost("POST /share", sweepStatic("/share"), form("admin"), [4]int{http.StatusOK, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}, "")
	adminRole.Label = "admin role"
	bookPost := sweepPost("POST /books/{id}/share", sweepBookPath("/share"), sweepFormOf(url.Values{
		"name": {"Rosa"}, "role": {"guest"}, "scope": {"book"}, "read": {"1"}, "expires": {"7d"}}), staff, "")
	return []sweepRow{
		sweepGet("GET /share", sweepStatic("/share"), staff),
		post, adminRole,
		sweepGet("GET /books/{id}/share", sweepBookPath("/share"), staff),
		bookPost,
	}
}
