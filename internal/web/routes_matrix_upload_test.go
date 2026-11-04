package web

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/upload"
)

// uploadSeed gives the sweep app the upload routes (decision 0015). The sweep never runs jobs, so
// the service only has to exist: the rows ask for ids that do not exist, or for a book Tendshelf
// did not add, and the library of the sweep takes no uploads.
func uploadSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	a := e.a
	svc := upload.New(upload.Config{DataDir: a.dir}, a.st, a.lib, media.Tools{}, a.runner, nil)
	if err := svc.Register(a.runner); err != nil {
		t.Fatal(err)
	}
	a.srv.uploads = svc
}

// uploadSweepRows are the route matrix rows of the upload routes. Owner and admin may add books;
// a member and a guest never reach any of these routes, even with a library grant. A visitor who is
// not signed in gets 401 from the JSON routes and the usual redirect from the two pages' routes.
func uploadSweepRows() []sweepRow {
	staff := func(ok int) [4]int { return sweepStaff(ok, http.StatusNotFound) }
	json := func(r sweepRow) sweepRow {
		r.Anon = http.StatusUnauthorized
		return r
	}
	create := json(sweepPost("POST /libraries/{id}/uploads",
		func(e *sweepEnv, _ int) string { return "/libraries/" + e.libID + "/uploads" }, sweepFormOf(url.Values{}),
		staff(http.StatusBadRequest), ""))
	get := json(sweepGet("GET /uploads/{id}", sweepStatic("/uploads/upl_none"), sweepSame(http.StatusNotFound)))
	chunk := json(sweepRow{Pattern: "PUT /uploads/{id}/files/{fileID}", Method: http.MethodPut,
		Path: sweepStatic("/uploads/upl_none/files/upf_none?offset=0"), Form: sweepFormOf(url.Values{}),
		Want: staff(http.StatusUnsupportedMediaType)})
	finish := json(sweepPost("POST /uploads/{id}/finish", sweepStatic("/uploads/upl_none/finish"), sweepFormOf(url.Values{}),
		sweepSame(http.StatusNotFound), ""))
	cancel := json(sweepPost("POST /uploads/{id}/cancel", sweepStatic("/uploads/upl_none/cancel"), sweepFormOf(url.Values{}),
		sweepSame(http.StatusNotFound), ""))
	setting := sweepPost("POST /admin/libraries/{id}/uploads",
		func(e *sweepEnv, _ int) string { return "/admin/libraries/" + e.libID + "/uploads" }, sweepFormOf(url.Values{"folder_id": {""}}),
		sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound), "/admin/libraries#uploads-h")
	remove := sweepPost("POST /books/{id}/remove", sweepBookPath("/remove"), sweepFormOf(url.Values{}),
		staff(http.StatusConflict), "")
	return []sweepRow{create, get, chunk, finish, cancel, setting, remove}
}
