package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/align"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// alignSeed gives the sweep app read-along (M5 R3 P5): the alignment service over a word-timing runtime that
// is there, the word-timing page's runtime with no Python, and paired ebooks with an edition for each route.
// A person gets a file of their own for each verb, because cancel, resume and discard use theirs up. The
// start rows get a file with no alignment; cancel gets a queued one; resume and discard get held ones.
func alignSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	a := e.a
	ctx := context.Background()
	if _, err := a.st.Writer().ExecContext(ctx, `INSERT INTO runtime_components(id, kind, name, language, revision, model_sha256, files_json, python_path, worker_sha256, licence_spdx, licence_url, source_url, checked_at)
VALUES ('rc_talign', 'emissions-model', 'w2v', 'en', 'r', ?, '[]', '/py', ?, 'MIT', 'u', 'u', 1)`, fmt.Sprintf("%064x", 1), fmt.Sprintf("%064x", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().ExecContext(ctx, `UPDATE audio_files SET duration_ms = 3600000`); err != nil {
		t.Fatal(err)
	}
	svc := align.NewService(align.Deps{Store: a.st, Library: a.lib, Ebooks: a.srv.ebooks.Store(), Runner: a.runner,
		Runtimes: &talignRT{ok: true}, DataDir: a.dir})
	if err := svc.Register(a.runner); err != nil {
		t.Fatal(err)
	}
	a.srv.align = svc
	e.alignFile = map[string]string{}
	n := 0
	for _, verb := range []string{"start", "cancel", "resume", "discard"} {
		for role := 0; role < 2; role++ {
			n++
			id := placeEbook(t, a, e.book, e.uid[sweepOwner], fmt.Sprintf("align-%s%d.epub", verb, role), "ready", "")
			sha := fmt.Sprintf("%064x", 6000+n)
			if _, err := a.st.Writer().ExecContext(ctx, `UPDATE ebook_files SET sha256 = ?, quick_hash = ? WHERE id = ?`, sha, sha, id); err != nil {
				t.Fatal(err)
			}
			edID := fmt.Sprintf("ed_align%d", n)
			if _, err := a.st.Writer().ExecContext(ctx, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, language, chapter_count, word_count, char_count, created_at)
VALUES (?, ?, ?, ?, ?, 5, 'epub', 'en', 1, 10, 50, 1)`, edID, id, sha, ebook.Recipe, fmt.Sprintf("%064x", 7000+n)); err != nil {
				t.Fatal(err)
			}
			e.alignFile[fmt.Sprintf("%s%d", verb, role)] = id
			if verb == "start" {
				continue
			}
			al, err := svc.Start(ctx, edID, e.book.ID, e.uid[sweepOwner])
			if err != nil {
				t.Fatalf("seed alignment %s%d: %v", verb, role, err)
			}
			if verb == "cancel" && role == 0 {
				e.alignJob = al.JobID
			}
			if verb != "cancel" {
				if _, err := a.st.Writer().ExecContext(ctx, `UPDATE alignments SET state = 'held', error_code = 'worker_failed' WHERE id = ?`, al.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := a.st.Writer().ExecContext(ctx, `UPDATE jobs SET state = 'held' WHERE id = ?`, al.JobID); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	runtimeSeed(t, e)
}

// runtimeSeed wires the word-timing runtime (no Python) into the sweep app and queues one check.
func runtimeSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	cfg := artConfig(t)
	comps := alignrt.NewComponents(e.a.st)
	if err := alignrt.Register(e.a.runner, cfg, comps); err != nil {
		t.Fatal(err)
	}
	e.a.srv.runtimes = &alignrt.Provider{Cfg: cfg, Comps: comps}
	id, err := e.a.runner.Enqueue(context.Background(), alignrt.KindCheck, alignrt.CheckPayload{Language: "en"}, jobs.EnqueueOptions{CreatedBy: e.uid[sweepOwner]})
	if err != nil {
		t.Fatal(err)
	}
	e.runtimeJob = id
}

// alignSweepRows are the route matrix rows of preparing read-along on the Text tab. Owner and admin may start
// and settle an alignment; a member and a guest get 404 (read-along is an editor's action, even with can_read).
func alignSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	post := func(verb, key string) sweepRow {
		r := sweepPost("POST /books/{id}/text/{fileID}/"+verb, func(e *sweepEnv, role int) string {
			return "/books/" + e.book.ID + "/text/" + e.alignFile[fmt.Sprintf("%s%d", key, min(role, 1))] + "/" + verb
		}, sweepFormOf(url.Values{}), sweepStaff(http.StatusSeeOther, http.StatusNotFound), "")
		r.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/text" }
		return r
	}
	poll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string { return "/hx/jobs/" + e.alignJob }, staff)
	poll.Label = "alignment"
	return []sweepRow{post("align", "start"), post("align/cancel", "cancel"), post("align/resume", "resume"), post("align/discard", "discard"), poll}
}

// runtimesSweepRows are the rows of the word-timing page: the server's settings, the owner's alone.
func runtimesSweepRows() []sweepRow {
	page := sweepOwnerOnly(http.StatusOK, http.StatusNotFound)
	check := sweepPost("POST /admin/runtimes/check", sweepStatic("/admin/runtimes/check"), sweepFormOf(url.Values{"language": {"en"}}),
		sweepOwnerOnly(http.StatusSeeOther, http.StatusNotFound), "")
	poll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string { return "/hx/jobs/" + e.runtimeJob }, page)
	poll.Label = "word timing check"
	return []sweepRow{
		sweepGet("GET /admin/runtimes", sweepStatic("/admin/runtimes"), page),
		check,
		sweepGet("GET /admin/runtimes/files/{name...}", sweepStatic("/admin/runtimes/files/requirements-torch.lock"), page),
		poll,
	}
}
