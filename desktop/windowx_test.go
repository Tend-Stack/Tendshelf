package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
)

// fakeWindow records the title bar verbs.
type fakeWindow struct{ calls []string }

func (f *fakeWindow) Minimise()       { f.calls = append(f.calls, "minimise") }
func (f *fakeWindow) ToggleMaximise() { f.calls = append(f.calls, "maximise") }
func (f *fakeWindow) Close()          { f.calls = append(f.calls, "close") }
func (f *fakeWindow) Quit()           { f.calls = append(f.calls, "quit") }
func (f *fakeWindow) ZoomIn()         { f.calls = append(f.calls, "zoom-in") }
func (f *fakeWindow) ZoomOut()        { f.calls = append(f.calls, "zoom-out") }
func (f *fakeWindow) ZoomReset()      { f.calls = append(f.calls, "zoom-reset") }
func (f *fakeWindow) Change()         { f.calls = append(f.calls, "change") }
func (f *fakeWindow) About()          { f.calls = append(f.calls, "about") }
func (f *fakeWindow) KeepPlaying(on bool) {
	f.calls = append(f.calls, map[bool]string{true: "keep-on", false: "keep-off"}[on])
}

// TestDeskBridgeWindow: the title bar's verbs run from the session's pages and from the picker,
// never from another origin, and an unknown verb does nothing.
func TestDeskBridgeWindow(t *testing.T) {
	fw := &fakeWindow{}
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, win: fw}
	session := "http://127.0.0.1:9/library"
	for _, verb := range []string{"minimise", "maximise", "close", "quit"} {
		b.Handle(msgWindow+verb, session)
	}
	b.Handle(msgWindow+"close", pickerOrigin+"wails/")
	if got := strings.Join(fw.calls, ","); got != "minimise,maximise,close,quit,close" {
		t.Fatalf("calls = %s", got)
	}

	fw.calls = nil
	for _, from := range []string{"", "https://example.com/", "http://127.0.0.1:99/", "http://127.0.0.1:9.evil.example/", "file:///etc/passwd", "wails:/x", "xwails://wails/"} {
		b.Handle(msgWindow+"quit", from)
	}
	for _, verb := range []string{"", "Quit", "minimise ", "destroy", "close; alert(1)", "quit extra"} {
		b.Handle(msgWindow+verb, session)
	}
	b.Handle("tendshelf:window", session) // no space, no verb
	if len(fw.calls) != 0 {
		t.Fatalf("refused messages ran %v", fw.calls)
	}
}

// TestDeskBridgeCommands: Go may send only the fixed list, and each is a plain constant script.
func TestDeskBridgeCommands(t *testing.T) {
	want := []string{"toggle", "back", "forward", "active", "inactive", "maximised", "restored", "tray", "keep", "nokeep"}
	if len(commands) != len(want) {
		t.Fatalf("commands has %d entries, want %d", len(commands), len(want))
	}
	var ran []string
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, exec: func(js string) { ran = append(ran, js) }}
	b.Handle(msgReady, "http://127.0.0.1:9/")
	for _, c := range want {
		if !b.Send(c) {
			t.Errorf("Send(%q) = false", c)
		}
	}
	if b.Send("close") || b.Send(`x"});alert(1);({"`) {
		t.Error("an unlisted command was sent")
	}
	for _, js := range ran {
		if !regexp.MustCompile(`^window\.dispatchEvent\(new CustomEvent\("ts:desktop",\{detail:"[a-z]+"\}\)\)$`).MatchString(js) {
			t.Errorf("script %q is not the fixed shape", js)
		}
	}
}

// TestDeskBridgePickerHears: the picker is told about the window, but never receives a media key.
func TestDeskBridgePickerHears(t *testing.T) {
	var ran []string
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, exec: func(js string) { ran = append(ran, js) }}
	if b.Send("maximised") {
		t.Fatal("sent before any page was ready")
	}
	b.Handle(msgReady, pickerOrigin+"wails/")
	if !b.Send("maximised") || !b.Send("active") {
		t.Fatal("the picker did not hear the window's state")
	}
	if b.Send("toggle") || b.Send("back") || b.Send("forward") {
		t.Fatal("the picker was sent a media key")
	}
	b.Reset()
	if b.Send("restored") {
		t.Fatal("a new page must say ready again")
	}
}

// TestDeskBridgeLoad: a first page from the session or the picker releases the window; others do not.
func TestDeskBridgeLoad(t *testing.T) {
	var n atomic.Int32
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, onLoad: func() { n.Add(1) }}
	b.Handle(msgReady, "https://example.com/")
	b.Handle(msgReady, "")
	if n.Load() != 0 {
		t.Fatal("a foreign page released the window")
	}
	b.Handle(msgReady, "http://127.0.0.1:9/")
	b.Handle(msgReady, pickerOrigin+"wails/")
	if n.Load() != 2 {
		t.Fatalf("loads = %d, want 2", n.Load())
	}
}

