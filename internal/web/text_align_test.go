package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/align"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// talignRT is a word-timing runtime that is there or not. Its worker is never launched: the tests do not run
// the align.book job, they look at the rows and the pages around it.
type talignRT struct{ ok bool }

func (r *talignRT) Usable(context.Context, string) (alignrt.Component, bool, error) {
	return alignrt.Component{ID: "rc_talign", ModelSHA256: strings.Repeat("ab", 32)}, r.ok, nil
}

func (r *talignRT) Emitter(context.Context, string) (alignrt.Emitter, error) {
	return nil, errors.New("talign: no worker in this test")
}

// talignLab is a Text tab lab with the alignment service on, one ready ebook paired with the book, and the
// job runner stopped so an alignment's job stays where the test puts it.
type talignLab struct {
	*textLab
	rt   *talignRT
	svc2 *align.Service
	file ebook.File
	ed   ebook.Edition
}

func newTalignLab(t *testing.T, runtime bool) *talignLab {
	t.Helper()
	l := &talignLab{textLab: newTextLab(t), rt: &talignRT{ok: runtime}}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO runtime_components(id, kind, name, language, revision, model_sha256, files_json, python_path, worker_sha256, licence_spdx, licence_url, source_url, checked_at)
VALUES ('rc_talign', 'emissions-model', 'w2v', 'en', 'r', ?, '[]', '/py', ?, 'MIT', 'u', 'u', 1)`, strings.Repeat("ab", 32), strings.Repeat("cd", 32)); err != nil {
		t.Fatal(err)
	}
	l.svc2 = align.NewService(align.Deps{Store: l.a.st, Library: l.a.lib, Ebooks: l.svc.Store(), Runner: l.a.runner,
		Runtimes: l.rt, DataDir: l.a.dir})
	if err := l.svc2.Register(l.a.runner); err != nil {
		t.Fatal(err)
	}
	l.a.srv.align = l.svc2
	l.run(t)
	l.file = l.addReady(t, "talign.epub", "Textword Lantern", 3)
	if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET duration_ms = 3600000 WHERE book_id = ?`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.a.runner.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ed, ok, err := l.svc.Store().CurrentEdition(context.Background(), l.file.ID)
	if err != nil || !ok {
		t.Fatalf("edition: %v %v", ok, err)
	}
	l.ed = ed
	return l
}

func (l *talignLab) alignPath(verb string) string { return l.path("/" + l.file.ID + "/" + verb) }

// status is the newest alignment of the edition.
func (l *talignLab) status(t *testing.T) align.Alignment {
	t.Helper()
	a, ok, err := l.svc2.Store().Newest(context.Background(), l.ed.ID)
	if err != nil || !ok {
		t.Fatalf("no alignment: %v %v", ok, err)
	}
	return a
}

