//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The player kinds (decision 0043, P4): one phone player for a book, a podcast episode and a radio station. These tests open
// each kind in the real full player and look at what is drawn.

const pkThreeChapters = `{"version":"1.2.0","chapters":[{"startTime":0,"title":"Intro"},{"startTime":3,"title":"Middle"},{"startTime":6,"title":"End"}]}`

// pkEpisode plays the first episode of a fake show (with three chapters, or none), pauses and opens the full player.
func pkEpisode(t *testing.T, chapters bool, width, height int, theme string) (tab *cdp.Tab, w *podWorld, base string) {
	t.Helper()
	b := chromium(t)
	w = podWavWorld(t)
	if chapters {
		if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET chapters_json = ? WHERE id = ?`, pkThreeChapters, w.eps[0]); err != nil {
			t.Fatal(err)
		}
	}
	base, _ = podPlayServer(t, w)
	tab = b.NewTab(width, height, width < 768, true)
	tab.Navigate(base + "/podcasts/episodes/" + w.eps[0])
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	playerClickSel(t, tab, `[data-ts-play="`+w.eps[0]+`"]`)
	tab.WaitFor(`window.tsPlayer.state().playing`, 15*time.Second)
	fullpOpenPlayer(t, tab)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	pkTheme(tab, theme)
	return tab, w, base
}

// pkStation plays a fake live station and opens the full player.
func pkStation(t *testing.T, width, height int, theme string) (tab *cdp.Tab, w *radioWorld, id string) {
	t.Helper()
	b := chromium(t)
	w, id, base := radioBrowserWorld(t, "Harbour FM")
	tab = b.NewTab(width, height, width < 768, true)
	tab.Navigate(base + "/radio")
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	playerClickSel(t, tab, `[data-ts-play="`+id+`"]`)
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().itemKind === "radio"`, 15*time.Second)
	fullpOpenPlayer(t, tab)
	pkTheme(tab, theme)
	return tab, w, id
}

func pkTheme(tab *cdp.Tab, theme string) {
	tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
	time.Sleep(150 * time.Millisecond)
}

// pkShown lists which of the selectors (inside #player-full) are drawn.
func pkShown(tab *cdp.Tab, sels ...string) map[string]bool {
	out := map[string]bool{}
	tab.Eval(`(() => { const d = `+ppDialog+`, r = {}; for (const s of `+pkJSON(sels)+`) { r[s] = [...d.querySelectorAll(s)].some((e) => e.offsetWidth > 0 || e.offsetHeight > 0); } return r; })()`, &out)
	return out
}

func pkJSON(list []string) string {
	quoted := make([]string, len(list))
	for i, s := range list {
		quoted[i] = cdp.Quote(s)
	}
	return "[" + strings.Join(quoted, ",") + "]"
}

// pkBar returns the labels of the settings bar's drawn items and their widths.
func pkBar(tab *cdp.Tab) (labels []string, widths []float64) {
	var r struct {
		Labels []string
		Widths []float64
	}
	tab.Eval(`(() => { const items = [...`+ppDialog+`.querySelectorAll(".ts-player-bar__item")].filter((e) => e.offsetWidth > 0 && !e.classList.contains("ts-player__barmark"));
	  return {Labels: items.map((e) => e.querySelector(".ts-player-bar__label").textContent.trim()), Widths: items.map((e) => e.getBoundingClientRect().width)}; })()`, &r)
	return r.Labels, r.Widths
}

func pkEqual(t *testing.T, what string, widths []float64) {
	t.Helper()
	for _, w := range widths {
		if w < widths[0]-1 || w > widths[0]+1 {
			t.Errorf("%s: unequal bar items %v", what, widths)
			return
		}
	}
}

