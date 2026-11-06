package web

import (
	"context"
	"net/http"
	"net/url"
	"sync"
)

// lookupsSweepRows are the route matrix rows of automatic and bulk lookups (AL-4). Owner and admin
// manage libraries; a member and a guest get 404 on every one.
func lookupsSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	lib := func(suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return "/admin/libraries/" + e.libID + "/" + suffix }
	}
	auto := sweepPost("POST /admin/libraries/{id}/auto-lookup", lib("auto-lookup"), sweepFormOf(url.Values{"on": {"1"}}), see, "/admin/libraries")
	start := sweepPost("POST /admin/libraries/{id}/lookups", lib("lookups"), sweepFormOf(url.Values{}), see, "/admin/libraries")
	stop := sweepPost("POST /admin/libraries/{id}/lookups/stop", lib("lookups/stop"), sweepFormOf(url.Values{}), see, "/admin/libraries")
	list := sweepGet("GET /admin/libraries/{id}/lookups", lib("lookups"), staff)
	dismiss := sweepPost("POST /admin/libraries/{id}/lookups/{book}/dismiss", func(e *sweepEnv, _ int) string {
		return "/admin/libraries/" + e.libID + "/lookups/" + e.book.ID + "/dismiss"
	}, sweepFormOf(url.Values{}), see, "")
	dismiss.LocOf = func(e *sweepEnv) string { return "/admin/libraries/" + e.libID + "/lookups" }
	// The status of a bulk job needs one: the first row to ask queues it.
	var mu sync.Mutex
	queued := map[*sweepEnv]string{}
	poll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string {
		mu.Lock()
		defer mu.Unlock()
		if queued[e] == "" {
			id, err := e.a.srv.details.StartBulk(context.Background(), e.libID, "", false, "")
			if err != nil {
				e.t.Fatal(err)
			}
			queued[e] = id
		}
		return "/hx/jobs/" + queued[e]
	}, staff)
	poll.Label = "bulk lookup"
	return []sweepRow{auto, start, stop, list, dismiss, poll}
}
