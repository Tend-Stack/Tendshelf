//go:build dev

package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// detailsShot is one captured surface of the details work (M4-10): a path under the book or author, the
// element to wait for, and whether it is a main shot (all themes) or a secondary one.
type detailsShot struct {
	name, path, wait string
	main             bool
}

// detailsCapture opens each shot at the widths and themes of visualMatrix and applies the checks every
// page test makes: no horizontal overflow, no target under 44 px, no unlabelled field, one h1, no
// skipped heading level and no inline style. Captures are named details-<name>-<theme>-<width>.png.
func detailsCapture(t *testing.T, b *cdp.Browser, base, out string, shots []detailsShot) {
	t.Helper()
	for _, p := range shots {
		for _, sh := range visualMatrix(p.main) {
			p, sh := p, sh
			t.Run(fmt.Sprintf("%s-%s-%d", p.name, sh.Theme, sh.Width), func(t *testing.T) {
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				sep := "?"
				if strings.Contains(p.path, "?") {
					sep = "&"
				}
				tab.Navigate(base + p.path + sep + "theme=" + sh.Theme)
				if p.wait != "" {
					tab.WaitFor(`Boolean(document.querySelector("`+p.wait+`"))`, 5*time.Second)
					time.Sleep(450 * time.Millisecond) // sheets slide in; measure them at rest
				}
				var m bookProbeResult
				tab.Eval(bookProbeJS, &m)
				switch {
				case m.Over:
					t.Errorf("horizontal overflow")
				case m.Theme != sh.Theme:
					t.Errorf("data-theme = %q, want %q", m.Theme, sh.Theme)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				// The shared probe knows only label[for]; these sheets also use wrapping labels and aria-label.
				var unlabelled []string
				tab.Eval(`[...document.querySelectorAll("input:not([type=hidden]), select")].filter(i =>
					!(i.labels && i.labels.length) && !i.getAttribute("aria-label") && !i.getAttribute("aria-labelledby")).map(i => i.id || i.name)`, &unlabelled)
				if len(unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				if m.Skip {
					t.Errorf("a heading level is skipped")
				}
				if m.Styled > 0 {
					t.Errorf("%d elements with a style attribute", m.Styled)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("details-%s-%s-%d.png", p.name, sh.Theme, sh.Width)))
			})
		}
	}
}

// detailsBoards copies the boards this work is compared with beside the captures.
func detailsBoards(out string) {
	for name, dir := range map[string]string{"ComparePhone": "core", "BookCover": "core", "NoMatchPhone": "reader", "AuthorPage": "browse", "AuthorTablet": "browse"} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", dir, name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "details-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}
}