func TestPlayerKindStationPhone(t *testing.T) {
	tab, w, _ := pkStation(t, 390, 844, "dark")
	var st struct {
		Kind, Head                 string
		LiveH                      float64
		ToggleX                    float64
		Transport                  []string
		LiveShown, ScrubInputShown bool
	}
	tab.Eval(`(() => { const d = `+ppDialog+`, live = d.querySelector(".ts-player__live"), tg = d.querySelector("[data-ts-full-toggle]").getBoundingClientRect();
	  return {Kind: d.dataset.tsKind, Head: d.querySelector(".ts-player__chapter").textContent, LiveH: live.clientHeight, ToggleX: tg.x + tg.width / 2, LiveShown: live.offsetHeight > 0,
	    Transport: [...d.querySelectorAll(".ts-player__transport > button")].filter((b) => b.offsetWidth > 0).map((b) => b.getAttribute("data-ts-full-toggle") !== null ? "toggle" : "other")}; })()`, &st)
	if st.Kind != "radio" {
		t.Errorf("data-ts-kind = %q, want radio", st.Kind)
	}
	if shown := pkShown(tab, ".ts-scrub__input", ".ts-scrub__times", ".ts-player__ruler", "[data-ts-open-speed]", "[data-ts-full-back]", "[data-ts-full-forward]", "[data-ts-full-prev]", "[data-ts-full-next]"); len(shown) > 0 {
		for sel, on := range shown {
			if on {
				t.Errorf("drawn for a station: %s", sel)
			}
		}
	}
	if len(st.Transport) != 1 || st.Transport[0] != "toggle" {
		t.Errorf("transport: %v, want only the toggle", st.Transport)
	}
	if st.ToggleX < 194 || st.ToggleX > 196 {
		t.Errorf("toggle centre x = %.1f, want 195", st.ToggleX)
	}
	if strings.TrimSpace(st.Head) != "Live" {
		t.Errorf("header %q, want Live", st.Head)
	}
	if !st.LiveShown || st.LiveH < 55 || st.LiveH > 57 {
		t.Errorf("live line shown %v, height %.1f, want 56", st.LiveShown, st.LiveH)
	}
	labels, widths := pkBar(tab)
	if len(labels) != 2 || labels[0] != "Sleep" || labels[1] != "Radio" {
		t.Errorf("station bar: %v, want Sleep and Radio", labels)
	}
	pkEqual(t, "station", widths)

	// Sleep still works: 15 minutes shows the time left, and the fast-forwarded timer stops the stream.
	playerClickSel(t, tab, "[data-ts-open-sleep]")
	tab.WaitFor(`!document.querySelector("#player-full [data-ts-sleep-sheet]").hidden`, 3*time.Second)
	playerClickSel(t, tab, `[data-ts-sleep-choice="15"]`)
	tab.WaitFor(`!document.querySelector("#player-full [data-ts-full-sleep-left]").hidden`, 3*time.Second)
	var left string
	tab.Eval(`document.querySelector("#player-full [data-ts-full-sleep-left]").textContent`, &left)
	if !regexp.MustCompile(`^\d+:\d\d$`).MatchString(left) {
		t.Errorf("sleep label %q", left)
	}
	tab.Eval(`window.__ticked = false; document.addEventListener("ts:player:tick", () => { window.__ticked = true; }, { once: true });`, nil)
	tab.WaitFor(`window.__ticked`, 10*time.Second)
	tab.Eval(`window.tsPlayerFull.debug.advanceSleep(16 * 60000)`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 10*time.Second)
	waitFor(t, "the stream to be let go after sleep", func() bool { return w.fx.active.Load() == 0 })
}

func TestPlayerKindStationMenuSafe(t *testing.T) {
	tab, _, id := pkStation(t, 390, 844, "dark")
	var r struct {
		Bad   []string
		Shown map[string]bool
	}
	tab.Eval(`(() => { const d = `+ppDialog+`, bad = [];
	  for (const e of d.querySelectorAll("[href], [action]")) { const v = e.getAttribute("href") || e.getAttribute("action"); if (v.includes("/books/") || v.includes("/hx/books/") || v.includes(`+cdp.Quote("/episodes/")+`)) bad.push(v); }
	  return {Bad: bad, Shown: {}}; })()`, &r)
	if len(r.Bad) > 0 {
		t.Errorf("addresses of another kind for station %s: %v", id, r.Bad)
	}
	for sel, on := range pkShown(tab, "[data-ts-full-book]", "[data-ts-full-finish]", "[data-ts-eyes-free-open]", ".ts-player__menuqueue", "[data-ts-full-show]") {
		if on {
			t.Errorf("drawn for a station: %s", sel)
		}
	}
	var hidden bool
	tab.Eval(ppDialog+`.querySelector("[data-ts-full-show]").hidden`, &hidden)
	if !hidden {
		t.Error("Go to show is not hidden for a station")
	}
}

