//go:build dev

package web

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// rstVisualApp is the listening visual world with reading added for the owner: most days of 2026 with a
// session or two, a yearly goal of 12 and four books finished this year. The random source is fixed.
func rstVisualApp(t *testing.T) *listeningWorld {
	t.Helper()
	w := listeningVisualApp(t)
	rows, err := w.a.st.Reader().Query(`SELECT id FROM books ORDER BY title`)
	if err != nil {
		t.Fatal(err)
	}
	var books []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		books = append(books, id)
	}
	rows.Close()
	rng := rand.New(rand.NewSource(5))
	end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	for day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC); day.Before(end); day = day.AddDate(0, 0, 1) {
		if rng.Intn(100) < 35 {
			continue
		}
		at := day.Add(time.Duration(20+rng.Intn(3))*time.Hour + time.Duration(rng.Intn(50))*time.Minute)
		mins := int64(10 + rng.Intn(70))
		rstRead(t, w, w.ownerID, books[rng.Intn(len(books))], at, mins, mins*1100)
	}
	for i, d := range []time.Time{time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC), time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)} {
		rstFinish(t, w, w.ownerID, books[i], d)
	}
	if err := w.a.srv.users.SaveReadingGoal(t.Context(), w.ownerID, users.ReadingGoal{Books: 12}); err != nil {
		t.Fatal(err)
	}
	return w
}

// TestReadStatsVisual captures /me/reading (Month, one step back, and Year) in dark and light at 390, 768,
// 1024 and 1440 px and asserts the page fits: no sideways scroll, one h1, text of at least 13 px, no card
// that overflows, and a goal field of at least 44 px.
func TestReadStatsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	w := rstVisualApp(t)
	base := themedServer(t, w.a.signedIn(t))
	shoot := func(t *testing.T, path, theme string, width int, name string) {
		cur, err := w.a.srv.users.LoadSettings(t.Context(), w.ownerID)
		if err != nil {
			t.Fatal(err)
		}
		cur.Theme = theme
		if err := w.a.srv.users.SaveSettings(t.Context(), w.ownerID, cur); err != nil {
			t.Fatal(err)
		}
		tab := b.NewTab(width, 900, width == 390, false)
		if err := tab.Call("Emulation.setTimezoneOverride", map[string]any{"timezoneId": "UTC"}, nil); err != nil {
			t.Fatal(err)
		}
		tab.Navigate(base + path + sepFor(path) + "theme=" + theme)
		tab.WaitFor(`document.querySelector(".ts-dash__card") !== null`, 5*time.Second)
		tab.Eval(`document.fonts.ready.then(() => true)`, nil)
		time.Sleep(300 * time.Millisecond)
		var m browseProbe
		tab.Eval(browseProbeJS, &m)
		if m.Theme != theme {
			t.Fatalf("data-theme = %q, want %q", m.Theme, theme)
		}
		if m.Over {
			t.Errorf("horizontal overflow")
		}
		if m.H1 != 1 {
			t.Errorf("%d h1, want 1", m.H1)
		}
		for _, s := range m.Small {
			if !strings.HasPrefix(s, "label ts-dash__topname") {
				t.Errorf("target under 44 px: %s", s)
			}
		}
		var p listeningProbe
		tab.Eval(listeningProbeJS, &p)
		if p.MinFont < 13 {
			t.Errorf("text as small as %.1f px", p.MinFont)
		}
		for _, c := range p.Cards {
			if c.Over {
				t.Errorf("card %q overflows sideways", c.Title)
			}
		}
		tab.ScreenshotFull(filepath.Join(out, name))
	}
	for _, view := range []struct{ name, path string }{
		{"month", "/me/reading?range=month&at=2026-10-15"},
		{"year", "/me/reading?range=year"},
	} {
		for _, sh := range visualMatrix(false) {
			t.Run(fmt.Sprintf("%s-%s-%d", view.name, sh.Theme, sh.Width), func(t *testing.T) {
				shoot(t, view.path, sh.Theme, sh.Width, fmt.Sprintf("reading-%s-%s-%d.png", view.name, sh.Theme, sh.Width))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