// detailsPhoto writes a 320 px portrait (a soft gradient with a head and shoulders) as the author's photo.
func detailsPhoto(t *testing.T, a *testApp, personID string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 320))
	for y := 0; y < 320; y++ {
		for x := 0; x < 320; x++ {
			img.Set(x, y, color.RGBA{uint8(60 + y/6), uint8(90 + x/8), uint8(110 + y/8), 255})
		}
	}
	for y := 0; y < 320; y++ {
		for x := 0; x < 320; x++ {
			dx, dy := x-160, y-130
			head := dx*dx+dy*dy < 60*60
			dx2, dy2 := x-160, y-340
			shoulders := dx2*dx2/4+dy2*dy2 < 150*150/2
			if head || shoulders {
				img.Set(x, y, color.RGBA{214, 170, 140, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	dir := metadata.PeopleDir(a.dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("cd", 32)
	if err := os.WriteFile(filepath.Join(dir, sha+"-320.jpg"), buf.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`UPDATE people SET image_id = ?, description = ? WHERE id = ?`, sha+".jpg",
		"Writes quiet, weather-heavy mysteries set on the northern islands. Former harbour pilot.\n\nHer Harbour Lights series follows one family and one lamp across four winters.", personID); err != nil {
		t.Fatal(err)
	}
}

// detailsLogSteps is the log of the captured lookups: mid-search (a place being asked, two waiting), or finished.
func detailsLogSteps(running bool) []online.Step {
	q := `"The Lighthouse Keeper's Daughter"`
	steps := []online.Step{
		{Provider: "audnexus", Label: "Audible catalogue", Query: q, State: online.StepDone, Count: 5},
		{Provider: "audnexus", Label: "Audnexus", Region: "US", Query: "B0LIGHT001", State: online.StepDone, Count: 1, Cached: true},
		{Provider: "audnexus", Label: "Audnexus", Region: "US", Query: "B0LIGHT002", State: online.StepNone},
		{Provider: "google", Label: "Google Books", Query: q, State: online.StepFailed, Code: "busy"},
		{Provider: "openlibrary", Label: "Open Library", Query: q, State: online.StepRunning},
		{Provider: "itunes", Label: "Apple Books", State: online.StepWaiting},
	}
	if !running {
		steps[4].State, steps[4].Count = online.StepDone, 2
		steps[5] = online.Step{Provider: "itunes", Label: "Apple Books", Query: q, State: online.StepNone}
	}
	return steps
}

// detailsFinishJob finishes a queued lookup with the given candidates (the app never touches the network).
func detailsFinishJob(t *testing.T, a *testApp, cn *compareNet, id string, cands []metadata.Candidate) {
	t.Helper()
	if _, _, err := cn.client.FetchCover(context.Background(), compareCoverURL); err != nil {
		t.Fatal(err)
	}
	res, err := json.Marshal(metadata.LookupResult{Query: online.Query{Title: "The Lighthouse Keeper's Daughter"}, FetchedAt: 1_730_000_000_000,
		Searched: []string{"audnexus"}, Steps: detailsLogSteps(false), Candidates: cands})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = ? WHERE id = ?`, string(res), id); err != nil {
		t.Fatal(err)
	}
}

// TestVisualDetailsSheets captures the lookup sheet, the waiting, no-match, match-list and compare
// sheets, the cover picker, the author page with photo and bio, and the Details tab with its new
// actions, on the Harbour Lights book of the boards. Boards: ComparePhone, NoMatchPhone, AuthorPage,
// AuthorTablet (copied beside the captures).
func TestVisualDetailsSheets(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	detailsBoards(out)
	cn := &compareNet{}
	app := newTestApp(t, compareMod(t, cn))
	app.owner(t)
	bookID, authorID, _, _ := bookVisualSeed(t, app)
	ctx := context.Background()

	// The cover picker: the folder cover is current; an online and an uploaded picture wait beside it.
	covers := mediajobs.NewCovers(app.lib, mediatest.FakeTools(t), app.dir, app.runner, nil)
	app.srv.covers = covers
	for i, source := range []string{library.CoverOnline, library.CoverUpload, library.CoverEmbedded, library.CoverOnline} {
		tint := browsePalette[(i+4)%len(browsePalette)]
		sha, rel := browseWriteCover(t, app.dir, fmt.Sprintf("pick-%d", i), tint)
		cv := library.Cover{BookID: bookID, Source: source, Mime: "image/png", Width: 96, Height: 96, SHA256: sha, Path: rel, Tint: tint}
		if err := app.lib.Update(ctx, func(tx *library.Tx) error { return tx.PutCover(&cv) }); err != nil {
			t.Fatal(err)
		}
	}
	detailsPhoto(t, app, authorID)

	// Three lookups: one still waiting, one that found matches and one that found nothing.
	start := func() string {
		id, err := app.srv.details.StartLookup(ctx, bookID, online.Query{Title: "The Lighthouse Keeper's Daughter"}, false, "")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	waiting, found, none := start(), start(), start()
	detailsFinishJob(t, app, cn, found, []metadata.Candidate{
		{Provider: "audnexus", ProviderLabel: "Audible", Score: 0.95, Exact: true, CoverURL: compareCoverURL, CoverCached: true,
			Details: metadata.Details{Title: "The Lighthouse Keeper's Daughter", Narrators: []string{"Theodore Marsh"}, Publisher: "Harrow and Finch Ltd",
				Year: 2021, Genres: []string{"Literary mystery"}, Description: strings.Repeat("A winter on the island, told twice. ", 8)}},
		{Provider: "audnexus", ProviderLabel: "Audible", Score: 0.55,
			Details: metadata.Details{Title: "The Lighthouse Keeper's Daughter (Dramatised)", Year: 2022}},
	})
	detailsFinishJob(t, app, cn, none, nil)
	progress, err := json.Marshal(detailsLogSteps(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.st.Writer().Exec(`UPDATE jobs SET progress_json = ? WHERE id = ?`, string(progress), waiting); err != nil {
		t.Fatal(err)
	}
	base := themedServer(t, app.signedIn(t))
	book := "/books/" + bookID + "/details"
	sheet := func(extra string) string { return book + "?sheet=" + extra }

	detailsCapture(t, b, base, out, []detailsShot{
		{"lookup", sheet("lookup"), ".ts-lookup", true},
		{"compare-waiting", sheet("compare&job=" + waiting), "#compare-body", false},
		{"compare-nomatch", sheet("compare&job=" + none), "#compare-body", false},
		{"compare-list", sheet("compare&job=" + found), "#compare-body", false},
		{"compare", sheet("compare&job=" + found + "&c=0"), "table.ts-compare", true},
		{"cover", sheet("cover"), ".ts-cover-pick__card", true},
		{"author", "/authors/" + authorID, "", true},
		{"tab", book, ".ts-dpanel", false},
	})
}

// TestVisualDetailsWrite captures the write sheet (ready, refused) and the pending panel with its
// Looks good and Undo buttons, on the real write job over the fake ffmpeg.
func TestVisualDetailsWrite(t *testing.T) {
	// The write labs create members whose names carry a counter that other captures print ("admin-2");
	// put the counter back afterwards so those captures do not change.
	seq := memberCounter.Load()
	t.Cleanup(func() { memberCounter.Store(seq) })
	b := chromium(t)
	out := visualDir(t, "chromium")

	ready := newWriteLab(t, 2, false)
	ready.start(t)
	refused := newWriteLab(t, 1, true)
	readyBase := themedServer(t, ready.a.signedIn(t))
	refusedBase := themedServer(t, refused.a.signedIn(t))
	detailsCapture(t, b, readyBase, out, []detailsShot{{"write-ready", ready.sheet(), ".ts-write", true}})
	detailsCapture(t, b, refusedBase, out, []detailsShot{{"write-refused", refused.sheet(), ".ts-write__refused", false}})

	job := ready.jobOf(t, ready.post(t, ready.owner, "/books/"+ready.book.ID+"/write"))
	ready.waitDone(t, ready.owner, job)
	detailsCapture(t, b, readyBase, out, []detailsShot{
		{"write-pending-sheet", ready.sheet() + "&job=" + job, ".ts-write__pending", false},
		{"write-pending", ready.tab(), ".ts-write__pending", true},
	})

	// After Looks good the options panel leads with "Kept the new details."
	ready.post(t, ready.owner, "/books/"+ready.book.ID+"/write/confirm")
	detailsCapture(t, b, readyBase, out, []detailsShot{{"write-kept", ready.tab() + "?written=kept", ".ts-dpanel", false}})
}
