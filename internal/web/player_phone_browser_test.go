//go:build dev

package web

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The phone full-screen player (handoff 2026-11-14, decision 0042): the wave, the time row, the
// book bar of chapter segments, the transport and the raised settings bar. Header and cover are
// untouched; the wave's drawing and the scrub state are covered here too.

// ppChapters gives the book n chapters of uneven length over its 180 s (weights 1 to 5), so the segments
// differ in width the way a real book's do.
func ppChapters(t *testing.T, e *playerEnv, n int) {
	t.Helper()
	total := 0
	for i := 0; i < n; i++ {
		total += 1 + (i*7)%5
	}
	var chs []library.Chapter
	start := int64(0)
	acc := 0
	for i := 0; i < n; i++ {
		acc += 1 + (i*7)%5
		end := int64(180_000) * int64(acc) / int64(total)
		chs = append(chs, library.Chapter{StartMs: start, EndMs: end, Title: fmt.Sprintf("Chapter %d", i+1), Source: library.ChapterFile})
		start = end
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
}

// ppOpen opens the full player at w x h in a theme with n chapters.
func ppOpen(t *testing.T, e *playerEnv, w, h int, theme string) *cdp.Tab {
	t.Helper()
	tab := fullpOpen(t, e, w, h, true)
	fullpOpenPlayer(t, tab)
	tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
	time.Sleep(150 * time.Millisecond)
	return tab
}

const ppDialog = `document.getElementById("player-full")`

type ppRect struct{ X, Y, W, H, Top, Bottom float64 }

func ppRectOf(tab *cdp.Tab, sel string) ppRect {
	var r ppRect
	tab.Eval(`(() => { const e = `+ppDialog+`.querySelector(`+cdp.Quote(sel)+`); const r = e.getBoundingClientRect(); return {X: r.x, Y: r.y, W: r.width, H: r.height, Top: r.top, Bottom: r.bottom}; })()`, &r)
	return r
}

func TestPlayerPhoneBarsBehaveAsBefore(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	tab := ppOpen(t, e, 390, 844, "dark")

	// The ruler is still the chapter picker.
	var r struct {
		Role, Text string
		Segs       int
		Dur        []float64
		Rects      [][2]float64
		Width      float64
	}
	tab.Eval(`(() => { const d = `+ppDialog+`, ruler = d.querySelector("[data-ts-ruler]"), segs = [...d.querySelectorAll(".ts-player__seg")], box = d.querySelector("[data-ts-ruler-segs]").getBoundingClientRect();
	  const ch = window.tsPlayer.info().chapters, total = window.tsPlayer.info().durationMs;
	  return {Role: ruler.getAttribute("role"), Text: ruler.getAttribute("aria-valuetext"), Segs: segs.length, Dur: ch.map((c) => (c.end - c.start) / total),
	    Rects: segs.map((s) => { const q = s.getBoundingClientRect(); return [q.left - box.left, q.width]; }), Width: box.width}; })()`, &r)
	if r.Role != "slider" || !regexp.MustCompile(`^Chapter \d+ of 12`).MatchString(r.Text) {
		t.Errorf("ruler role %q, value text %q", r.Role, r.Text)
	}
	if r.Segs != 12 {
		t.Fatalf("%d segments, want 12", r.Segs)
	}
	for i, d := range r.Dur {
		want := d * r.Width
		got := r.Rects[i][1]
		if i < 11 {
			got += 2 // the gap is drawn inside the chapter's share
		}
		if got < want-1 || got > want+1 {
			t.Errorf("segment %d is %.1f px wide (with its gap), the chapter's share is %.1f px", i, got, want)
		}
	}

	// A drag on the wave moves the position inside the chapter, as before.
	before := fullpPos(tab)
	var chapter struct{ Start, End int64 }
	tab.Eval(`(() => { const c = window.tsPlayer.info().chapters, p = window.tsPlayer.state().positionMs; const k = c.findLastIndex((x) => x.start <= p); return {Start: c[k].start, End: c[k].end}; })()`, &chapter)
	w := ppRectOf(tab, `[data-ts-scrub="full"]`)
	fullpDrag(t, tab, w.X+w.W*0.5, w.Y+w.H/2, w.X+w.W*0.5-60, w.Y+w.H/2)
	after := fullpPos(tab)
	if after == before || after < chapter.Start || after > chapter.End {
		t.Errorf("a drag on the wave moved the position from %d to %d ms (chapter %d-%d)", before, after, chapter.Start, chapter.End)
	}

	// A tap in the middle of segment 3 starts chapter 3 (a segment is the span chapter-bar.js maps that x to) and offers Undo.
	var tap struct{ X, Y float64 }
	var start int64
	tab.Eval(`(() => { const s = `+ppDialog+`.querySelectorAll(".ts-player__seg")[3].getBoundingClientRect(), r = `+ppDialog+`.querySelector("[data-ts-ruler]").getBoundingClientRect();
	  return {X: s.x + s.width / 2, Y: r.y + r.height / 2}; })()`, &tap)
	tab.Eval(`window.tsPlayer.info().chapters[3].start`, &start)
	playerClickAt(t, tab, tap.X, tap.Y)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - `+fmt.Sprint(start)+`) < 1000`, 3*time.Second)
	tab.WaitFor(`document.querySelector(`+"`"+undoButton+"`"+`) !== null`, 3*time.Second)

	// ArrowRight on the focused ruler moves the choice one chapter.
	tab.Eval(`document.querySelector("#player-full [data-ts-ruler]").focus()`, nil)
	var now string
	tab.Eval(`document.querySelector("#player-full [data-ts-ruler]").getAttribute("aria-valuenow")`, &now)
	fullpKey(t, tab, "ArrowRight", 39)
	var next string
	tab.WaitFor(`document.querySelector("#player-full [data-ts-ruler]").getAttribute("aria-valuenow") !== `+cdp.Quote(now), 2*time.Second)
	tab.Eval(`document.querySelector("#player-full [data-ts-ruler]").getAttribute("aria-valuenow")`, &next)
	if now != "4" || next != "5" {
		t.Errorf("ArrowRight moved the ruler from chapter %s to %s, want 4 to 5", now, next)
	}
}

func TestPlayerPhoneBookRow(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	tab := ppOpen(t, e, 390, 844, "dark")
	var r struct {
		Label, Chapter    string
		Now, Done         int
		Idx               int
		NowH, RestH       float64
		Thin              int
		MarkShown         bool
		LabelRight, Right float64
	}
	probe := `(() => { const d = ` + ppDialog + `, segs = [...d.querySelectorAll(".ts-player__seg")], lab = d.querySelector("[data-ts-full-bookbar-left]");
	  const c = window.tsPlayer.info().chapters, p = window.tsPlayer.state().positionMs, mark = d.querySelector(".ts-player__segmark");
	  const rest = segs.find((s) => !s.classList.contains("is-now") && !s.classList.contains("is-done"));
	  return {Label: lab.textContent, Chapter: d.querySelector("[data-ts-full-chapter]").textContent, Now: segs.filter((s) => s.classList.contains("is-now")).length,
	    Done: segs.filter((s) => s.classList.contains("is-done")).length, Idx: c.findLastIndex((x) => x.start <= p),
	    NowH: segs.find((s) => s.classList.contains("is-now")).getBoundingClientRect().height, RestH: rest ? rest.getBoundingClientRect().height : 0,
	    Thin: segs.filter((s) => s.classList.contains("is-thin")).length, MarkShown: mark.checkVisibility(),
	    LabelRight: lab.getBoundingClientRect().right, Right: d.querySelector("[data-ts-ruler-segs]").getBoundingClientRect().right}; })()`
	tab.Eval(probe, &r)
	if !regexp.MustCompile(`left in book$`).MatchString(r.Label) || regexp.MustCompile(`Chapter`).MatchString(r.Label) {
		t.Errorf("book label %q: want only the time left in the book", r.Label)
	}
	if !regexp.MustCompile(`^Chapter \d+ of 12`).MatchString(r.Chapter) {
		t.Errorf("the header line %q changed", r.Chapter)
	}
	if r.Now != 1 || r.Done != r.Idx || r.Idx == 0 {
		t.Errorf("segments: %d now, %d done, the position is in chapter index %d", r.Now, r.Done, r.Idx)
	}
	if r.NowH != 8 || r.RestH != 4 {
		t.Errorf("segment heights: now %.1f (want 8), upcoming %.1f (want 4)", r.NowH, r.RestH)
	}
	if r.Thin != 0 || r.MarkShown {
		t.Errorf("12 chapters at 390 px: %d thin segments, mark shown %v", r.Thin, r.MarkShown)
	}
	if r.LabelRight > r.Right+0.5 || r.LabelRight < r.Right-0.5 {
		t.Errorf("the label ends at %.1f, the bar at %.1f", r.LabelRight, r.Right)
	}

	// 300 chapters at 360 px: each is about a pixel wide, so they run together and the mark shows the place.
	e2 := playerApp(t)
	ppChapters(t, e2, 300)
	tab2 := ppOpen(t, e2, 360, 740, "dark")
	tab2.Eval(probe, &r)
	if r.Thin < 250 || !r.MarkShown {
		t.Errorf("300 chapters at 360 px: %d thin segments, mark shown %v", r.Thin, r.MarkShown)
	}
	var cls bool
	tab2.Eval(`document.querySelector("#player-full [data-ts-ruler]").classList.contains("is-thin-now")`, &cls)
	if !cls {
		t.Error("the ruler lacks is-thin-now while the current segment is thin")
	}
}

func TestPlayerPhoneSettingsBar(t *testing.T) {
	probe := `(() => { const d = ` + ppDialog + `, bar = d.querySelector(".ts-player__bar"), cs = getComputedStyle(bar);
	  const items = [...bar.querySelectorAll(".ts-player-bar__item")].filter((i) => i.checkVisibility());
	  const raised = (() => { const p = document.createElement("i"); p.style.color = "var(--raised)"; d.appendChild(p); const c = getComputedStyle(p).color; p.remove(); return c; })();
	  return {Items: items.map((i) => { const l = i.querySelector(".ts-player-bar__label"), q = i.getBoundingClientRect(), lq = l.getBoundingClientRect();
	      return {W: q.width, Label: l.textContent.trim(), Cut: l.scrollWidth > l.clientWidth, OneLine: lq.height <= 20 || !l.checkVisibility(), LabelShown: l.checkVisibility()}; }),
	    H: bar.getBoundingClientRect().height - parseFloat(cs.borderTopWidth), Bg: cs.backgroundColor, Raised: raised,
	    Borders: [cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth], Cols: cs.gridTemplateColumns.split(" ").length}; })()`
	type bar struct {
		Items []struct {
			W          float64
			Label      string
			Cut        bool
			OneLine    bool
			LabelShown bool
		}
		H, Cols    float64
		Bg, Raised string
		Borders    []string
	}
	check := func(t *testing.T, tab *cdp.Tab, what string, want int, ebook bool) bar {
		t.Helper()
		if ebook {
			tab.Eval(ppDialog+`.querySelector("[data-ts-full-read]").hidden = false`, nil)
		}
		var b bar
		tab.Eval(probe, &b)
		if len(b.Items) != want {
			t.Fatalf("%s: %d items, want %d", what, len(b.Items), want)
		}
		for i, it := range b.Items {
			if it.W < b.Items[0].W-1 || it.W > b.Items[0].W+1 {
				t.Errorf("%s: item %d is %.1f px wide, item 0 %.1f", what, i, it.W, b.Items[0].W)
			}
			if it.Cut || !it.OneLine {
				t.Errorf("%s: item %q is cut or wraps", what, it.Label)
			}
		}
		if b.Bg != b.Raised || b.H < 63 || b.H > 65 {
			t.Errorf("%s: background %s (want %s), height %.1f (want 64)", what, b.Bg, b.Raised, b.H)
		}
		for _, w := range b.Borders {
			if w != "0px" {
				t.Errorf("%s: the bar has borders %v", what, b.Borders)
			}
		}
		return b
	}
	e := playerApp(t)
	for _, size := range [][2]int{{360, 640}, {390, 844}} {
		tab := ppOpen(t, e, size[0], size[1], "dark")
		what := fmt.Sprintf("%dx%d", size[0], size[1])
		check(t, tab, what+" no ebook", 3, false)
		b := check(t, tab, what+" ebook", 4, true)
		if b.Items[3].Label != "Read along" {
			t.Errorf("%s: the fourth label is %q", what, b.Items[3].Label)
		}
		// A running sleep timer: the moon stays, the label gives way to the time left.
		playerClickSel(t, tab, "[data-ts-open-sleep]")
		tab.WaitFor(`!document.querySelector("#player-full [data-ts-sleep-sheet]").hidden`, 3*time.Second)
		playerClickSel(t, tab, `[data-ts-sleep-choice="15"]`)
		tab.WaitFor(`!document.querySelector("#player-full [data-ts-full-sleep-left]").hidden`, 3*time.Second)
		time.Sleep(450 * time.Millisecond)
		var s struct {
			Value, Label string
			Icon, Label2 bool
		}
		tab.Eval(`(() => { const i = `+ppDialog+`.querySelector("[data-ts-open-sleep]"); return {Value: i.querySelector("[data-ts-full-sleep-left]").textContent,
		  Icon: i.querySelector(".ts-icon").checkVisibility(), Label2: i.querySelector(".ts-player-bar__label").checkVisibility()}; })()`, &s)
		if !regexp.MustCompile(`^\d+:\d\d$`).MatchString(s.Value) || !s.Icon || s.Label2 {
			t.Errorf("%s: sleeping item: value %q, icon visible %v, label visible %v", what, s.Value, s.Icon, s.Label2)
		}
		tab.Close()
	}
	// Larger text: two columns.
	ltSetSize(t, e.a, e.ownerID, true)
	defer ltSetSize(t, e.a, e.ownerID, false)
	tab := ppOpen(t, e, 390, 844, "dark")
	var b bar
	tab.Eval(probe, &b)
	if b.Cols != 2 {
		t.Errorf("large text: %v columns, want 2", b.Cols)
	}
}

func TestPlayerPhoneSpacing(t *testing.T) {
	probe := `(() => { const d = ` + ppDialog + `, q = (s) => d.querySelector(s).getBoundingClientRect(), bt = (s) => parseFloat(getComputedStyle(d.querySelector(s)).borderTopWidth);
	  const wave = q('[data-ts-scrub="full"]'), times = q(".ts-scrub__times"), lab = q(".ts-player__booklabel"), segs = q("[data-ts-ruler-segs]"), tr = q(".ts-player__transport"), bar = q(".ts-player__bar"), body = d.querySelector(".ts-player__body");
	  const boxes = {head: q(".ts-player__head"), cover: q(".ts-player__cover"), title: q(".ts-player__title"), author: q(".ts-player__author"), wave: wave, times: times, label: lab, segs: segs, transport: tr, bar: bar};
	  const content = {wave: wave.top + bt('[data-ts-scrub="full"]'), label: lab.top + bt(".ts-player__booklabel"), transport: tr.top + bt(".ts-player__transport"), bar: bar.top + bt(".ts-player__bar")};
	  return {G1: content.wave - q(".ts-player__author").bottom, G2: content.label - times.bottom, G3: content.transport - (segs.top + 9), G4: content.bar - tr.bottom,
	    Boxes: Object.fromEntries(Object.entries(boxes).map(([k, r]) => [k, [r.top, r.bottom]])), Scrolls: body.scrollHeight > body.clientHeight + 1, Bottom: innerHeight - bar.bottom, Cover: boxes.cover.height}; })()`
	type spacing struct {
		G1, G2, G3, G4 float64
		Boxes          map[string][2]float64
		Scrolls        bool
		Bottom, Cover  float64
	}
	e := playerApp(t)
	ppChapters(t, e, 12)
	for _, size := range [][2]int{{360, 640}, {390, 844}, {430, 932}} {
		what := fmt.Sprintf("%dx%d", size[0], size[1])
		tab := ppOpen(t, e, size[0], size[1], "dark")
		var s spacing
		tab.Eval(probe, &s)
		t.Logf("%s: gaps %.1f %.1f %.1f %.1f, cover %.0f px, bottom %.1f", what, s.G1, s.G2, s.G3, s.G4, s.Cover, s.Bottom)
		for i, g := range []float64{s.G1, s.G2, s.G3} {
			if g < 7.5 || g < s.G1-1 || g > s.G1+1 {
				t.Errorf("%s: gap %d = %.1f px (first gap %.1f), want equal and at least 7.5", what, i+1, g, s.G1)
			}
		}
		if s.G4 < s.G1+3 || s.G4 > s.G1+5 {
			t.Errorf("%s: transport to bar %.1f px, want the first gap %.1f + 4", what, s.G4, s.G1)
		}
		order := []string{"head", "cover", "title", "author", "wave", "times", "label", "segs", "transport", "bar"}
		for i := 1; i < len(order); i++ {
			if s.Boxes[order[i]][0] < s.Boxes[order[i-1]][1]-0.5 {
				t.Errorf("%s: %s (top %.1f) overlaps %s (bottom %.1f)", what, order[i], s.Boxes[order[i]][0], order[i-1], s.Boxes[order[i-1]][1])
			}
		}
		if s.Scrolls {
			t.Errorf("%s: the player body scrolls", what)
		}
		if s.Bottom < 11 || s.Bottom > 13 {
			t.Errorf("%s: the bar ends %.1f px above the screen edge, want 12", what, s.Bottom)
		}
		tab.Close()
	}
}

func TestPlayerPhoneContrast(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	tab := ppOpen(t, e, 390, 844, "dark")
	for _, theme := range themes {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(150 * time.Millisecond)
		var r struct{ Ground, Raised, Rest, Done, Now, Mark, Elapsed, ChLeft, Label, Value, BookLabel, WaveRest, WavePlayed string }
		tab.Eval(`(() => { const d = `+ppDialog+`, segs = [...d.querySelectorAll(".ts-player__seg")], cs = (e) => getComputedStyle(e);
		  const c = (v) => { const p = document.createElement("i"); p.style.color = v; d.appendChild(p); const x = getComputedStyle(p).color; p.remove(); return x; };
		  d.querySelector("[data-ts-full-sleep-left]").hidden = false; d.querySelector("[data-ts-full-sleep-left]").textContent = "9:59";
		  return {Ground: cs(d).backgroundColor, Raised: c("var(--raised)"), Rest: cs(segs[segs.length - 1]).backgroundColor, Done: cs(segs[0]).backgroundColor,
		    Now: cs(segs.find((s) => s.classList.contains("is-now"))).backgroundColor, Mark: c("var(--ts-mark)"),
		    Elapsed: cs(d.querySelector("[data-ts-full-elapsed]")).color, ChLeft: cs(d.querySelector("[data-ts-full-chapter-left]")).color,
		    Label: cs(d.querySelector(".ts-player-bar__label")).color, Value: cs(d.querySelector("[data-ts-full-sleep-left]")).color, BookLabel: cs(d.querySelector(".ts-player__booklabel")).color,
			    WaveRest: cs(d.querySelector(".ts-wave__rest path")).stroke, WavePlayed: cs(d.querySelector(".ts-wave__played path")).stroke}; })()`, &r)
		marks := map[string]string{"unplayed segment": r.Rest, "played segment": r.Done, "current segment": r.Now, "--ts-mark": r.Mark, "wave rest stroke": r.WaveRest, "wave played stroke": r.WavePlayed}
		for name, c := range marks {
			if x := pnrRatio(c, r.Ground); x < 3 {
				t.Errorf("%s: %s contrast %.2f < 3", theme, name, x)
			}
		}
		text := map[string][2]string{"elapsed": {r.Elapsed, r.Ground}, "chapter left": {r.ChLeft, r.Ground}, "book label": {r.BookLabel, r.Ground}, "bar label": {r.Label, r.Raised}}
		for name, c := range text {
			if x := pnrRatio(c[0], c[1]); x < 4.5 {
				t.Errorf("%s: %s contrast %.2f < 4.5", theme, name, x)
			}
		}
		t.Logf("%s: wave rest %.2f played %.2f | rest %.2f done %.2f now %.2f mark %.2f | elapsed %.2f chapter-left %.2f book label %.2f bar label on raised %.2f", theme,
			pnrRatio(r.WaveRest, r.Ground), pnrRatio(r.WavePlayed, r.Ground), pnrRatio(r.Rest, r.Ground), pnrRatio(r.Done, r.Ground), pnrRatio(r.Now, r.Ground), pnrRatio(r.Mark, r.Ground),
			pnrRatio(r.Elapsed, r.Ground), pnrRatio(r.ChLeft, r.Ground), pnrRatio(r.BookLabel, r.Ground), pnrRatio(r.Label, r.Raised))
	}
}

func TestPlayerPhoneThemesLargeText(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	defer ltSetSize(t, e.a, e.ownerID, false)
	for _, large := range []bool{false, true} {
		ltSetSize(t, e.a, e.ownerID, large)
		for _, theme := range themes {
			tab := ppOpen(t, e, 390, 844, theme)
			var r struct {
				Clipped []string
				Overlap []string
			}
			tab.Eval(`(() => { const d = `+ppDialog+`, clipped = [], overlap = [];
			  for (const s of [".ts-player__title", ".ts-scrub__times > span", ".ts-player__booklabel", ".ts-player-bar__label", ".ts-player-bar__value"]) {
			    for (const e of d.querySelectorAll(s)) if (e.checkVisibility() && e.scrollWidth > e.clientWidth + 1 && getComputedStyle(e).display !== "inline") clipped.push(s + ":" + e.textContent.trim());
			  }
			  const names = [".ts-scrub__times", ".ts-player__booklabel", ".ts-player__transport", ".ts-player__bar"], r = names.map((n) => d.querySelector(n).getBoundingClientRect());
			  for (let i = 1; i < r.length; i++) if (r[i].top < r[i - 1].bottom - 0.5) overlap.push(names[i] + " over " + names[i - 1]);
			  return {Clipped: clipped, Overlap: overlap}; })()`, &r)
			if len(r.Clipped) > 0 || len(r.Overlap) > 0 {
				t.Errorf("%s large=%v: clipped %v overlaps %v", theme, large, r.Clipped, r.Overlap)
			}
			tab.Close()
		}
	}
}

func TestPlayerPhoneTabletUnchanged(t *testing.T) {
	e := playerApp(t)
	tab := ppOpen(t, e, 768, 1024, "dark")
	var r struct {
		Clock, Ticks       bool
		Segs, Label, Jump  bool
		ChLeft             bool
		BarBorder, BarFill string
	}
	tab.Eval(`(() => { const d = `+ppDialog+`, v = (s) => d.querySelector(s).checkVisibility(), cs = getComputedStyle(d.querySelector(".ts-player__bar"));
	  return {Clock: v("[data-ts-full-clock]"), Ticks: v(".ts-player__ruler-ticks"), Segs: v(".ts-player__segs"), Label: v(".ts-player__booklabel"), Jump: v(".ts-player__jump"),
	    ChLeft: v(".ts-player__chleft"), BarBorder: cs.borderTopWidth, BarFill: cs.backgroundColor}; })()`, &r)
	if !r.Clock || !r.Ticks || r.Segs || r.Label || r.Jump || r.ChLeft || r.BarBorder != "0px" || r.BarFill != "rgba(0, 0, 0, 0)" {
		t.Errorf("768 px: %+v", r)
	}
}

// ppHold presses on the wave at share a of its width and moves to share b without releasing; the caller releases at the returned point.
func ppHold(t *testing.T, tab *cdp.Tab, a, b float64) (x, y float64) {
	t.Helper()
	w := ppRectOf(tab, `[data-ts-scrub="full"]`)
	y = w.Y + w.H/2
	fullpMouse(t, tab, "mousePressed", w.X+w.W*a, y)
	for i := 1; i <= 10; i++ {
		time.Sleep(30 * time.Millisecond)
		fullpMouse(t, tab, "mouseMoved", w.X+w.W*(a+(b-a)*float64(i)/10), y)
	}
	return w.X + w.W*b, y
}

// ppPause stops at the middle of chapter k and waits for the times to show it.
func ppPause(t *testing.T, tab *cdp.Tab, k int) {
	t.Helper()
	tab.Eval(fmt.Sprintf(`(() => { window.tsPlayer.pause(); const c = window.tsPlayer.info().chapters[%d]; window.tsPlayer.seek(Math.floor((c.start + c.end) / 2)); })()`, k), nil)
	tab.WaitFor(fmt.Sprintf(`window.tsPlayer.state().chapterIdx === %d`, k), 2*time.Second)
	time.Sleep(250 * time.Millisecond)
}

// The scrub state: at rest no clock and no scrim; with the finger down the scrim over the cover shows the target
// time, the row says where the book was, the lower half steps back and the needle grows; letting go seeks; keys do not show the scrim.
func TestPlayerPhoneClock(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	tab := ppOpen(t, e, 390, 844, "dark")
	ppPause(t, tab, 3)
	type state struct {
		ClockShown                                 bool
		Scrim, Transport, Bar, Ruler, Jump, Needle string
		JumpTime, Elapsed, ChLeft                  string
		Was                                        bool
	}
	read := func() state {
		var r state
		tab.Eval(`(() => { const d = `+ppDialog+`, cs = (s) => getComputedStyle(d.querySelector(s)), txt = (s) => d.querySelector(s).textContent;
		  return {ClockShown: d.querySelector("[data-ts-full-clock]").checkVisibility(), Scrim: cs(".ts-player__jump").opacity, Transport: cs(".ts-player__transport").opacity,
		    Bar: cs(".ts-player__bar").opacity, Ruler: cs(".ts-player__ruler").opacity, Jump: cs(".ts-wave__jump").opacity, Needle: cs(".ts-scrub__needle").width,
		    JumpTime: txt("[data-ts-full-jump-time]"), Elapsed: txt("[data-ts-full-elapsed]"), ChLeft: txt("[data-ts-full-chapter-left]"),
		    Was: d.querySelector("[data-ts-full-chapter-left]").classList.contains("is-was")}; })()`, &r)
		return r
	}
	rest := read()
	if rest.ClockShown || rest.Scrim != "0" || rest.Transport != "1" || rest.Jump != "0" || rest.Needle != "4px" || rest.Was {
		t.Errorf("at rest: %+v", rest)
	}
	startAt := rest.Elapsed
	near := func(s string, want float64) bool {
		var v float64
		fmt.Sscanf(s, "%f", &v)
		return v > want-0.02 && v < want+0.02
	}

	// Mid-drag.
	x, y := ppHold(t, tab, 0.5, 0.8)
	tab.WaitFor(`getComputedStyle(`+ppDialog+`.querySelector(".ts-player__jump")).opacity === "1"`, 2*time.Second)
	during := read()
	if !near(during.Transport, 0.45) || !near(during.Bar, 0.45) || !near(during.Ruler, 0.45) || !near(during.Jump, 0.5) || during.Needle != "6px" {
		t.Errorf("mid-drag: %+v", during)
	}
	if during.JumpTime != during.Elapsed || during.JumpTime == startAt {
		t.Errorf("scrim shows %q, row elapsed %q, started at %q", during.JumpTime, during.Elapsed, startAt)
	}
	if during.ChLeft != "was "+startAt || !during.Was {
		t.Errorf("chapter-left slot %q (is-was %v), want %q", during.ChLeft, during.Was, "was "+startAt)
	}

	// After release: the seek happens, everything is back.
	fullpMouse(t, tab, "mouseReleased", x, y)
	tab.WaitFor(`getComputedStyle(`+ppDialog+`.querySelector(".ts-player__jump")).opacity === "0" && !`+ppDialog+`.querySelector("[data-ts-full-chapter-left]").classList.contains("is-was")`, 2*time.Second)
	after := read()
	if after.Transport != "1" || after.Bar != "1" || after.Ruler != "1" || after.Needle != "4px" || after.Jump != "0" {
		t.Errorf("after release: %+v", after)
	}
	var pos int64
	tab.Eval(`window.tsPlayer.state().positionMs`, &pos)
	var c struct{ Start, End int64 }
	tab.Eval(`(() => { const c = window.tsPlayer.info().chapters[3]; return {Start: c.start, End: c.end}; })()`, &c)
	if want := c.Start + (c.End-c.Start)*80/100; pos < want-(c.End-c.Start)/10 || pos > want+(c.End-c.Start)/10 {
		t.Errorf("released at %d ms, want near %d (chapter %d-%d)", pos, want, c.Start, c.End)
	}

	// Keys seek at once, with no scrim.
	tab.Eval(`document.querySelector("#player-full .ts-scrub__input").focus()`, nil)
	fullpKey(t, tab, "ArrowLeft", 37)
	time.Sleep(300 * time.Millisecond)
	if k := read(); k.Scrim != "0" || k.Was {
		t.Errorf("after ArrowLeft: %+v", k)
	}
}

// The wave: 3 px round-capped lines every 5 px, 8 to 46 px tall, level while there is no picture, in three layers.
func TestPlayerPhoneWave(t *testing.T) {
	e := playerApp(t)
	ppChapters(t, e, 12)
	type wave struct {
		N, Layers      int
		Width          float64
		Stroke, Cap    string
		Pitch, Heights []float64
	}
	read := func(tab *cdp.Tab) wave {
		var r wave
		tab.Eval(`(() => { const w = `+ppDialog+`.querySelector('[data-ts-scrub="full"] .ts-wave'), p = w.querySelector(".ts-wave__rest path"), cs = getComputedStyle(p);
		  const xs = [...p.getAttribute("d").matchAll(/M([\d.]+) ([\d.]+)V([\d.]+)/g)];
		  return {N: xs.length, Layers: w.querySelectorAll(":scope > svg").length, Width: w.clientWidth, Stroke: cs.strokeWidth, Cap: cs.strokeLinecap,
		    Pitch: xs.slice(1).map((m, i) => +m[1] - +xs[i][1]), Heights: xs.map((m) => +m[3] - +m[2] + 3)}; })()`, &r)
		return r
	}
	check := func(what string, r wave, level bool) {
		t.Helper()
		if want := int(r.Width+2) / 5; r.N != want || r.Layers != 3 || r.Stroke != "3px" || r.Cap != "round" {
			t.Errorf("%s: %d lines of %s/%s in %d layers on %.0f px, want %d", what, r.N, r.Stroke, r.Cap, r.Layers, r.Width, int(r.Width+2)/5)
		}
		lo, hi := 1e9, 0.0
		for _, h := range r.Heights {
			lo, hi = min(lo, h), max(hi, h)
		}
		if lo < 7.9 || hi > 46.1 {
			t.Errorf("%s: heights %.1f to %.1f, want within 8 to 46", what, lo, hi)
		}
		if level && hi-lo > 0.2 || !level && hi-lo < 12 {
			t.Errorf("%s: heights %.1f to %.1f, level=%v", what, lo, hi, level)
		}
		for _, p := range r.Pitch {
			if p != 5 {
				t.Errorf("%s: pitch %v, want 5", what, p)
				break
			}
		}
	}
	// No peaks yet: level bars, the same count.
	tab := ppOpen(t, e, 390, 844, "dark")
	tab.WaitFor(`!!`+ppDialog+`.querySelector('[data-ts-scrub="full"] .ts-wave__rest')`, 3*time.Second)
	check("flat", read(tab), true)
	tab.Close()

	// With peaks, the picture follows the loudness, at several phone widths.
	pbwWithPeaks(t, e)
	for _, w := range []int{390, 360, 430} {
		tab := ppOpen(t, e, w, 844, "dark")
		ppPause(t, tab, 2)
		tab.WaitFor(`!!`+ppDialog+`.querySelector('[data-ts-scrub="full"] .ts-wave__rest')`, 3*time.Second)
		check(fmt.Sprintf("peaks at %d", w), read(tab), false)
		tab.Close()
	}
}

// TestPlayerPhoneCaptures writes the phone player's screenshots when TS_PHONE_SHOTS names a folder, for a look at the
// layout: a 14-chapter book with a text edition and varied peaks, at rest and with the pointer held mid-drag.
func TestPlayerPhoneCaptures(t *testing.T) {
	dir := os.Getenv("TS_PHONE_SHOTS")
	if dir == "" {
		t.Skip("TS_PHONE_SHOTS names a folder for the screenshots")
	}
	e := playerApp(t)
	ppChapters(t, e, 14)
	ppWritePeaks(t, e)
	for _, c := range []struct {
		name, theme string
		w, h        int
		scrub       bool
	}{
		{"phone-rest-dark-390x844", "dark", 390, 844, false}, {"phone-scrub-dark-390x844", "dark", 390, 844, true},
		{"phone-rest-light-390x844", "light", 390, 844, false}, {"phone-scrub-light-390x844", "light", 390, 844, true},
		{"phone-rest-dark-360x640", "dark", 360, 640, false}, {"phone-rest-dark-430x932", "dark", 430, 932, false},
	} {
		tab := ppOpen(t, e, c.w, c.h, c.theme)
		tab.Eval(ppDialog+`.querySelector("[data-ts-full-read]").hidden = false`, nil)
		ppPause(t, tab, 4)
		if c.scrub {
			x, y := ppHold(t, tab, 0.4, 0.75)
			tab.WaitFor(`getComputedStyle(`+ppDialog+`.querySelector(".ts-player__jump")).opacity === "1"`, 2*time.Second)
			time.Sleep(200 * time.Millisecond)
			tab.Screenshot(filepath.Join(dir, c.name+".png"))
			fullpMouse(t, tab, "mouseReleased", x, y)
		} else {
			tab.Screenshot(filepath.Join(dir, c.name+".png"))
		}
		tab.Close()
	}
}

// ppWritePeaks stores peaks that look like speech for both files: 100 ms bins with a slow swell, word-sized bursts
// and a deterministic scatter, so a chapter a few seconds long still has varied bars.
func ppWritePeaks(t *testing.T, e *playerEnv) {
	t.Helper()
	peaksAttach(t, e.a)
	dir := mediajobs.PeaksDir(e.a.dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range e.files[:2] {
		values := make([]byte, 900)
		for i := range values {
			swell := 0.55 + 0.25*math.Sin(float64(i)/23)
			burst := 0.5 + 0.5*math.Sin(float64(i)/2.7+math.Sin(float64(i)/9)*2)
			scatter := float64((i*37+i*i*11)%100) / 100
			values[i] = byte(25 + 215*math.Min(1, swell*(0.35+0.45*burst)+0.3*scatter*swell))
		}
		b := media.Peaks{BinMs: 100, Values: values}.Encode()
		if err := os.WriteFile(filepath.Join(dir, f.QuickHash+"-v1.peaks"), b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
}