func TestPlayerKindEpisodeChapters(t *testing.T) {
	tab, w, _ := pkEpisode(t, true, 390, 844, "dark")
	var r struct {
		Kind, Chapters, ShowText, ShowHref string
		Transport, Segs                    int
	}
	tab.Eval(`(() => { const d = `+ppDialog+`, s = d.querySelector("[data-ts-full-show]");
	  return {Kind: d.dataset.tsKind, Chapters: d.dataset.tsChapters, ShowText: s.textContent, ShowHref: s.getAttribute("href"),
	    Transport: [...d.querySelectorAll(".ts-player__transport > button")].filter((b) => b.offsetWidth > 0).length, Segs: d.querySelectorAll(".ts-player__seg").length}; })()`, &r)
	if r.Kind != "episode" || r.Chapters != "many" {
		t.Errorf("kind %q chapters %q", r.Kind, r.Chapters)
	}
	if r.Transport != 5 || r.Segs != 3 {
		t.Errorf("transport buttons %d, segments %d, want 5 and 3", r.Transport, r.Segs)
	}
	if labels, widths := pkBar(tab); strings.Join(labels, ",") != "Speed,Sleep,Chapters" {
		t.Errorf("episode bar: %v", labels)
	} else {
		pkEqual(t, "episode", widths)
	}
	if strings.TrimSpace(r.ShowText) != "Go to show" || r.ShowHref != "/podcasts/shows/"+w.show {
		t.Errorf("menu show item: %q -> %q", r.ShowText, r.ShowHref)
	}
}

func TestPlayerKindEpisodeWhole(t *testing.T) {
	for _, size := range [][2]int{{390, 844}, {1440, 900}} {
		tab, _, _ := pkEpisode(t, false, size[0], size[1], "dark")
		what := fmt.Sprintf("%dx%d", size[0], size[1])
		shown := pkShown(tab, ".ts-player__ruler", ".ts-player__booklabel", "[data-ts-full-prev]", "[data-ts-full-next]")
		for sel, on := range shown {
			// the ruler and its label are a phone piece; the chapter buttons are hidden at every width
			if on && (size[0] < 768 || strings.Contains(sel, "prev") || strings.Contains(sel, "next")) {
				t.Errorf("%s: drawn for an episode without chapters: %s", what, sel)
			}
		}
		var r struct{ Kind, Chapters, Head string }
		tab.Eval(`(() => { const d = `+ppDialog+`; return {Kind: d.dataset.tsKind, Chapters: d.dataset.tsChapters, Head: d.querySelector(".ts-player__chapter").textContent}; })()`, &r)
		if r.Kind != "episode" || r.Chapters != "one" {
			t.Errorf("%s: kind %q chapters %q", what, r.Kind, r.Chapters)
		}
		if size[0] < 768 {
			if strings.TrimSpace(r.Head) != "Episode" {
				t.Errorf("%s: header %q, want Episode", what, r.Head)
			}
			if labels, widths := pkBar(tab); strings.Join(labels, ",") != "Speed,Sleep" {
				t.Errorf("%s: bar %v, want Speed and Sleep", what, labels)
			} else {
				pkEqual(t, what, widths)
			}
		}
		tab.Close()
	}
}

func TestPlayerKindEpisodeNoBookmark(t *testing.T) {
	tab, _, _ := pkEpisode(t, false, 390, 844, "dark")
	tab.Eval(`window.__bookmarkCalls = []; const f = window.fetch; window.fetch = (u, o) => { window.__bookmarkCalls.push(String(u)); return f(u, o); };`, nil)
	fullpKey(t, tab, "b", 66)
	time.Sleep(600 * time.Millisecond)
	var r struct {
		Calls  []string
		Toasts int
	}
	tab.Eval(`({Calls: window.__bookmarkCalls.filter((u) => u.includes("/bookmarks")), Toasts: `+ppDialog+`.querySelector("[data-ts-full-toasts]").children.length})`, &r)
	if len(r.Calls) > 0 || r.Toasts > 0 {
		t.Errorf("B on an episode: requests %v, toasts %d", r.Calls, r.Toasts)
	}
	playerClickSel(t, tab, "[data-ts-open-speed]")
	tab.WaitFor(`!document.querySelector("#player-full [data-ts-speed-sheet]").hidden`, 3*time.Second)
	var s struct {
		Text       string
		UsualShown bool
	}
	tab.Eval(`({Text: `+ppDialog+`.querySelector("[data-ts-speed-for]").textContent, UsualShown: `+ppDialog+`.querySelector("[data-ts-speed-usual]").offsetWidth > 0})`, &s)
	if s.Text != "This speed is for this show. Other shows keep your usual speed." || s.UsualShown {
		t.Errorf("speed sheet: %q, usual button shown %v", s.Text, s.UsualShown)
	}
}