// setRow puts the alignment in a state the job would have left it in.
func (l *talignLab) setRow(t *testing.T, id, state, code, labels string, coverage float64) {
	t.Helper()
	if _, err := l.a.st.Writer().Exec(`UPDATE alignments SET state = ?, error_code = ?, labels_json = ?, coverage = ? WHERE id = ?`,
		state, nullIf(code), labels, coverage, id); err != nil {
		t.Fatal(err)
	}
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestTextAlignStart(t *testing.T) {
	l := newTalignLab(t, true)
	body := l.get(l.owner, l.path(""), false).Body.String()
	if !strings.Contains(body, ">Prepare read-along<") || !strings.Contains(body, l.alignPath("align")) {
		t.Fatalf("the tab has no Prepare read-along button")
	}
	// An editor starts it: the plain form goes back to the tab, and the card shows the work in progress.
	rec := l.postForm(t, l.owner, l.alignPath("align"), false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != l.path("") {
		t.Fatalf("start: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	a := l.status(t)
	if a.State != align.StateQueued || a.JobID == "" || a.EditionID != l.ed.ID {
		t.Fatalf("alignment: %+v", a)
	}
	body = l.get(l.owner, l.path(""), false).Body.String()
	for _, want := range []string{"Preparing read-along", "Waiting for its turn", "This runs on your server", l.alignPath("align/cancel"),
		`hx-get="/hx/jobs/` + a.JobID + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("card misses %q", want)
		}
	}
	if strings.Contains(body, l.alignPath("align")+`"`) {
		t.Error("Prepare read-along is offered while one is running")
	}
	// Again: the active one is returned, not a second row.
	if r := l.postForm(t, l.admin, l.alignPath("align"), true); r.Code != http.StatusOK {
		t.Errorf("admin start (htmx): %d", r.Code)
	}
	var n int
	if err := l.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM alignments WHERE edition_id = ?`, l.ed.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("alignments: %d %v", n, err)
	}
	// Roles: nobody but an editor starts one, and a file that is not on this book is "not found".
	otherID := l.addPlaced(t, "other.epub", "ready", "")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, otherID); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"reader": l.reader, "noRead": l.noRead, "guest": l.guest, "guestNoR": l.guestNoR} {
		for _, verb := range []string{"align", "align/cancel", "align/resume", "align/discard"} {
			if r := l.postForm(t, ck, l.alignPath(verb), false); r.Code != http.StatusNotFound {
				t.Errorf("%s %s: %d", name, verb, r.Code)
			}
		}
	}
	if r := l.postForm(t, l.owner, l.path("/"+otherID+"/align"), false); r.Code != http.StatusNotFound {
		t.Errorf("unpaired file: %d", r.Code)
	}
	if r := l.postForm(t, l.owner, l.path("/nope/align"), false); r.Code != http.StatusNotFound {
		t.Errorf("unknown file: %d", r.Code)
	}
}

func TestTextAlignRuntimeMissing(t *testing.T) {
	l := newTalignLab(t, false)
	// The edition says it is English, and the edition's language comes first.
	want := "Read-along for English needs word timing for English. An admin can set it up."
	rec := l.postForm(t, l.owner, l.alignPath("align"), true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `class="ts-toast"`) || !strings.Contains(rec.Body.String(), want) {
		t.Errorf("htmx: %d %s", rec.Code, rec.Body.String())
	}
	rec = l.postForm(t, l.owner, l.alignPath("align"), false)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `role="alert"`) || !strings.Contains(rec.Body.String(), want) {
		t.Errorf("plain: %d", rec.Code)
	}
	if _, ok, _ := l.svc2.Store().Newest(context.Background(), l.ed.ID); ok {
		t.Error("a refused start left a row")
	}
}

func TestTextAlignPairAndPrepare(t *testing.T) {
	for _, runtime := range []bool{true, false} {
		l := newTalignLab(t, runtime)
		// The lab's file becomes a suggestion: no book, no pairing.
		if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, l.file.ID); err != nil {
			t.Fatal(err)
		}
		sg := l.file
		body := l.get(l.owner, l.path(""), false).Body.String()
		label := "Pair with this audiobook"
		if runtime {
			label = "Pair and prepare read-along"
		}
		if !strings.Contains(body, ">"+label+"<") {
			t.Errorf("runtime=%v: label %q is not on the tab", runtime, label)
		}
		if runtime && strings.Contains(body, ">Pair with this audiobook<") {
			t.Error("both labels are shown")
		}
		if r := l.postForm(t, l.owner, l.path("/"+sg.ID+"/pair"), false); r.Code != http.StatusSeeOther {
			t.Fatalf("pair: %d", r.Code)
		}
		if f := l.textPairFile(t, sg.ID); f.BookID != l.book.ID {
			t.Fatalf("not paired: %+v", f)
		}
		ed, ok, _ := l.svc.Store().CurrentEdition(context.Background(), sg.ID)
		if !ok {
			t.Fatal("no edition")
		}
		_, started, err := l.svc2.Store().Newest(context.Background(), ed.ID)
		if err != nil || started != runtime {
			t.Errorf("runtime=%v: started=%v %v", runtime, started, err)
		}
	}
}

func TestTextAlignCancelResumeDiscard(t *testing.T) {
	l := newTalignLab(t, true)
	post := func(verb string, htmx bool) *http.Response {
		return l.postForm(t, l.owner, l.alignPath(verb), htmx).Result()
	}
	// Nothing runs: all three are "already finished", as a toast for htmx.
	for _, verb := range []string{"align/cancel", "align/resume", "align/discard"} {
		rec := l.postForm(t, l.owner, l.alignPath(verb), true)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This has already finished.") {
			t.Errorf("%s with nothing running: %d %s", verb, rec.Code, rec.Body.String())
		}
	}
	// Cancel a queued one.
	if r := post("align", false); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("start: %d", r.StatusCode)
	}
	a := l.status(t)
	if r := post("align/cancel", false); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("cancel: %d", r.StatusCode)
	}
	if got := l.status(t); got.State != align.StateCancelled {
		t.Errorf("after cancel: %s", got.State)
	}
	var js string
	if err := l.a.st.Reader().QueryRow(`SELECT state FROM jobs WHERE id = ?`, a.JobID).Scan(&js); err != nil || js != "cancelled" {
		t.Errorf("job after cancel: %q %v", js, err)
	}
	// A running one that waits for a person: the reason, Resume and Discard.
	if r := post("align", false); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("restart: %d", r.StatusCode)
	}
	b := l.status(t)
	if b.ID == a.ID {
		t.Fatal("restart reused the cancelled row")
	}
	l.setRow(t, b.ID, align.StateHeld, align.HoldWorkerFailed, "[]", 0)
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'held' WHERE id = ?`, b.JobID); err != nil {
		t.Fatal(err)
	}
	body := l.get(l.owner, l.path(""), false).Body.String()
	for _, want := range []string{"Read-along is waiting for you", "The word timing stopped on part of this book", l.alignPath("align/resume"), l.alignPath("align/discard")} {
		if !strings.Contains(body, want) {
			t.Errorf("held card misses %q", want)
		}
	}
	if r := post("align/resume", true); r.StatusCode != http.StatusOK {
		t.Fatalf("resume: %d", r.StatusCode)
	}
	if err := l.a.st.Reader().QueryRow(`SELECT state FROM jobs WHERE id = ?`, b.JobID).Scan(&js); err != nil || js != "queued" {
		t.Errorf("job after resume: %q %v", js, err)
	}
	// Resume of one that is not held: already finished.
	l.setRow(t, b.ID, align.StateAligning, "", "[]", 0)
	if rec := l.postForm(t, l.owner, l.alignPath("align/resume"), true); rec.Code != http.StatusConflict {
		t.Errorf("resume while running: %d", rec.Code)
	}
	// Discard a held one.
	l.setRow(t, b.ID, align.StateHeld, align.HoldAudioChanged, "[]", 0)
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'held' WHERE id = ?`, b.JobID); err != nil {
		t.Fatal(err)
	}
	if r := post("align/discard", false); r.StatusCode != http.StatusSeeOther {
		t.Fatalf("discard: %d", r.StatusCode)
	}
	if got := l.status(t); got.State != align.StateCancelled {
		t.Errorf("after discard: %s", got.State)
	}
}

func TestTextAlignStates(t *testing.T) {
	l := newTalignLab(t, true)
	if r := l.postForm(t, l.owner, l.alignPath("align"), false); r.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", r.Code)
	}
	a := l.status(t)
	cases := []struct {
		name, state, code, labels string
		coverage                  float64
		want, not                 []string
	}{
		{"done", align.StateDone, "", "[]", 0.97, []string{"Read-along is ready for Textword Lantern"}, []string{">Prepare read-along<", "Cancel"}},
		{"partial", align.StatePartial, "", `["abridged"]`, 0.64, []string{"Read-along works for 64 % of the book.", "The recording seems shorter than the ebook"}, nil},
		{"different", align.StatePartial, "", `["different_edition"]`, 0.8, []string{"This looks like a different edition."}, nil},
		{"no match", align.StateFailed, align.CodeNoMatch, "[]", 0, []string{"This ebook does not seem to match the recording.", ">Try again<"}, nil},
		{"no runtime", align.StateFailed, "runtime_missing", "[]", 0, []string{"needs word timing", ">Try again<"}, nil},
		{"other", align.StateFailed, "decode_failed", "[]", 0, []string{"Tendshelf could not prepare read-along."}, nil},
		{"cancelled", align.StateCancelled, "", "[]", 0, []string{">Prepare read-along<"}, nil},
	}
	for _, c := range cases {
		l.setRow(t, a.ID, c.state, c.code, c.labels, c.coverage)
		body := l.get(l.owner, l.path(""), false).Body.String()
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: misses %q", c.name, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(body, w) {
				t.Errorf("%s: shows %q", c.name, w)
			}
		}
	}
	// Readers see no read-along controls on the tab.
	l.setRow(t, a.ID, align.StateDone, "", "[]", 0.97)
	if body := l.get(l.reader, l.path(""), false).Body.String(); strings.Contains(body, "Read-along is ready") || strings.Contains(body, "align") && strings.Contains(body, "/align") {
		t.Error("a reader sees the read-along card")
	}
}

func TestTextAlignJobFragment(t *testing.T) {
	l := newTalignLab(t, true)
	if r := l.postForm(t, l.owner, l.alignPath("align"), false); r.Code != http.StatusSeeOther {
		t.Fatalf("start: %d", r.Code)
	}
	a := l.status(t)
	path := "/hx/jobs/" + a.JobID
	// Running: the card asks for itself and shows progress from the job.
	if _, err := l.a.st.Writer().Exec(`UPDATE alignments SET state = 'aligning' WHERE id = ?`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'running', lease_until = 9999999999999, progress_json = ? WHERE id = ?`,
		`{"stage":"aligning","chaptersDone":1,"chaptersTotal":4,"leftMs":420000}`, a.JobID); err != nil {
		t.Fatal(err)
	}
	rec := l.get(l.owner, path, true)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "1 of 4") ||
		!strings.Contains(body, "Matching chapter 2 · Chapter 1") || !strings.Contains(body, "about 7 min left") ||
		strings.Contains(body, "<html") {
		t.Fatalf("running fragment: %d %s", rec.Code, body)
	}
	// Done: no polling, the result.
	l.setRow(t, a.ID, align.StateDone, "", "[]", 0.97)
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'done', lease_until = NULL WHERE id = ?`, a.JobID); err != nil {
		t.Fatal(err)
	}
	body = l.get(l.owner, path, true).Body.String()
	if strings.Contains(body, "hx-trigger") || !strings.Contains(body, "Read-along is ready for Textword Lantern") {
		t.Errorf("done fragment: %s", body)
	}
	// Other people: no such job.
	for name, ck := range map[string]*http.Cookie{"reader": l.reader, "noRead": l.noRead, "guest": l.guest} {
		if r := l.get(ck, path, true); r.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, r.Code)
		}
	}
	if r := l.get(l.owner, "/hx/jobs/nope", true); r.Code != http.StatusNotFound {
		t.Errorf("unknown job: %d", r.Code)
	}
}
