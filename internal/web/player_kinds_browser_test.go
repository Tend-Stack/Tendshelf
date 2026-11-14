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
	return pkEpisodeNotes(t, chapters, "", width, height, theme)
}

// pkEpisodeNotes is pkEpisode for an episode whose show notes are the given HTML (empty: none).
func pkEpisodeNotes(t *testing.T, chapters bool, notes string, width, height int, theme string) (tab *cdp.Tab, w *podWorld, base string) {
	t.Helper()
	b := chromium(t)
	w = podWavWorld(t)
	if notes != "" {
		if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET notes_html = ? WHERE id = ?`, notes, w.eps[0]); err != nil {
			t.Fatal(err)
		}
	}
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
	for _, size := range [][2]int{{390, 844}, {1024, 768}} {
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

const pkNotes = `<p>Notes for the episode, with <a href="https://example.com/e">a link</a>.</p>`

// pkNotesState reads the notes sheet: whether it is open, its text, the failure line, what has focus, and any repeated id.
const pkNotesState = `(() => { const d = ` + ppDialog + `, wrap = d.querySelector("[data-ts-notes-sheet]"), a = document.activeElement;
  const ids = {}, dup = []; for (const e of document.querySelectorAll("[id]")) { if (ids[e.id]) dup.push(e.id); ids[e.id] = 1; }
  return {Open: !wrap.hidden, Text: d.querySelector("[data-ts-notes-body]").textContent.trim(), Focus: a && a.hasAttribute("data-ts-sheet-close") && wrap.contains(a),
    OnOpener: a === d.querySelector("[data-ts-open-notes]"), ItemShown: d.querySelector("[data-ts-open-notes]").offsetWidth > 0, Dup: dup}; })()`

type pkNotesView struct {
	Open, Focus, OnOpener, ItemShown bool
	Text                             string
	Dup                              []string
}

func pkReadNotes(tab *cdp.Tab) (v pkNotesView) {
	tab.Eval(pkNotesState, &v)
	return v
}

// TestPlayerKindEpisodeNotes: the Show notes item opens a sheet with the notes (fetched from the episode panel), focus lands on
// its close button, Escape gives focus back, no id appears twice, an episode without notes has no item, and a failed fetch shows
// a plain line that the next open retries. It runs on the phone and on a tablet width (at 1280 and up the bar is hidden and the Now playing panel carries the notes).
func TestPlayerKindEpisodeNotes(t *testing.T) {
	for _, size := range []struct{ w, h int }{{390, 844}, {1024, 768}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			tab, _, _ := pkEpisodeNotes(t, false, pkNotes, size.w, size.h, "dark")
			v := pkReadNotes(tab)
			if !v.ItemShown {
				t.Fatalf("the Show notes item is not drawn")
			}
			// The first ask fails with a 500, the second goes through.
			tab.Eval(`window.__failNotes = true; const f = window.fetch; window.fetch = (u, o) => String(u).includes("episode-panel") && window.__failNotes ? Promise.resolve(new Response("", {status: 500})) : f(u, o);`, nil)
			playerClickSel(t, tab, "#player-full [data-ts-open-notes]")
			tab.WaitFor(`!`+ppDialog+`.querySelector("[data-ts-notes-sheet]").hidden && `+ppDialog+`.querySelector("[data-ts-notes-body]").textContent.includes("did not load")`, 5*time.Second)
			if v = pkReadNotes(tab); v.Text != "The show notes did not load. Try again in a moment." {
				t.Fatalf("failure line: %q", v.Text)
			}
			fullpKey(t, tab, "Escape", 27)
			tab.WaitFor(ppDialog+`.querySelector("[data-ts-notes-sheet]").hidden`, 3*time.Second)
			tab.Eval(`window.__failNotes = false`, nil)
			playerClickSel(t, tab, "#player-full [data-ts-open-notes]")
			tab.WaitFor(ppDialog+`.querySelector("[data-ts-notes-body]").textContent.includes("Notes for the episode")`, 5*time.Second)
			time.Sleep(200 * time.Millisecond)
			v = pkReadNotes(tab)
			if !v.Open || !strings.Contains(v.Text, "Notes for the episode, with a link.") {
				t.Errorf("sheet after retry: open %v text %q", v.Open, v.Text)
			}
			if !v.Focus {
				t.Errorf("focus is not on the sheet's close button")
			}
			if len(v.Dup) > 0 {
				t.Errorf("repeated ids in the document: %v", v.Dup)
			}
			fullpKey(t, tab, "Escape", 27)
			tab.WaitFor(ppDialog+`.querySelector("[data-ts-notes-sheet]").hidden`, 3*time.Second)
			time.Sleep(200 * time.Millisecond)
			if v = pkReadNotes(tab); v.Open || !v.OnOpener {
				t.Errorf("after Escape: open %v, focus back on the item %v", v.Open, v.OnOpener)
			}
			tab.Close()
		})
	}
	tab, _, _ := pkEpisode(t, false, 390, 844, "dark")
	defer tab.Close()
	if v := pkReadNotes(tab); v.ItemShown {
		t.Errorf("an episode without notes still shows the Show notes item")
	}
}

// TestPlayerKindEpisodeSpeedSaved: a speed chosen in the player is saved to the episode's show (the field speed only), and a
// reload of the episode starts at it. A book's speed keeps its own route (TestFullPlayerBrowserSpeed).
func TestPlayerKindEpisodeSpeedSaved(t *testing.T) {
	tab, w, _ := pkEpisode(t, false, 390, 844, "dark")
	defer tab.Close()
	if body := w.get("/hx/player/episode/"+w.eps[0], w.ben).Body.String(); !strings.Contains(body, `data-speed="1.00"`) {
		t.Fatalf("start speed is not 1.00: %s", body)
	}
	tab.Eval(`window.__posts = []; const f = window.fetch; window.fetch = (u, o) => { if (o && o.method === "POST") window.__posts.push([String(u), String(o.body)]); return f(u, o); };`, nil)
	playerClickSel(t, tab, "[data-ts-open-speed]")
	tab.WaitFor(`!`+ppDialog+`.querySelector("[data-ts-speed-sheet]").hidden`, 3*time.Second)
	playerClickSel(t, tab, `#player-full [data-ts-speed-choice="1.5"]`)
	tab.WaitFor(`window.__posts.some((p) => p[0].endsWith("/settings"))`, 5*time.Second)
	var posts [][]string
	tab.Eval(`window.__posts`, &posts)
	var got []string
	for _, p := range posts {
		if strings.HasSuffix(p[0], "/settings") {
			got = p
		}
		if strings.Contains(p[0], "/hx/books/") {
			t.Errorf("an episode posted to a book route: %v", p)
		}
	}
	if got == nil || got[0] != "/podcasts/shows/"+w.show+"/settings" || got[1] != "speed=1.50" {
		t.Errorf("settings post: %v", got)
	}
	time.Sleep(300 * time.Millisecond)
	if body := w.get("/hx/player/episode/"+w.eps[0], w.ben).Body.String(); !strings.Contains(body, `data-speed="1.50"`) {
		t.Errorf("a reload does not play at 1.5: %s", body)
	}
}

// TestPodcastNotesStyleMoved: the show notes keep their 17 px text on the episode page after their rules moved to now-panel.css.
func TestPodcastNotesStyleMoved(t *testing.T) {
	b := chromium(t)
	w := podWavWorld(t)
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET notes_html = ? WHERE id = ?`, pkNotes, w.eps[0]); err != nil {
		t.Fatal(err)
	}
	base, _ := podPlayServer(t, w)
	tab := b.NewTab(1440, 900, false, false)
	defer tab.Close()
	tab.Navigate(base + "/podcasts/episodes/" + w.eps[0])
	tab.WaitFor(`document.querySelector(".ts-podnotes") !== null`, 10*time.Second)
	var size string
	tab.Eval(`getComputedStyle(document.querySelector(".ts-podnotes")).fontSize`, &size)
	if size != "17px" {
		t.Errorf(".ts-podnotes font-size %q, want 17px", size)
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
	tab, _, _ = pkEpisodeNotes(t, true, pkNotes, 390, 844, "dark")
	playerClickSel(t, tab, "#player-full [data-ts-open-notes]")
	tab.WaitFor(ppDialog+`.querySelector("[data-ts-notes-body]").textContent.includes("Notes for the episode")`, 5*time.Second)
	time.Sleep(500 * time.Millisecond)
	tab.Screenshot(filepath.Join(dir, "kind-episode-notes-dark-390x844.png"))
	tab.Close()
}