// pkProbe measures the phone column: the auto gaps between the blocks that draw, and whether anything scrolls or overlaps.
const pkProbe = `(() => { const d = ` + ppDialog + `, vis = (e) => e && e.getBoundingClientRect().height > 0, body = d.querySelector(".ts-player__body");
  const r = (s) => d.querySelector(s).getBoundingClientRect(), bt = (s) => parseFloat(getComputedStyle(d.querySelector(s)).borderTopWidth);
  const wave = vis(d.querySelector('[data-ts-scrub="full"]')) ? '[data-ts-scrub="full"]' : ".ts-player__live";
  const chain = [{n: "author", top: r(".ts-player__author").top, bottom: r(".ts-player__author").bottom}];
  const add = (n, sel, auto, bottomExtra) => { const e = d.querySelector(sel); if (!vis(e)) return; const b = e.getBoundingClientRect();
    chain.push({n: n, top: b.top + (auto ? bt(sel) : 0), bottom: bottomExtra === undefined ? b.bottom : b.top + bottomExtra, auto: auto}); };
  add("wave", wave, true); add("times", ".ts-scrub__times", false); add("label", ".ts-player__booklabel", true); add("segs", "[data-ts-ruler-segs]", false, 9);
  add("transport", ".ts-player__transport", true); add("bar", ".ts-player__bar", true);
  const gaps = [], overlaps = [];
  for (let i = 1; i < chain.length; i++) { if (chain[i].top < chain[i - 1].bottom - 0.5) overlaps.push(chain[i].n); if (chain[i].auto) gaps.push({n: chain[i].n, g: chain[i].top - chain[i - 1].bottom}); }
  return {Gaps: gaps, Overlaps: overlaps, Scrolls: body.scrollHeight > body.clientHeight + 1, Bottom: innerHeight - r(".ts-player__bar").bottom}; })()`

func pkCheckSpacing(t *testing.T, what string, tab *cdp.Tab) {
	t.Helper()
	var s struct {
		Gaps []struct {
			N string
			G float64
		}
		Overlaps []string
		Scrolls  bool
		Bottom   float64
	}
	tab.Eval(pkProbe, &s)
	t.Logf("%s: gaps %+v bottom %.1f", what, s.Gaps, s.Bottom)
	if len(s.Gaps) < 3 {
		t.Fatalf("%s: only %d auto gaps", what, len(s.Gaps))
	}
	first := s.Gaps[0].G
	for i, g := range s.Gaps[:len(s.Gaps)-1] {
		if g.G < 7.5 || g.G < first-1 || g.G > first+1 {
			t.Errorf("%s: gap %d (%s) = %.1f px (first %.1f), want equal and at least 7.5", what, i+1, g.N, g.G, first)
		}
	}
	if last := s.Gaps[len(s.Gaps)-1]; last.G < first+3 || last.G > first+5 {
		t.Errorf("%s: gap before the bar %.1f px, want the first gap %.1f + 4", what, last.G, first)
	}
	if s.Scrolls || len(s.Overlaps) > 0 {
		t.Errorf("%s: body scrolls %v, overlaps %v", what, s.Scrolls, s.Overlaps)
	}
	if s.Bottom < 11 || s.Bottom > 13 {
		t.Errorf("%s: the bar ends %.1f px above the screen edge, want 12", what, s.Bottom)
	}
}

func TestPlayerKindsNoDeadBand(t *testing.T) {
	sizes := [][2]int{{360, 640}, {390, 844}, {430, 932}}
	for _, size := range sizes {
		suffix := fmt.Sprintf("%dx%d", size[0], size[1])
		tab, _, _ := pkEpisode(t, true, size[0], size[1], "dark")
		pkCheckSpacing(t, "episode with chapters "+suffix, tab)
		tab.Close()
		tab, _, _ = pkEpisode(t, false, size[0], size[1], "dark")
		pkCheckSpacing(t, "episode whole "+suffix, tab)
		tab.Close()
		tab, _, _ = pkStation(t, size[0], size[1], "dark")
		pkCheckSpacing(t, "station "+suffix, tab)
		tab.Close()
	}
	e := playerApp(t)
	ppChapters(t, e, 12)
	for _, size := range sizes {
		tab := ppOpen(t, e, size[0], size[1], "dark")
		pkCheckSpacing(t, fmt.Sprintf("book %dx%d", size[0], size[1]), tab)
		tab.Close()
	}
}

