package main

import (
	"reflect"
	"strings"
	"testing"
)

func labelsOf(entries []trayEntry) []string {
	var out []string
	for _, e := range entries {
		if e.divider {
			out = append(out, "-")
		} else {
			out = append(out, e.label)
		}
	}
	return out
}

// TestDeskTrayEntries: nothing playing offers Show and Quit; a book adds its lines, Pause only while
// it plays, and the two skips. Every action line runs its own action.
func TestDeskTrayEntries(t *testing.T) {
	var calls []string
	note := func(s string) func() { return func() { calls = append(calls, s) } }
	a := trayActions{show: note("show"), hide: note("hide"), playPause: note("pause"), back: note("back"), forward: note("forward"), quit: note("quit")}

	idle := trayEntries(a, nowState{})
	if want := []string{"Show Tendshelf", "Quit Tendshelf"}; !reflect.DeepEqual(labelsOf(idle), want) {
		t.Fatalf("idle = %v", labelsOf(idle))
	}

	playing := trayEntries(a, nowState{Title: "A &amp; B", Chapter: "Chapter 3", Left: "2 h left", Playing: true})
	want := []string{"A & B", "Chapter 3 · 2 h left", "-", "Pause", "Skip back", "Skip forward", "-", "Show Tendshelf", "Quit Tendshelf"}
	if !reflect.DeepEqual(labelsOf(playing), want) {
		t.Fatalf("playing = %v", labelsOf(playing))
	}
	if !playing[0].disabled || !playing[1].disabled || playing[3].disabled {
		t.Error("book lines must be disabled and actions enabled")
	}

	paused := labelsOf(trayEntries(a, nowState{Title: "T"}))
	for _, l := range paused {
		if l == "Pause" || strings.HasPrefix(l, "Play") {
			t.Errorf("paused tray offers %q: it never starts a book", l)
		}
	}

	for _, e := range append(idle, playing...) {
		if e.run != nil {
			e.run()
		}
	}
	if want := []string{"show", "quit", "pause", "back", "forward", "show", "quit"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v", calls)
	}

	calls = nil
	toggleWindow(func() bool { return true }, a)
	toggleWindow(func() bool { return false }, a)
	if want := []string{"hide", "show"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("click on the icon: %v, want %v", calls, want)
	}
}

// TestDeskTrayTooltip: the hover text names the book and says when it is paused.
func TestDeskTrayTooltip(t *testing.T) {
	cases := map[string]nowState{
		"Tendshelf":     {},
		"Dune":          {Title: "Dune", Playing: true},
		"Dune (paused)": {Title: "Dune"},
		"Tom & Jerry":   {Title: "Tom &amp; Jerry", Playing: true},
	}
	for want, s := range cases {
		if got := trayTooltip(s); got != want {
			t.Errorf("tooltip(%+v) = %q, want %q", s, got, want)
		}
	}
}

// TestDeskNotifyRules: a notification only when playback stopped for a reason, the window is not in
// front, and it is news.
func TestDeskNotifyRules(t *testing.T) {
	playing := nowState{Title: "Dune", Playing: true}
	ended := nowState{Title: "Dune", Stop: "end"}
	slept := nowState{Title: "Dune", Stop: "sleep"}

	if title, body, ok := noteFor(playing, ended, false); !ok || title != "Dune" || body != noteEnd {
		t.Errorf("end: %q %q %v", title, body, ok)
	}
	if _, body, ok := noteFor(playing, slept, false); !ok || body != noteSleep {
		t.Errorf("sleep: %q %v", body, ok)
	}
	if _, _, ok := noteFor(playing, ended, true); ok {
		t.Error("a notification while the window is in front")
	}
	if _, _, ok := noteFor(ended, ended, false); ok {
		t.Error("the same stop was told twice")
	}
	if _, _, ok := noteFor(playing, nowState{Title: "Dune"}, false); ok {
		t.Error("a pause by hand is not news")
	}
	if _, _, ok := noteFor(playing, nowState{Stop: "end"}, false); ok {
		t.Error("a stop with no book")
	}

	var notes []string
	var drawn, grounds int
	w := &nowWatcher{active: func() bool { return false }, tray: func(nowState) { drawn++ },
		notify: func(title, b string) { notes = append(notes, title+"|"+b) }, ground: func(string) { grounds++ }}
	w.Set(playing)
	if !w.Playing() {
		t.Error("Playing() false")
	}
	w.Set(slept)
	w.Set(slept)
	w.Set(nowState{Title: "Dune", Theme: "light"})
	w.Set(nowState{Title: "Dune", Theme: "light"})
	if len(notes) != 1 || drawn != 5 || grounds != 1 || w.Playing() {
		t.Errorf("notes %v, drawn %d, grounds %d", notes, drawn, grounds)
	}
}
