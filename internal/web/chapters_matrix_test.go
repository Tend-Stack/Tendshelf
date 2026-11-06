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
	// L3b: owner and admin start a lookup (a second press reuses the waiting job); the redirect names it.
	lookup := sweepPost("POST /books/{id}/chapters/lookup", sweepBookPath("/chapters/lookup"), sweepFormOf(url.Values{"asin": {"B0TESTBOOK"}}),
		sweepStaff(http.StatusSeeOther, http.StatusNotFound), "")
	lookup.LocOf = func(e *sweepEnv) string {
		var id string
		_ = e.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = 'chapter_lookup' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&id)
		return "/books/" + e.book.ID + "/chapters/edit?lookup=" + id
	}
	return []sweepRow{page, confirm, draft, reset, lookup}
}
