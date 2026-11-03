//go:build dev

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// lookupsProbeJS is adminProbeJS for pages with the lookups checkbox: a checkbox is 24 px square, so
// its target is the label around it, which must be 44 px high.
const lookupsProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main button, main a.ts-btn, main summary, main select, main input:not([type=hidden]):not([type=checkbox]):not([type=radio]), body.ts-frame button, body.ts-frame a.ts-btn, body.ts-frame input:not([type=hidden]):not([type=checkbox]):not([type=radio]), label.ts-admin__choice")) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("input:not([type=hidden]), select")].filter(i => i.labels.length === 0).map(i => i.id || i.name);
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// TestVisualLookups captures the lookups part of Libraries & scanning (idle, running, paused, with
// books that need a look), the "Books that need a look" list, Add a folder and the setup step in dark
// and light at 390, 768, 1024 and 1440, and fails on horizontal overflow, a target under 44 px, an
// unlabelled input or a page without exactly one h1.
func TestVisualLookups(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t, compareMod(t, &compareNet{}))
	adminSeed(t, app)
	ctx := context.Background()
	libs, err := app.lib.Libraries(ctx)
	if err != nil || len(libs) != 1 {
		t.Fatalf("libraries: %v %v", libs, err)
	}
	libID := libs[0].ID
	base := themedServer(t, app.signedIn(t))

	// Two books whose lookup could not settle: several matches, and nothing found.
	var books [2]struct{ ID string }
	for i, title := range []string{"Saltmarsh", "The Long Tide"} {
		if err := app.st.Reader().QueryRow(`SELECT id FROM books WHERE title = ?`, title).Scan(&books[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	needs := func() {
		err := app.lib.Update(ctx, func(tx *library.Tx) error {
			if err := tx.PutBookLookup(books[0].ID, library.LookupNeedsLook, "audnexus", "", 1_730_000_000_000); err != nil {
				return err
			}
			return tx.PutBookLookup(books[1].ID, library.LookupNoMatch, "audnexus", "", 1_730_000_000_000)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// progress sets the job's state and progress; the pause is at a fixed time of day so the
	// capture does not change from run to run.
	var jobID string
	progress := func(state string, p metadata.BulkProgress) {
		t.Helper()
		if jobID == "" {
			if jobID, err = app.srv.details.StartBulk(ctx, libID, false, ""); err != nil {
				t.Fatal(err)
			}
		}
		raw, _ := json.Marshal(p)
		app.adminSetJobState(t, jobID, state)
		if _, err := app.st.Writer().Exec(`UPDATE jobs SET progress_json = ?, result_json = ? WHERE id = ?`, string(raw), string(raw), jobID); err != nil {
			t.Fatal(err)
		}
	}
	day := time.Now().UTC().Truncate(24 * time.Hour).Add(48*time.Hour + 6*time.Hour)

	type page struct{ name, path string }
	states := []struct {
		name  string
		setup func()
		pages []page
	}{
		{"idle", func() {}, []page{{"libraries-idle", "/admin/libraries"}, {"add-folder", "/admin/libraries/folders/new"}, {"setup-folder", "/setup/folder"}}},
		{"running", func() {
			progress("running", metadata.BulkProgress{Done: 37, Total: 120, Matched: 29, NeedsLook: 4, NotFound: 4})
		}, []page{{"libraries-running", "/admin/libraries"}}},
		{"paused", func() {
			progress("running", metadata.BulkProgress{Done: 37, Total: 120, Matched: 29, NeedsLook: 4, NotFound: 4,
				PausedUntil: day.UnixMilli(), PauseReason: metadata.PauseBusy})
		}, []page{{"libraries-paused", "/admin/libraries"}}},
		{"needs", func() {
			progress("done", metadata.BulkProgress{Done: 120, Total: 120, Matched: 109, NeedsLook: 2, NotFound: 9})
			needs()
		}, []page{{"libraries-needs", "/admin/libraries"}, {"needs-look", "/admin/libraries/" + libID + "/lookups"}}},
	}
	for _, st := range states {
		st.setup()
		for _, pg := range st.pages {
			for _, theme := range []string{"dark", "light"} {
				for _, w := range []int{390, 768, 1024, 1440} {
					t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
						tab := b.NewTab(w, 900, w == 390, false)
						tab.Navigate(base + pg.path + "?theme=" + theme)
						var m struct {
							Over       bool     `json:"over"`
							Theme      string   `json:"theme"`
							Small      []string `json:"small"`
							Unlabelled []string `json:"unlabelled"`
							H1         int      `json:"h1"`
						}
						tab.Eval(lookupsProbeJS, &m)
						if m.Over {
							t.Errorf("horizontal overflow")
						}
						if m.Theme != theme {
							t.Errorf("data-theme = %q, want %q", m.Theme, theme)
						}
						if len(m.Small) > 0 {
							t.Errorf("targets under 44 px: %v", m.Small)
						}
						if len(m.Unlabelled) > 0 {
							t.Errorf("inputs without a label: %v", m.Unlabelled)
						}
						if m.H1 != 1 {
							t.Errorf("%d h1 elements", m.H1)
						}
						tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("lookups-%s-%s-%d.png", pg.name, theme, w)))
					})
				}
			}
		}
	}
}
