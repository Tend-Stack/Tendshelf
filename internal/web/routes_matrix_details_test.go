package web

import (
	"net/http"
	"net/url"
)

// detailsSweepRows are the route matrix rows of "Look up details online" (M4-6). Owner and admin
// may use every book route; a member and a guest never see one, even with a library grant, because
// editing details is not a grantable action. The online switch is the owner's alone.
func detailsSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	book := func(suffix string) func(*sweepEnv, int) string { return sweepBookPath(suffix) }
	lookupSheet := sweepGet("GET /books/{id}/{tab}", book("/details?sheet=lookup"), staff)
	lookupSheet.Label = "lookup sheet"
	jobSheet := func(c string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string {
			return "/books/" + e.book.ID + "/details?sheet=compare&job=" + e.lookupJob + c
		}
	}
	compareList := sweepGet("GET /books/{id}/{tab}", jobSheet(""), staff)
	compareList.Label = "compare list"
	compareTable := sweepGet("GET /books/{id}/{tab}", jobSheet("&c=0"), staff)
	compareTable.Label = "compare table"
	poll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string {
		return "/hx/jobs/" + e.lookupJob + "?book=" + e.book.ID
	}, staff)
	poll.Label = "lookup"
	cover := sweepGet("GET /books/{id}/match/{job}/cover/{n}", func(e *sweepEnv, _ int) string {
		return "/books/" + e.book.ID + "/match/" + e.lookupJob + "/cover/0"
	}, staff)
	match := sweepPost("POST /books/{id}/match", book("/match"), sweepFormOf(url.Values{"title": {"Sweepword Lantern"}}), see, "")
	apply := sweepPost("POST /books/{id}/match/apply", book("/match/apply"), func(e *sweepEnv, _ int) url.Values {
		return url.Values{"job": {e.lookupJob}, "c": {"0"}, "use": {"narrators"}}
	}, see, "")
	apply.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/details?applied=1" }
	reset := sweepPost("POST /books/{id}/details/reset", book("/details/reset"), sweepFormOf(url.Values{}), see, "")
	reset.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/details" }
	switchRow := sweepPost("POST /admin/online", sweepStatic("/admin/online"), sweepFormOf(url.Values{"on": {"0"}}),
		sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound), "/admin/libraries")
	return []sweepRow{lookupSheet, compareList, compareTable, poll, cover, match, apply, reset, switchRow}
}