func TestPlayerKindsThemes(t *testing.T) {
	probe := `(() => { const d = ` + ppDialog + `, cs = (e) => getComputedStyle(e);
	  const c = (v) => { const p = document.createElement("i"); p.style.color = v; d.appendChild(p); const x = getComputedStyle(p).color; p.remove(); return x; };
	  const a = d.querySelector(".ts-player__author"); a.querySelector("[data-ts-full-author]").textContent = "A very long line of the track that is playing now, by an artist with a long name, from an album with an even longer name";
	  const lh = parseFloat(cs(a).lineHeight);
	  return {Ground: cs(d).backgroundColor, Mark: c("var(--ts-mark)"), Head: cs(d.querySelector(".ts-player__chapter")).color, AuthorH: a.getBoundingClientRect().height, LineH: lh,
	    Ellipsis: cs(a).textOverflow === "ellipsis" && cs(a).whiteSpace === "nowrap"}; })()`
	type colours struct {
		Ground, Mark, Head string
		AuthorH, LineH     float64
		Ellipsis           bool
	}
	for _, kind := range []string{"episode", "station"} {
		var tab *cdp.Tab
		if kind == "episode" {
			tab, _, _ = pkEpisode(t, false, 390, 844, "dark")
		} else {
			tab, _, _ = pkStation(t, 390, 844, "dark")
		}
		for _, theme := range themes {
			pkTheme(tab, theme)
			var r colours
			tab.Eval(probe, &r)
			if x := pnrRatio(r.Mark, r.Ground); x < 3 {
				t.Errorf("%s %s: --ts-mark contrast %.2f < 3", kind, theme, x)
			}
			if kind == "station" {
				if x := pnrRatio(r.Head, r.Ground); x < 4.5 {
					t.Errorf("%s: Live contrast %.2f < 4.5", theme, x)
				}
				if !r.Ellipsis || r.AuthorH > r.LineH*1.5 {
					t.Errorf("%s: the now-playing line is %.1f px tall (line %.1f), ellipsis %v", theme, r.AuthorH, r.LineH, r.Ellipsis)
				}
			}
		}
		tab.Close()
	}
}

func TestPlayerKindBookUnchanged(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	tab := ppOpen(t, e, 390, 844, "dark")
	var kind string
	tab.Eval(ppDialog+`.dataset.tsKind`, &kind)
	if kind != "book" {
		t.Errorf("data-ts-kind = %q, want book", kind)
	}
	tab.Eval(ppDialog+`.querySelector("[data-ts-full-read]").hidden = false`, nil)
	labels, _ := pkBar(tab)
	if strings.Join(labels, ",") != "Speed,Sleep,Chapters,Read along" {
		t.Errorf("book bar: %v", labels)
	}
	if on := pkShown(tab, ".ts-player__live", "[data-ts-full-radio]", "[data-ts-full-show]"); on[".ts-player__live"] || on["[data-ts-full-radio]"] || on["[data-ts-full-show]"] {
		t.Errorf("book shows a piece of another kind: %v", on)
	}
}

// TestPlayerKindsCaptures writes the phone player of each kind when TS_KIND_SHOTS names a folder, for a look at the layout.
func TestPlayerKindsCaptures(t *testing.T) {
	dir := os.Getenv("TS_KIND_SHOTS")
	if dir == "" {
		t.Skip("TS_KIND_SHOTS names a folder for the screenshots")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	e := playerApp(t)
	ppChapters(t, e, 14)
	tab := ppOpen(t, e, 390, 844, "dark")
	ppPause(t, tab, 4)
	tab.Screenshot(filepath.Join(dir, "kind-book-dark-390x844.png"))
	tab.Close()
	tab, _, _ = pkEpisode(t, true, 390, 844, "dark")
	tab.Screenshot(filepath.Join(dir, "kind-episode-chapters-dark-390x844.png"))
	tab.Close()
	tab, _, _ = pkEpisode(t, false, 390, 844, "dark")
	tab.Screenshot(filepath.Join(dir, "kind-episode-whole-dark-390x844.png"))
	tab.Close()
	tab, _, _ = pkStation(t, 390, 844, "dark")
	tab.Screenshot(filepath.Join(dir, "kind-station-dark-390x844.png"))
	tab.Close()
}
