package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The tray's "Play or pause" and the media keys use the same bridge command, and the page honours
// exactly the commands Go can send.
func TestKeysCommandList(t *testing.T) {
	var ran []string
	b := &Bridge{origin: func() string { return "http://127.0.0.1:1" }, exec: func(js string) { ran = append(ran, js) }}

	if b.Send("toggle") {
		t.Fatal("a command before the page is ready must be dropped")
	}
	b.Handle(msgReady, "http://127.0.0.1:1/")
	for _, name := range []string{"toggle", "back", "forward"} {
		if !b.Send(name) {
			t.Fatalf("%s was not sent", name)
		}
	}
	for _, name := range []string{"", "next", "toggle; alert(1)", "stop"} {
		if b.Send(name) {
			t.Fatalf("unknown command %q was sent", name)
		}
	}
	if len(ran) != 3 {
		t.Fatalf("ran %d scripts, want 3", len(ran))
	}
	for _, js := range ran {
		if !strings.HasPrefix(js, `window.dispatchEvent(new CustomEvent("ts:desktop",{detail:"`) {
			t.Fatalf("unexpected script %q", js)
		}
	}

	// desktop.js must press a button for each of the three commands.
	src, err := os.ReadFile(filepath.Join("..", "internal", "web", "static", "js", "desktop.js"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const buttons = \{([^}]*)\}`).FindSubmatch(src)
	if m == nil {
		t.Fatal("desktop.js has no buttons table")
	}
	var inPage []string
	for _, k := range regexp.MustCompile(`(\w+):\s*"\[data-ts-`).FindAllSubmatch(m[1], -1) {
		inPage = append(inPage, string(k[1]))
	}
	var inGo []string
	// The window and tray commands are for desktop-shell.js (plan X2); desktop.js presses player buttons.
	state := map[string]bool{"active": true, "inactive": true, "maximised": true, "restored": true, "tray": true, "keep": true, "nokeep": true}
	for k := range commands {
		if state[k] {
			continue
		}
		inGo = append(inGo, k)
	}
	sort.Strings(inPage)
	sort.Strings(inGo)
	if !reflect.DeepEqual(inPage, inGo) {
		t.Fatalf("desktop.js handles %v, Go sends %v", inPage, inGo)
	}
}
