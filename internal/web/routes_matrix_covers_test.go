package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// coverPhotoSHA names the seeded author's photo.
const coverPhotoSHA = "ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12cd34ef56ab12"

// coverSeed gives the sweep app the cover picker (M4-8): a covers service, a waiting cover
// adoption and a waiting author lookup of the seeded book and author, and a photo for that author.
// The sweep never runs jobs, so the adoption kind is a stub that only lets a job be queued.
func coverSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	ctx := context.Background()
	a := e.a
	err := a.runner.Register(jobs.Kind{Name: mediajobs.KindCoverAdopt, Concurrency: 1, MaxAttempts: 2,
		Handler: func(context.Context, *jobs.Task) (any, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	a.srv.covers = mediajobs.NewCovers(a.lib, media.Tools{}, a.dir, a.runner, nil)
	if err := a.st.Reader().QueryRow(`SELECT cover_id FROM books WHERE id = ?`, e.book.ID).Scan(&e.coverID); err != nil {
		t.Fatal(err)
	}
	e.coverJob, err = a.runner.Enqueue(ctx, mediajobs.KindCoverAdopt,
		mediajobs.AdoptPayload{BookID: e.book.ID, Source: "upload", Staged: "0123456789abcdef.img"}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if e.authorJob, err = a.srv.details.StartAuthorLookup(ctx, e.author, ""); err != nil {
		t.Fatal(err)
	}
	coverSeedPhoto(t, a, e.author, coverPhotoSHA, ".jpg")
}

// coverSeedPhoto stores a person's photo thumbnail where the photo route looks and points the person at it.
func coverSeedPhoto(t *testing.T, a *testApp, personID, sha, ext string) {
	t.Helper()
	dir := metadata.PeopleDir(a.dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sha+"-320.jpg"), []byte("\xff\xd8\xff\xe0jpeg"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`UPDATE people SET image_id = ? WHERE id = ?`, sha+ext, personID); err != nil {
		t.Fatal(err)
	}
}

// coverSweepRows are the route matrix rows of the cover picker and the author details (M4-8). Owner
// and admin may edit every book; a member and a guest never reach a cover route, even with a library
// grant. The photo follows the page rule: whoever can open a book by that author sees it.
func coverSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	see := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	sheet := sweepGet("GET /books/{id}/{tab}", sweepBookPath("/details?sheet=cover"), staff)
	sheet.Label = "cover sheet"
	sheetJob := sweepGet("GET /books/{id}/{tab}", func(e *sweepEnv, _ int) string {
		return "/books/" + e.book.ID + "/details?sheet=cover&job=" + e.coverJob
	}, staff)
	sheetJob.Label = "cover sheet with a job"
	choice := sweepGet("GET /covers/{bookID}/choices/{coverID}", func(e *sweepEnv, _ int) string {
		return "/covers/" + e.book.ID + "/choices/" + e.coverID + "?w=160"
	}, staff)
	choose := sweepPost("POST /books/{id}/cover", sweepBookPath("/cover"), func(e *sweepEnv, _ int) url.Values {
		return url.Values{"cover_id": {e.coverID}}
	}, see, "")
	choose.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/details" }
	// The sweep sends urlencoded forms, so the upload row proves the refusals only: staff get "choose a picture".
	upload := sweepPost("POST /books/{id}/cover/upload", sweepBookPath("/cover/upload"), sweepFormOf(url.Values{}),
		sweepStaff(http.StatusUnprocessableEntity, http.StatusNotFound), "")
	coverPoll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string { return "/hx/jobs/" + e.coverJob }, staff)
	coverPoll.Label = "cover adoption"
	authorPoll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string { return "/hx/jobs/" + e.authorJob }, staff)
	authorPoll.Label = "author lookup"
	photo := sweepGet("GET /people/{id}/photo", func(e *sweepEnv, _ int) string { return "/people/" + e.author + "/photo" }, staff)
	photo.Granted = [4]int{0, 0, http.StatusOK, http.StatusOK}
	lookup := sweepPost("POST /people/{id}/lookup", func(e *sweepEnv, _ int) string { return "/people/" + e.author + "/lookup" },
		nil, see, "")
	// The Location holds the new job's id, so only the status is checked here; TestCoverPickAuthorLookup reads it.
	narratorLookup := sweepPost("POST /people/{id}/lookup", func(e *sweepEnv, _ int) string { return "/people/" + e.narrator + "/lookup" },
		nil, sweepSame(http.StatusNotFound), "")
	narratorLookup.Label = "a narrator who wrote nothing"
	authorsPage := sweepGet("GET /authors/{id}", func(e *sweepEnv, _ int) string {
		return "/authors/" + e.author + "?job=" + e.authorJob
	}, staff)
	authorsPage.Label = "with a lookup status"
	authorsPage.Granted = [4]int{0, 0, http.StatusOK, http.StatusOK}
	authorsPage.Body = func(t *testing.T, _ *sweepEnv, role int, body string) {
		t.Helper()
		if !strings.Contains(body, authorLookupButton) || !strings.Contains(body, authorWaiting) {
			t.Errorf("%s: the author page lacks the lookup button or its status", sweepRoleNames[role])
		}
	}
	return []sweepRow{sheet, sheetJob, choice, choose, upload, coverPoll, authorPoll, photo, lookup, narratorLookup, authorsPage}
}
