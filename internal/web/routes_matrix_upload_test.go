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
		sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound), "")
	setting.LocOf = func(e *sweepEnv) string { return "/admin/libraries#uploads-" + e.libID }
	remove := sweepPost("POST /books/{id}/remove", sweepBookPath("/remove"), sweepFormOf(url.Values{}),
		staff(http.StatusConflict), "")
	// The pages: the sweep's library takes no uploads and its books were not added by upload, so every
	// role gets 404 (the allowed answers are in routes_upload_pages_test.go).
	addPage := sweepGet("GET /libraries/{id}/add", func(e *sweepEnv, _ int) string { return "/libraries/" + e.libID + "/add" }, sweepSame(http.StatusNotFound))
	rowPage := sweepGet("GET /hx/uploads/{id}", sweepStatic("/hx/uploads/upl_none"), sweepSame(http.StatusNotFound))
	removePage := sweepGet("GET /books/{id}/remove", sweepBookPath("/remove"), staff(http.StatusOK))
	// Removing one part (decision 0015, "Remove one part"): the same two routes with a part, a file and an unknown
	// part. The sweep's book has no audio-and-text pair Tendshelf added, so staff get 409 or 404 and nobody else gets in.
	removeText := sweepPost("POST /books/{id}/remove", sweepBookPath("/remove"), sweepFormOf(url.Values{"part": {"text"}, "file": {"ebf_none"}}),
		staff(http.StatusConflict), "")
	removeText.Label = "one edition"
	removeAudio := sweepPost("POST /books/{id}/remove", sweepBookPath("/remove"), sweepFormOf(url.Values{"part": {"audio"}}),
		staff(http.StatusConflict), "")
	removeAudio.Label = "the audiobook only"
	removeOdd := sweepPost("POST /books/{id}/remove", sweepBookPath("/remove"), sweepFormOf(url.Values{"part": {"sideways"}}),
		sweepSame(http.StatusNotFound), "")
	removeOdd.Label = "unknown part"
	removeEdition := sweepGet("GET /books/{id}/remove", sweepBookPath("/remove?file=ebf_none"), sweepSame(http.StatusNotFound))
	removeEdition.Label = "confirm one edition"
	rows := []sweepRow{create, get, chunk, finish, cancel, setting, remove, addPage, rowPage, removePage,
		removeText, removeAudio, removeOdd, removeEdition}
	return append(rows, removeAnySweepRows()...)
}

// removeAnySweepRows are the rows of removing any book (decision 0045): owner and admin only, everyone else 404.
// The sweep's book is not a real file on disk, so a removal of it is refused, never started.
func removeAnySweepRows() []sweepRow {
	staff := func(ok int) [4]int { return sweepStaff(ok, http.StatusNotFound) }
	noMode := sweepPost("POST /books/{id}/remove-files", sweepBookPath("/remove-files"), sweepFormOf(url.Values{}),
		staff(http.StatusUnprocessableEntity), "")
	noMode.Label = "no mode"
	noBox := sweepPost("POST /books/{id}/remove-files", sweepBookPath("/remove-files"), sweepFormOf(url.Values{"mode": {"files"}, "plan": {"x"}}),
		staff(http.StatusUnprocessableEntity), "")
	noBox.Label = "box not ticked"
	notRemovable := sweepPost("POST /books/{id}/remove-files", sweepBookPath("/remove-files"),
		sweepFormOf(url.Values{"mode": {"keep_files"}, "plan": {"x"}}), staff(http.StatusConflict), "")
	notRemovable.Label = "a book with no file on disk"
	page := sweepGet("GET /removals/{rid}", sweepStatic("/removals/rmv_none"), sweepSame(http.StatusNotFound))
	frag := sweepGet("GET /hx/removals/{rid}", sweepStatic("/hx/removals/rmv_none"), sweepSame(http.StatusNotFound))
	restore := sweepPost("POST /removals/{rid}/restore", sweepStatic("/removals/rmv_none/restore"), sweepFormOf(url.Values{}),
		sweepSame(http.StatusNotFound), "")
	purge := sweepPost("POST /removals/{rid}/purge", sweepStatic("/removals/rmv_none/purge"), sweepFormOf(url.Values{"understand": {"1"}}),
		sweepSame(http.StatusNotFound), "")
	list := sweepGet("GET /admin/libraries/{id}/removed", func(e *sweepEnv, _ int) string { return "/admin/libraries/" + e.libID + "/removed" },
		staff(http.StatusOK))
	return []sweepRow{noMode, noBox, notRemovable, page, frag, restore, purge, list}
}
