package web

import (
	"bytes"
	"context"
	"fmt"
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
	// Ready files for the pairing rows: two on the book (default, choose, and one unpair each for the
	// owner and the admin) and two unpaired (pair, not-same). They have no edition; the routes only
	// need the row.
	e.ebookPair = map[string]string{}
	for i, name := range []string{"default", "choose", "unpair0", "unpair1", "pair", "not-same"} {
		id := placeEbook(t, a, e.book, e.uid[sweepOwner], "sweep-"+name+".epub", "ready", "")
		if _, err := a.st.Writer().ExecContext(ctx, `UPDATE ebook_files SET sha256 = ?, quick_hash = ? WHERE id = ?`,
			fmt.Sprintf("%064x", 5000+i), fmt.Sprintf("%064x", 5000+i), id); err != nil {
			t.Fatal(err)
		}
		if name == "pair" || name == "not-same" {
			if _, err := a.st.Writer().ExecContext(ctx, `UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, id); err != nil {
				t.Fatal(err)
			}
		}
		e.ebookPair[name] = id
	}
	ebookInboxSeed(t, e)
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
	rows := []sweepRow{tab, poll, add, dismiss}
	acts := func(verb, key string, want [4]int) sweepRow {
		r := sweepPost("POST /books/{id}/text/{fileID}/"+verb, func(e *sweepEnv, role int) string {
			k := key
			if key == "unpair" {
				k += fmt.Sprint(min(role, 1))
			}
			return "/books/" + e.book.ID + "/text/" + e.ebookPair[k] + "/" + verb
		}, sweepFormOf(url.Values{}), want, "")
		r.LocOf = func(e *sweepEnv) string { return "/books/" + e.book.ID + "/text" }
		return r
	}
	for _, verb := range []string{"pair", "unpair", "not-same", "default"} {
		rows = append(rows, acts(verb, verb, sweepStaff(http.StatusSeeOther, http.StatusNotFound)))
	}
	// Choosing needs only the right to read the text: a member or guest without can_read gets 404.
	return append(rows, acts("choose", "choose", sweepStaff(http.StatusSeeOther, http.StatusNotFound)))
}