// TestDeskWindowStateRoundTrip: the size comes back, other settings survive, and a damaged size is
// brought back into the screen.
func TestDeskWindowStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg", "desktop.json")
	if err := desktopshell.Save(path, desktopshell.Config{Mode: desktopshell.ModePersonal, Port: 41234, KeepPlaying: true, Ground: "sepia"}); err != nil {
		t.Fatal(err)
	}
	tr := newWindowTracker(desktopshell.Window{})
	tr.resized(1100, 700, false)
	tr.resized(1920, 1080, true) // maximised: the size to return to must stay 1100 by 700
	if err := saveWindow(path, tr.snapshot()); err != nil {
		t.Fatal(err)
	}
	got, err := desktopshell.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := desktopshell.Window{Width: 1100, Height: 700, Maximised: true}
	if got.Window != want {
		t.Fatalf("window = %+v, want %+v", got.Window, want)
	}
	if got.Mode != desktopshell.ModePersonal || got.Port != 41234 || !got.KeepPlaying || got.Ground != "sepia" {
		t.Fatalf("saving the size changed other settings: %+v", got)
	}

	for _, c := range []struct {
		name         string
		saved        desktopshell.Window
		sw, sh       int
		wantW, wantH int
	}{
		{"nothing saved", desktopshell.Window{}, 1920, 1080, defaultWidth, defaultHeight},
		{"fits", desktopshell.Window{Width: 1000, Height: 700}, 1920, 1080, 1000, 700},
		{"larger than the screen", desktopshell.Window{Width: 5000, Height: 4000}, 1366, 768, 1366, 768},
		{"too small", desktopshell.Window{Width: 10, Height: 20}, 1920, 1080, minWidth, minHeight},
		{"negative", desktopshell.Window{Width: -5, Height: -5}, 1920, 1080, defaultWidth, defaultHeight},
		{"screen unknown", desktopshell.Window{Width: 1000, Height: 700}, 0, 0, 1000, 700},
		{"small screen", desktopshell.Window{}, 1024, 600, 1024, 600},
	} {
		w, h := fitWindow(c.saved, c.sw, c.sh)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: fitWindow = %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
	}
}

// TestDeskWindowGrounds keeps the window's background equal to --ground of each theme in tokens.css.
func TestDeskWindowGrounds(t *testing.T) {
	css, err := os.ReadFile(filepath.Join("..", "internal", "web", "static", "css", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range grounds {
		block := regexp.MustCompile(`(?s)\[data-theme="` + name + `"\]\s*\{(.*?)\n\}`).FindStringSubmatch(string(css))
		if block == nil {
			t.Fatalf("no [data-theme=%q] block in tokens.css", name)
		}
		m := regexp.MustCompile(`--ground:\s*#([0-9a-fA-F]{6})\s*;`).FindStringSubmatch(block[1])
		if m == nil {
			t.Fatalf("no --ground in the %s theme", name)
		}
		if got := fmt.Sprintf("%02x%02x%02x", c.Red, c.Green, c.Blue); !strings.EqualFold(got, m[1]) {
			t.Errorf("%s: window ground #%s, tokens.css #%s", name, got, m[1])
		}
	}
	if groundColour("") != grounds["dark"] || groundColour("neon") != grounds["dark"] {
		t.Error("an unknown ground must be the dark one")
	}
}

// TestDeskWindowShowFallback: the window shows once, on the first ready, or after the fallback when
// no page ever says so.
func TestDeskWindowShowFallback(t *testing.T) {
	var shown atomic.Int32
	r := newRevealer(func() { shown.Add(1) }, time.Hour)
	r.Ready()
	r.Ready()
	if shown.Load() != 1 {
		t.Fatalf("shown %d times after ready, want 1", shown.Load())
	}

	var late atomic.Int32
	newRevealer(func() { late.Add(1) }, 20*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for late.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if late.Load() != 1 {
		t.Fatalf("the fallback showed the window %d times, want 1", late.Load())
	}

	// A ready after the fallback does not show it twice.
	var both atomic.Int32
	r2 := newRevealer(func() { both.Add(1) }, 10*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	r2.Ready()
	if both.Load() != 1 {
		t.Fatalf("shown %d times, want 1", both.Load())
	}
}

// TestDeskWindowKeys: Ctrl+Q quits now that the menu row is gone on Linux, and the zoom keys stay.
func TestDeskWindowKeys(t *testing.T) {
	var calls []string
	a := menuActions{
		quit:      func() { calls = append(calls, "quit") },
		zoomIn:    func() { calls = append(calls, "in") },
		zoomOut:   func() { calls = append(calls, "out") },
		zoomReset: func() { calls = append(calls, "reset") },
	}
	keys := zoomKeys(a)
	for _, k := range []string{"ctrl+q", "ctrl+plus", "ctrl+=", "ctrl+-", "ctrl+0"} {
		if keys[k] == nil {
			t.Fatalf("no binding for %s", k)
		}
		keys[k](nil)
	}
	if got := strings.Join(calls, ","); got != "quit,in,in,out,reset" {
		t.Fatalf("calls = %s", got)
	}
}
