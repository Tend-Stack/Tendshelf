package web

import (
	"net/http"
	"net/url"
)

// chapterSweepRows are the route matrix rows of the chapter editor (L3a). Owner and admin may edit a book's
// chapters; a member and a guest never reach a chapter route, even with a library grant.
func chapterSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	page := sweepGet("GET /books/{id}/chapters/edit", sweepBookPath("/chapters/edit"), staff)
	// Nothing was edited, so the reset confirmation does not exist for anyone.
	confirm := sweepGet("GET /books/{id}/chapters/reset", sweepBookPath("/chapters/reset"), sweepStaff(http.StatusNotFound, http.StatusNotFound))
	draft := sweepPost("POST /books/{id}/chapters", sweepBookPath("/chapters"), sweepFormOf(url.Values{"n": {"0"}, "op": {"del:0"}}), staff, "")
	reset := sweepPost("POST /books/{id}/chapters/reset", sweepBookPath("/chapters/reset"), sweepFormOf(url.Values{}),
		sweepStaff(http.StatusSeeOther, http.StatusNotFound), "")
	reset.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/chapters/edit" }
	return []sweepRow{page, confirm, draft, reset}
}
