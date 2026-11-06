package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
)

const sessionURI = "http://127.0.0.1:9/library"

// TestDeskStateLimits: the page's words are data. Oversize messages are refused, long words are cut
// to 200 runes, control characters go, and markup is escaped.
func TestDeskStateLimits(t *testing.T) {
	big, _ := json.Marshal(nowState{Title: strings.Repeat("a", maxStateBytes)})
	if _, ok := parseState(string(big)); ok {
		t.Error("a message over 1,024 bytes was accepted")
	}
	for _, bad := range []string{"", "x", "[]", `{"title":1}`, `{"playing":"yes"}`} {
		if _, ok := parseState(bad); ok {
			t.Errorf("%q was accepted", bad)
		}
	}

	long, _ := json.Marshal(nowState{Title: strings.Repeat("é", 400)})
	if len(long) <= maxStateBytes { // 400 two-byte runes plus the key: under the byte cap, over the rune cap
		s, ok := parseState(string(long))
		if !ok || len([]rune(s.Title)) != maxStateRunes {
			t.Errorf("title runes = %d, ok %v, want %d", len([]rune(s.Title)), ok, maxStateRunes)
		}
	}

	in, _ := json.Marshal(nowState{Title: "A\x00B\x1b[31m\u0085\nC <b>x</b> & \"q\"", Chapter: "<script>", Theme: "pink", Stop: "boom"})
	s, ok := parseState(string(in))
	if !ok {
		t.Fatal("a valid message was refused")
	}
	if s.Title != "AB[31mC &lt;b&gt;x&lt;/b&gt; &amp; \"q\"" {
		t.Errorf("title = %q", s.Title)
	}
	if s.Chapter != "&lt;script&gt;" {
		t.Errorf("chapter = %q", s.Chapter)
	}
	if s.Theme != "" || s.Stop != "" {
		t.Errorf("theme %q and stop %q must be cleared", s.Theme, s.Stop)
	}
	if got := plainWords(s.Chapter); got != "<script>" {
		t.Errorf("plainWords = %q", got)
	}
}

// TestDeskStateBridge: only the session's pages may say what is playing.
func TestDeskStateBridge(t *testing.T) {
	var got []nowState
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, onState: func(s nowState) { got = append(got, s) }}
	msg := msgState + `{"title":"Book","playing":true,"theme":"sepia","stop":"end"}`
	for _, from := range []string{"", "https://example.com/", pickerOrigin + "wails/", "http://127.0.0.1:99/"} {
		b.Handle(msg, from)
	}
	b.Handle(msgState+"{broken", sessionURI)
	if len(got) != 0 {
		t.Fatalf("refused states were passed on: %v", got)
	}
	b.Handle(msg, sessionURI)
	want := nowState{Title: "Book", Playing: true, Theme: "sepia", Stop: "end"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("got %v, want %v", got, want)
	}
	b.Reset() // a new page has said nothing yet
	if len(got) != 2 || got[1] != (nowState{}) {
		t.Fatalf("after Reset: %v", got)
	}
}

// TestDeskStateMoreVerbs: the More menu's lines run, from the session only.
func TestDeskStateMoreVerbs(t *testing.T) {
	fw := &fakeWindow{}
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, win: fw}
	for _, v := range []string{"zoom-in", "zoom-out", "zoom-reset", "change", "about", "keep-on", "keep-off"} {
		b.Handle(msgWindow+v, sessionURI)
	}
	want := []string{"zoom-in", "zoom-out", "zoom-reset", "change", "about", "keep-on", "keep-off"}
	if !reflect.DeepEqual(fw.calls, want) {
		t.Fatalf("calls = %v", fw.calls)
	}
	fw.calls = nil
	b.Handle(msgWindow+"keep-on", "https://example.com/")
	b.Handle(msgWindow+"keep", sessionURI)
	if len(fw.calls) != 0 {
		t.Fatalf("refused verbs ran %v", fw.calls)
	}
}

// TestDeskStateKeepOnClose: closing hides only when all four conditions hold.
func TestDeskStateKeepOnClose(t *testing.T) {
	for i := 0; i < 16; i++ {
		keep, tray, quitting, playing := i&1 != 0, i&2 != 0, i&4 != 0, i&8 != 0
		want := keep && tray && !quitting && playing
		if got := keepOnClose(keep, tray, quitting, playing); got != want {
			t.Errorf("keepOnClose(%v,%v,%v,%v) = %v", keep, tray, quitting, playing, got)
		}
	}
}

// TestDeskStateSaved: the theme and Keep playing are kept in desktop.json beside the other settings.
func TestDeskStateSaved(t *testing.T) {
	path := t.TempDir() + "/desktop.json"
	if err := saveGround(path, "sepia"); err != nil {
		t.Fatal(err)
	}
	if err := saveKeepPlaying(path, true); err != nil {
		t.Fatal(err)
	}
	if err := saveGround(path, "sepia"); err != nil { // unchanged: no rewrite needed, no harm
		t.Fatal(err)
	}
	cfg, err := loadSaved(path)
	if err != nil || cfg.Ground != "sepia" || !cfg.KeepPlaying {
		t.Fatalf("cfg = %+v, err %v", cfg, err)
	}
}

// loadSaved reads desktop.json for the test above.
func loadSaved(path string) (desktopshell.Config, error) { return desktopshell.Load(path) }
