package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
)

// writeSeed gives the sweep app the write routes (M4-9): a Writes service and a waiting write job
// of the seeded book. The sweep never runs jobs, so the kind is a stub that only lets one be queued.
// The seeded audio files are recent wav files, so a start is refused and nothing is ever written.
func writeSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	a := e.a
	err := a.runner.Register(jobs.Kind{Name: mediajobs.KindWriteDetails, Concurrency: 1, MaxAttempts: 1,
		Handler: func(context.Context, *jobs.Task) (any, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	a.srv.writes = mediajobs.NewWrites(a.lib, media.Tools{}, a.dir, a.runner, a.runner, nil)
	e.writeJob, err = a.runner.Enqueue(context.Background(), mediajobs.KindWriteDetails,
		mediajobs.WritePayload{BookID: e.book.ID}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
}

// writeSweepRows are the route matrix rows of the write routes (M4-9). Owner and admin may edit
// every book; a member and a guest never reach a write route, even with a library grant.
func writeSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	sheet := sweepGet("GET /books/{id}/{tab}", sweepBookPath("/details?sheet=write"), staff)
	sheet.Label = "write sheet"
	sheetJob := sweepGet("GET /books/{id}/{tab}", func(e *sweepEnv, _ int) string {
		return "/books/" + e.book.ID + "/details?sheet=write&job=" + e.writeJob
	}, staff)
	sheetJob.Label = "write sheet with a job"
	poll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string { return "/hx/jobs/" + e.writeJob }, staff)
	poll.Label = "write details"
	// The seeded files are recent, so staff get the sheet back with the refusal.
	start := sweepPost("POST /books/{id}/write", sweepBookPath("/write"), sweepFormOf(url.Values{}),
		sweepStaff(http.StatusUnprocessableEntity, http.StatusNotFound), "")
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	confirm := sweepPost("POST /books/{id}/write/confirm", sweepBookPath("/write/confirm"), sweepFormOf(url.Values{}), see, "")
	confirm.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/details?written=kept" }
	undo := sweepPost("POST /books/{id}/write/undo", sweepBookPath("/write/undo"), sweepFormOf(url.Values{}), see, "")
	undo.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/details?written=undone" }
	return []sweepRow{sheet, sheetJob, poll, start, confirm, undo}
}
