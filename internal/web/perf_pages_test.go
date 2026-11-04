package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Performance of the main pages at the four audit widths. `make perf` runs TestPerfPages (it needs
// TENDSHELF_TEST_PERF=1 and TENDSHELF_TEST_BROWSER). The fixture is the 2,000-book library of
// TestPerfLibraryPage plus progress on 20 books and a year of listening sessions for the owner. The
// budget is the house one (sweepPerfBudget, median interactive at a 4x CPU throttle) at every width.
// The largest contentful paint is logged and written but has no threshold yet: in headless Chromium on
// localhost it is mostly render time, so the operator may set one after a week of numbers.

// sweepPerfWidth is one viewport of the audit.
type sweepPerfWidth struct {
	W, H   int
	Mobile bool // width under 1280: handled as a phone viewport
}

// sweepPerfWidths are the four widths of the milestone: 390, 768, 1024 and 1440.
var sweepPerfWidths = []sweepPerfWidth{{390, 844, true}, {768, 1024, true}, {1024, 768, true}, {1440, 900, false}}

// sweepPerfPage is one measured page: a name for the subtest and JSON, and the path it loads.
type sweepPerfPage struct{ Name, Path string }

// sweepPerfPages are the five pages TestPerfPages measures; the weight test adds two more.
func sweepPerfPages(bookID string) []sweepPerfPage {
	return []sweepPerfPage{
		{"home", "/"}, {"library", "/libraries"}, {"book", "/books/" + bookID},
		{"listening", "/me/listening?range=year"}, {"search", "/search?q=Archive"},
	}
}

// sweepPerfPagesApp is sweepPerfApp plus progress on 20 books and 365 days of sessions for the
// owner. It returns the app and the id of one book to open.
func sweepPerfPagesApp(t *testing.T) (*testApp, string) {
	t.Helper()
	a := sweepPerfApp(t, t)
	ctx := context.Background()
	uid := sweepUserID(t, a, a.owner(t))
	var ids []string
	rows, err := a.st.Reader().Query(`SELECT id FROM books ORDER BY added_at DESC LIMIT 200`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) < 20 {
		t.Fatalf("only %d books seeded", len(ids))
	}
	for i, id := range ids[:20] {
		dur := int64(1+(i*7)%30) * 3_000_000
		if _, err := a.srv.progress.Save(ctx, progress.Update{UserID: uid, BookID: id, PositionMs: dur * int64(10+i*4) / 100, DurationMs: dur,
			ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "perfpages0001", DeviceLabel: "Test"}); err != nil {
			t.Fatal(err)
		}
	}
	sweepPerfSessions(t, a.st, uid, ids, rand.New(rand.NewSource(7)), time.Now().UTC(), 365)
	return a, ids[0]
}

func TestPerfPages(t *testing.T) {
	sweepPerfOn(t)
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	a, bookID := sweepPerfPagesApp(t)
	srv := httptest.NewServer(a.signedIn(t))
	t.Cleanup(srv.Close)
	b := cdp.Start(t, chrome)

	type cell struct {
		Loads       []sweepLoad `json:"loads"`
		MedianMs    float64     `json:"medianInteractiveMs"`
		MedianLCPMs float64     `json:"medianLcpMs"`
		Pass        bool        `json:"pass"`
	}
	results := map[string]map[string]cell{}
	for _, pg := range sweepPerfPages(bookID) {
		results[pg.Name] = map[string]cell{}
		for _, w := range sweepPerfWidths {
			t.Run(fmt.Sprintf("%s/%d", pg.Name, w.W), func(t *testing.T) {
				_, warm := sweepMeasureLoad(t, b, srv.URL+pg.Path, w.W, w.H, w.Mobile, false) // warm-up: caches and fonts
				warm.Close()
				var c cell
				var inter, lcp []float64
				for i := 0; i < sweepPerfLoads; i++ {
					l, tab := sweepMeasureLoad(t, b, srv.URL+pg.Path, w.W, w.H, w.Mobile, false)
					tab.Close()
					c.Loads = append(c.Loads, l)
					inter = append(inter, l.InteractiveMs)
					lcp = append(lcp, l.LCPMs)
				}
				c.MedianMs, c.MedianLCPMs = sweepMedian(inter), sweepMedian(lcp)
				c.Pass = c.MedianMs <= sweepPerfBudget
				results[pg.Name][fmt.Sprint(w.W)] = c
				t.Logf("%s at %d: median interactive %.0f ms (budget %.0f), median LCP %.0f ms, html %d bytes",
					pg.Name, w.W, c.MedianMs, sweepPerfBudget, c.MedianLCPMs, c.Loads[0].HTMLBytes)
				if !c.Pass {
					t.Errorf("%s at %d px: median interactive %.0f ms is over the %.0f ms budget", pg.Name, w.W, c.MedianMs, sweepPerfBudget)
				}
			})
		}
	}

	dir := filepath.Join("..", "..", ".local", "tests", "perf", time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(map[string]any{
		"cpu": sweepCPUModel(), "throttle": sweepPerfRate, "books": sweepPerfBooks, "budgetMs": sweepPerfBudget, "pages": results,
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "pages.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("cpu: %s; throttle %dx; json in %s", sweepCPUModel(), sweepPerfRate, dir)
}
