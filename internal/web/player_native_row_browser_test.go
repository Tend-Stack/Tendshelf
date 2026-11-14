//go:build dev

package web

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Decision 0042: the full player's bottom row is four plain buttons (icon or value over a label),
// and the chapter bar is neutral ink so it does not read as a second copy of the accent waveform.

// pnrProbe measures the row inside the open full player. The Read button is hidden until the book
// has an ebook, so the probe shows it for the measurement.
const pnrProbe = `(() => {
  const d = document.getElementById("player-full");
  d.querySelector("[data-ts-full-read]").hidden = false;
  const bar = d.querySelector(".ts-player-bar");
  const items = ["[data-ts-open-speed]", "[data-ts-open-sleep]", "[data-ts-open-chapters]", "[data-ts-full-read]"].map((s) => {
    const el = d.querySelector(s), r = el.getBoundingClientRect(), l = el.querySelector(".ts-player-bar__label");
    return {W: r.width, H: r.height, Label: l.textContent.trim(), Name: el.getAttribute("aria-label") || l.textContent.trim(),
      Cut: l.scrollWidth > l.clientWidth, Bl: getComputedStyle(el).borderLeftWidth, Bt: getComputedStyle(el).borderTopWidth, Right: r.right};
  });
  const cs = getComputedStyle(bar);
  const probe = document.createElement("i"); probe.style.color = "var(--raised)"; d.appendChild(probe); const raised = getComputedStyle(probe).color; probe.remove();
  return {Raised: raised, Items: items, Border: [cs.borderTopWidth, cs.borderRightWidth, cs.borderBottomWidth, cs.borderLeftWidth],
    Bg: cs.backgroundColor, Overflow: document.documentElement.scrollWidth > window.innerWidth,
    MaxRight: Math.max(...items.map((i) => i.Right)), Vw: window.innerWidth};
})()`

type pnrItem struct {
	W, H, Right float64
	Label, Name string
	Cut         bool
	Bl, Bt      string
}

type pnrResult struct {
	Items    []pnrItem
	Border   []string
	Bg       string
	Raised   string
	Overflow bool
	MaxRight float64
	Vw       float64
}

// pnrCheck checks the row. On a phone (under 768 px, phone handoff 2026-11-14) the row is one raised block with a 12 px
// transparent top border that holds its spacing; elsewhere it is borderless and unfilled as decision 0042 says.
func pnrCheck(t *testing.T, tab *cdp.Tab, what string) {
	t.Helper()
	var r pnrResult
	tab.Eval(pnrProbe, &r)
	phone := r.Vw < 768
	want := []string{"Speed", "Sleep", "Chapters", "Read along"} // one label at every width (phone handoff 2026-11-14)
	if len(r.Items) != 4 {
		t.Fatalf("%s: %d buttons, want 4", what, len(r.Items))
	}
	for i, it := range r.Items {
		if it.W < 44 || it.H < 44 {
			t.Errorf("%s: %s is %.0fx%.0f px, want at least 44x44", what, want[i], it.W, it.H)
		}
		if it.Label != want[i] {
			t.Errorf("%s: label %d = %q, want %q", what, i, it.Label, want[i])
		}
		if it.Cut {
			t.Errorf("%s: %s label is cut off", what, want[i])
		}
		if it.Bl != "0px" || it.Bt != "0px" {
			t.Errorf("%s: %s has a border (%s left, %s top)", what, want[i], it.Bl, it.Bt)
		}
	}
	if r.Items[3].Name != "Read this book" {
		t.Errorf("%s: Read button name = %q", what, r.Items[3].Name)
	}
	edges := r.Border
	if phone {
		edges = r.Border[1:] // the top border is the transparent spacing above the block
	}
	for _, b := range edges {
		if b != "0px" {
			t.Errorf("%s: the row has a border: %v", what, r.Border)
			break
		}
	}
	if phone && r.Bg != r.Raised {
		t.Errorf("%s: the row's background is %s, want --raised %s", what, r.Bg, r.Raised)
	}
	if !phone && r.Bg != "rgba(0, 0, 0, 0)" {
		t.Errorf("%s: the row has a background %s", what, r.Bg)
	}
	if r.Overflow || r.MaxRight > r.Vw+0.5 {
		t.Errorf("%s: the row overflows the viewport (right %.0f of %.0f)", what, r.MaxRight, r.Vw)
	}
}

func TestPlayerNativeRow(t *testing.T) {
	e := playerApp(t)
	shots := os.Getenv("TS_ROW_SHOTS") // optional: a folder for screenshots of the row
	// From 1280 px the side panel hides this row (the desktop bar carries the tools, unchanged), so the
	// normal-size checks run at the widths that show it and 1440 px is checked in large text below.
	for _, w := range []int{390, 768} {
		tab := fullpOpen(t, e, w, 900, true)
		fullpOpenPlayer(t, tab)
		for _, theme := range []string{"dark", "light"} {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			pnrCheck(t, tab, fmt.Sprintf("%d %s", w, theme))
			if shots != "" {
				tab.Screenshot(filepath.Join(shots, fmt.Sprintf("row-%d-%s.png", w, theme)))
			}
		}
	}
	// Large text never uses the side panel, so the row shows at 1440 px too, and keeps every label whole.
	ltSetSize(t, e.a, e.ownerID, true)
	defer ltSetSize(t, e.a, e.ownerID, false)
	for _, w := range []int{390, 1440} {
		for _, theme := range []string{"dark", "light"} {
			tab := fullpOpen(t, e, w, 900, true)
			fullpOpenPlayer(t, tab)
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			pnrCheck(t, tab, fmt.Sprintf("%d %s large text", w, theme))
			tab.Close()
		}
	}
}

