package web

import (
	"net/http"
	"net/url"
)

// pairSweepRows are the route matrix rows of adding a phone by QR (decision 0028). The owner's routes
// answer every role alike, because each person adds a phone for themselves only and no route takes a
// person; the public routes answer every role and the visitor alike. The sweep sends no ts_shell mark
// and no pairing exists, so the owner's id routes are the 404 page and the phone's POSTs the 403 page.
func pairSweepRows() []sweepRow {
	unknown := "/pair/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	ghost := "/me/devices/add/pr_nothere"
	same := func(c int) [4]int { return sweepSame(c) }
	public := func(r sweepRow, anon int) sweepRow { r.Anon = anon; return r }
	return []sweepRow{
		sweepGet("GET /me/devices/add", sweepStatic("/me/devices/add"), same(http.StatusOK)),
		sweepPost("POST /me/devices/add", sweepStatic("/me/devices/add"), nil, same(http.StatusOK), ""),
		sweepGet("GET /me/devices/add/{id}", sweepStatic(ghost), same(http.StatusNotFound)),
		sweepPost("POST /me/devices/add/{id}/approve", sweepStatic(ghost+"/approve"), sweepFormOf(url.Values{"match": {"0000"}}), same(http.StatusNotFound), ""),
		sweepPost("POST /me/devices/add/{id}/cancel", sweepStatic(ghost+"/cancel"), nil, same(http.StatusSeeOther), "/me/devices"),
		public(sweepGet("GET /pair/{token}", sweepStatic(unknown), same(http.StatusNotFound)), http.StatusNotFound),
		public(sweepPost("POST /pair/{token}", sweepStatic(unknown), sweepFormOf(url.Values{"challenge": {a1Challenge}}), same(http.StatusForbidden), ""), http.StatusForbidden),
		public(sweepGet("GET /pair/wait", sweepStatic("/pair/wait"), same(http.StatusNotFound)), http.StatusNotFound),
		public(sweepPost("POST /pair/finish", sweepStatic("/pair/finish"), nil, same(http.StatusForbidden), ""), http.StatusForbidden),
	}
}
