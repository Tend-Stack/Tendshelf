//go:build dev

package web

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

const listenLongTitle = "The Remarkably Long and Winding Title of a Very Patient Book About Nothing in Particular"

// listeningVisualApp stands the clock on the board's day (Saturday 2026-11-07 10:00 UTC) and seeds a
// person who has listened since March 2024: most days, mornings and evenings, with books finished in
// October. The random source is fixed, so the pages come out the same on every run.
func listeningVisualApp(t *testing.T) *listeningWorld {
	t.Helper()
	w := newListeningWorld(t)
	_, f := librarytest.NewLibrary(t, w.a.lib, "More", adminAudioDir(t, false))
	type bookSpec struct{ title, author, narrator string }
	specs := []bookSpec{
		{"North and South", "Elizabeth Gaskell", "Ana Ortiz"}, {"Moby-Dick", "Herman Melville", "Tomás Reyes"},
		{"Great Expectations", "Charles Dickens", "James Whitlow"}, {listenLongTitle, "Ada Lindqvist", "Ana Ortiz"},
		{"Middlemarch", "George Eliot", "Tomás Reyes"}, {"Emma", "Jane Austen", "Ana Ortiz"},
	}
	var books []string
	for _, s := range specs {
		b := librarytest.AddBook(t, w.a.lib, f, librarytest.BookSpec{Title: s.title, DurationMs: 36_000_000, Authors: []string{s.author}, Narrators: []string{s.narrator}})
		books = append(books, b.ID)
	}
	rng := rand.New(rand.NewSource(11))
	tx, err := w.a.st.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	end := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	for day := time.Date(2024, 3, 12, 0, 0, 0, 0, time.UTC); day.Before(end); day = day.AddDate(0, 0, 1) {
		if rng.Intn(100) < 18 {
			continue
		}
		for k := 0; k < 1+rng.Intn(3); k++ {
			hour := []int{7, 8, 9, 12, 13, 17, 19, 20, 21, 22, 23}[rng.Intn(11)]
			start := day.Add(time.Duration(hour)*time.Hour + time.Duration(rng.Intn(50))*time.Minute)
			ms := int64(15+rng.Intn(50)) * 60_000
			n++
			if _, err := tx.Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, ?)`, fmt.Sprintf("ses_v%d", n), w.ownerID, books[rng.Intn(3)+rng.Intn(2)*rng.Intn(4)], start.UnixMilli(), start.UnixMilli()+ms, ms, ms,
				[]int{100, 120, 125, 150}[rng.Intn(4)]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	statsFinish(t, w.a, w.ownerID, books[0], time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC))
	statsFinish(t, w.a, w.ownerID, books[1], time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC))
	statsFinish(t, w.a, w.ownerID, books[3], time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC))
	statsFinish(t, w.a, w.ownerID, books[4], time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC))
	statsFinish(t, w.a, w.ownerID, books[5], time.Date(2025, 8, 2, 12, 0, 0, 0, time.UTC))
	return w
}

// listeningProbeJS measures what the plan's browser assertions need: the cards (title, top, height,
// overflow), the visible heat grid against its card, and the Finished titles.
const listeningProbeJS = `(() => {
  const vis = e => e && e.getBoundingClientRect().width > 0;
  const cards = [...document.querySelectorAll(".ts-dash__card")].map(c => {
    const r = c.getBoundingClientRect();
    return {title: c.querySelector("h2").textContent, top: Math.round(r.top + scrollY), h: r.height, over: c.scrollWidth > c.clientWidth + 1};
  });
  const heat = [...document.querySelectorAll(".ts-heat")].find(vis);
  let heatInfo = null;
  if (heat) {
    const card = heat.closest(".ts-dash__card");
    const cs = getComputedStyle(card);
    const content = card.clientWidth - parseFloat(cs.paddingLeft) - parseFloat(cs.paddingRight);
    const plot = heat.querySelector(".ts-heat__plot").getBoundingClientRect().width;
    const rows = heat.querySelector(".ts-heat__rows").getBoundingClientRect().width;
    const gap = parseFloat(getComputedStyle(heat).columnGap);
    heatInfo = {kind: heat.classList.contains("ts-heat--months") ? "months" : "weeks", scrollW: heat.scrollWidth, clientW: heat.clientWidth,
      plot, content, rows, gap, cardOver: card.scrollWidth > card.clientWidth + 1};
  }
  const done = [...document.querySelectorAll(".ts-dash__donetitle")].map(t => {
    const s = getComputedStyle(t);
    const lh = parseFloat(s.lineHeight);
    const a = t.closest("a");
    return {text: t.textContent, font: parseFloat(s.fontSize), lines: Math.round(t.getBoundingClientRect().height / lh),
      height: t.getBoundingClientRect().height, lh, clamped: t.scrollHeight > t.clientHeight + 1,
      link: [...a.children].filter(c => c.getAttribute("aria-hidden") !== "true").map(c => c.textContent).join(" ")};
  });
  const minFont = Math.min(...[...document.querySelectorAll(".ts-dash *")].filter(e => e.childNodes.length && [...e.childNodes].some(n => n.nodeType === 3 && n.textContent.trim()) && !e.closest(".ts-sr")).map(e => parseFloat(getComputedStyle(e).fontSize)));
  return {cards, heat: heatInfo, done, minFont};
})()`

type listeningProbe struct {
	Cards []struct {
		Title string  `json:"title"`
		Top   int     `json:"top"`
		H     float64 `json:"h"`
		Over  bool    `json:"over"`
	} `json:"cards"`
	Heat *struct {
		Kind     string  `json:"kind"`
		ScrollW  float64 `json:"scrollW"`
		ClientW  float64 `json:"clientW"`
		Plot     float64 `json:"plot"`
		Content  float64 `json:"content"`
		Rows     float64 `json:"rows"`
		Gap      float64 `json:"gap"`
		CardOver bool    `json:"cardOver"`
	} `json:"heat"`
	Done []struct {
		Text    string  `json:"text"`
		Font    float64 `json:"font"`
		Lines   int     `json:"lines"`
		Height  float64 `json:"height"`
		LH      float64 `json:"lh"`
		Clamped bool    `json:"clamped"`
		Link    string  `json:"link"`
	} `json:"done"`
	MinFont float64 `json:"minFont"`
}

// TestListeningVisual captures /me/listening, Month (October 2026, one step back) and Year, in dark and
// light at 390, 768, 1024 and 1440 px (make visual-full), the board beside them, and asserts the layout
// rules of the plan in the browser: the heat grid never scrolls, the weeks plot spans its card, the two
// bar-chart cards share a row on wide screens and no others do, and the Finished titles are two lines
// of at least 13 px with the full title in the link.
func TestListeningVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	w := listeningVisualApp(t)
	base := themedServer(t, w.a.signedIn(t))

	boardShot := func(theme string) {
		tab := b.NewTab(1500, 900, false, false)
		abs, _ := filepath.Abs(filepath.Join("..", "..", "docs", "design", "boards", "ListeningDashboard.html"))
		tab.Navigate("file://" + abs)
		tab.WaitFor(`document.fonts.ready.then(() => true) && document.querySelector(".ts-dash") !== null`, 10*time.Second)
		tab.Eval(`document.getElementById("bd-`+theme+`").checked = true`, nil)
		time.Sleep(300 * time.Millisecond)
		tab.ScreenshotFull(filepath.Join(out, "listening-board-"+theme+".png"))
	}
	boardShot("dark")
	boardShot("light")

	shoot := func(t *testing.T, path, theme string, width int, name string) {
		cur, err := w.a.srv.users.LoadSettings(t.Context(), w.ownerID)
		if err != nil {
			t.Fatal(err)
		}
		cur.Theme = theme // a saved theme beats the cookie
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
			// A title link stretches over its whole row (::after), so its own box is not the target.
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
		if p.Heat != nil {
			h := p.Heat
			if h.ScrollW > h.ClientW+1 || h.CardOver {
				t.Errorf("heat grid scrolls: %+v", h)
			}
			if width < 768 && h.Kind != "months" || width >= 768 && h.Kind != "weeks" {
				t.Errorf("%d px shows the %s layout", width, h.Kind)
			}
			if want := h.Content - h.Rows - h.Gap; h.Kind == "weeks" && (h.Plot < want-2 || h.Plot > want+2) {
				t.Errorf("weeks plot %.1f px, card content minus labels %.1f px", h.Plot, want)
			}
		}
		if width >= 1024 {
			rows := map[int][]string{}
			var timeH, dowH float64
			for _, c := range p.Cards {
				rows[c.Top] = append(rows[c.Top], c.Title)
				switch c.Title {
				case "Listening time":
					timeH = c.H
				case "Days of the week":
					dowH = c.H
				}
			}
			for top, titles := range rows {
				if len(titles) > 1 && !(len(titles) == 2 && strings.Contains(strings.Join(titles, "|"), "Listening time") && strings.Contains(strings.Join(titles, "|"), "Days of the week")) {
					t.Errorf("cards %v share a row at y=%d", titles, top)
				}
			}
			if dowH > 0 && (timeH-dowH > 48 || dowH-timeH > 48) {
				t.Errorf("Listening time %.0f px and Days of the week %.0f px tall", timeH, dowH)
			}
		}
		for _, d := range p.Done {
			if d.Font < 13 || d.Lines > 2 || d.Height > 2*d.LH+1 {
				t.Errorf("finished title %q: %.1f px, %d lines", d.Text, d.Font, d.Lines)
			}
			if d.Text == listenLongTitle && !d.Clamped && width < 1024 {
				t.Logf("the long title is not clamped at %d px", width)
			}
			if !strings.HasPrefix(d.Link, d.Text+" ") {
				t.Errorf("link name %q does not start with the full title %q", d.Link, d.Text)
			}
		}
		tab.ScreenshotFull(filepath.Join(out, name))
	}
	for _, view := range []struct{ name, path string }{
		{"month", "/me/listening?range=month&at=2026-10-15"},
		{"year", "/me/listening?range=year"},
	} {
		for _, sh := range visualMatrix(false) {
			t.Run(fmt.Sprintf("%s-%s-%d", view.name, sh.Theme, sh.Width), func(t *testing.T) {
				shoot(t, view.path, sh.Theme, sh.Width, fmt.Sprintf("listening-%s-%s-%d.png", view.name, sh.Theme, sh.Width))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}

func sepFor(path string) string {
	if strings.Contains(path, "?") {
		return "&"
	}
	return "?"
}
