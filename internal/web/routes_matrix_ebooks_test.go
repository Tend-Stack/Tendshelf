package web

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// ebookSeed gives the sweep app the Text tab (M5 R1): the ebook service, one waiting read of the
// seeded book and one refused upload for each person who may clear it (owner and admin), so every
// dismiss row has a file of its own. The sweep never runs jobs.
func ebookSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	a := e.a
	ctx := context.Background()
	svc := ebook.New(a.st, a.lib, a.runner, a.dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.Register(a.runner); err != nil {
		t.Fatal(err)
	}
	a.srv.ebooks = svc
	f, err := svc.AddUpload(ctx, e.book, e.uid[sweepOwner], "sweep.epub", bytes.NewReader(textEPUB(t, "Sweep Ebook", 2)))
	if err != nil || f.JobID == "" {
		t.Fatalf("seed ebook: %v %+v", err, f)
	}
	e.ebookJob = f.JobID
	for i, name := range []string{"Sweep Owner Ebook", "Sweep Admin Ebook"} {
		f, err := svc.AddUpload(ctx, e.book, e.uid[sweepOwner], "refused.epub", bytes.NewReader(textEPUB(t, name, 2)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.st.Writer().ExecContext(ctx, `UPDATE ebook_files SET state='refused', refused_reason='drm' WHERE id=?`, f.ID); err != nil {
			t.Fatal(err)
		}
		e.ebookRefused[i] = f.ID
	}
}

// ebookSweepRows are the route matrix rows of the Text tab. Owner and admin may add and clear
// ebooks; a member and a guest never reach those routes, and without can_read never the tab either.
func ebookSweepRows() []sweepRow {
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	tab := sweepGet("GET /books/{id}/{tab}", sweepBookPath("/text"), staff)
	tab.Label = "text"
	poll := sweepGet("GET /hx/jobs/{id}", func(e *sweepEnv, _ int) string { return "/hx/jobs/" + e.ebookJob }, staff)
	poll.Label = "ebook read"
	// No file part in a urlencoded form: staff get the form back with a refusal.
	add := sweepPost("POST /books/{id}/text", sweepBookPath("/text"), sweepFormOf(url.Values{}),
		sweepStaff(http.StatusUnprocessableEntity, http.StatusNotFound), "")
	dismiss := sweepPost("POST /books/{id}/text/{fileID}/dismiss", func(e *sweepEnv, role int) string {
		return "/books/" + e.book.ID + "/text/" + e.ebookRefused[min(role, 1)] + "/dismiss"
	}, sweepFormOf(url.Values{}), sweepStaff(http.StatusSeeOther, http.StatusNotFound), "")
	dismiss.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/text" }
	return []sweepRow{tab, poll, add, dismiss}
}