func TestPlayerRulerColour(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := fullpOpen(t, e, 390, 900, true)
	fullpOpenPlayer(t, tab)
	for _, theme := range []string{"dark", "light"} {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(100 * time.Millisecond)
		var r struct{ Done, Now, Rest, Ink, Mark, RestToken, Wave string }
		tab.Eval(`(() => {
		  const d = document.getElementById("player-full");
		  const probe = (host, value) => { const p = document.createElement("i"); p.style.color = value; host.appendChild(p); const c = getComputedStyle(p).color; p.remove(); return c; };
		  const segs = [...d.querySelectorAll(".ts-player__seg")], bg = (s) => getComputedStyle(s).backgroundColor;
		  return {Done: bg(segs[0]), Now: bg(segs.find((s) => s.classList.contains("is-now"))), Rest: bg(segs[segs.length - 1]),
		    Ink: probe(d, "var(--ink)"), Mark: probe(d, "var(--ts-mark)"), RestToken: probe(d, "var(--ts-rest)"), Wave: probe(d.querySelector(".ts-scrub"), "var(--scrub-fill)")};
		})()`, &r)
		// Phone: the book bar is segments: played in --ts-mark, the current one --ink, the rest --ts-rest.
		if r.Done != r.Mark || r.Now != r.Ink || r.Rest != r.RestToken {
			t.Errorf("%s: segments done %s now %s rest %s, want mark %s ink %s rest %s", theme, r.Done, r.Now, r.Rest, r.Mark, r.Ink, r.RestToken)
		}
		if r.Now == r.Wave || r.Rest == r.Wave {
			t.Errorf("%s: the current or unplayed segment shares the waveform's colour %s", theme, r.Wave)
		}
	}
}

// At 768 px the ruler keeps decision 0042's ink fill; the segments are for phones.
func TestPlayerRulerColourTablet(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := fullpOpen(t, e, 768, 900, true)
	fullpOpenPlayer(t, tab)
	for _, theme := range []string{"dark", "light"} {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(100 * time.Millisecond)
		var r struct{ Fill, Ink, Wave string }
		tab.Eval(`(() => {
		  const d = document.getElementById("player-full");
		  const probe = (host, value) => { const p = document.createElement("i"); p.style.color = value; host.appendChild(p); const c = getComputedStyle(p).color; p.remove(); return c; };
		  return {Fill: getComputedStyle(d.querySelector(".ts-player__ruler .ts-ruler__fill")).backgroundColor,
		    Ink: probe(d, "var(--ink)"), Wave: probe(d.querySelector(".ts-scrub"), "var(--scrub-fill)")};
		})()`, &r)
		if r.Fill != r.Ink {
			t.Errorf("%s: chapter bar fill %s, want --ink %s", theme, r.Fill, r.Ink)
		}
		if r.Fill == r.Wave {
			t.Errorf("%s: chapter bar and waveform share the colour %s", theme, r.Fill)
		}
	}
}

var pnrNum = regexp.MustCompile(`[\d.]+`)

func pnrLum(c string) float64 {
	n := pnrNum.FindAllString(c, 3)
	var v [3]float64
	for i := range v {
		x, _ := strconv.ParseFloat(n[i], 64)
		x /= 255
		if x <= 0.03928 {
			v[i] = x / 12.92
		} else {
			v[i] = math.Pow((x+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*v[0] + 0.7152*v[1] + 0.0722*v[2]
}

func pnrRatio(a, b string) float64 {
	la, lb := pnrLum(a), pnrLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestPlayerRowContrast: the labels and the accent value are text (4.5:1) and the chapter bar's played
// and unplayed parts are graphics (3:1), against the player's own background, in all four themes.
func TestPlayerRowContrast(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 900, true)
	fullpOpenPlayer(t, tab)
	for _, theme := range themes {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(100 * time.Millisecond)
		var r struct{ Ground, Label, Played, Unplayed, Value string }
		// At 390 px the ruler is segments: the done and the upcoming ones stand for the played and unplayed parts.
		tab.Eval(`(() => {
		  const d = document.getElementById("player-full");
		  const c = (host, v) => { const p = document.createElement("i"); p.style.color = v; host.appendChild(p); const x = getComputedStyle(p).color; p.remove(); return x; };
		  return {Ground: getComputedStyle(d).backgroundColor, Label: getComputedStyle(d.querySelector(".ts-player-bar__label")).color,
		    Played: getComputedStyle(d.querySelector(".ts-player__seg.is-done")).backgroundColor,
		    Unplayed: getComputedStyle([...d.querySelectorAll(".ts-player__seg")].pop()).backgroundColor, Value: c(d, "var(--accent)")};
		})()`, &r)
		t.Logf("%s: label %.2f, played %.2f, unplayed %.2f, accent value %.2f (ground %s)", theme,
			pnrRatio(r.Label, r.Ground), pnrRatio(r.Played, r.Ground), pnrRatio(r.Unplayed, r.Ground), pnrRatio(r.Value, r.Ground), r.Ground)
		if x := pnrRatio(r.Label, r.Ground); x < 4.5 {
			t.Errorf("%s: label contrast %.2f < 4.5", theme, x)
		}
		if x := pnrRatio(r.Value, r.Ground); x < 4.5 {
			t.Errorf("%s: accent value contrast %.2f < 4.5", theme, x)
		}
		for name, c := range map[string]string{"played": r.Played, "unplayed": r.Unplayed} {
			if x := pnrRatio(c, r.Ground); x < 3 {
				t.Errorf("%s: chapter bar %s part contrast %.2f < 3", theme, name, x)
			}
		}
	}
}
