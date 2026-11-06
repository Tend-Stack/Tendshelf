package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestDeskNotifyNotReachable: registering the notifications service gives a page nothing. Pages load
// from http(s) and reach Go only through the bridge's fixed messages; Wails' bound services answer on
// its own runtime, which only the picker's wails:// origin and the exit run's fetch check
// (probe plan "now") exercise for real. Here: the bridge ignores everything that is not one of its
// messages, the only registered service is the notifications one, and no code makes a Wails
// scheme reachable from another origin.
func TestDeskNotifyNotReachable(t *testing.T) {
	ran := 0
	b := &Bridge{
		origin:  func() string { return "http://127.0.0.1:9" },
		open:    func(string) error { ran++; return nil },
		win:     &fakeWindow{},
		onState: func(nowState) { ran++ },
		onLoad:  func() { ran++ },
	}
	page := "http://127.0.0.1:9/library"
	for _, m := range []string{
		"wails:runtime:ready", "wails:invoke:{\"service\":\"notifications\"}", "wails:drag",
		`{"object":0,"method":0,"args":{"options":{"id":"x","title":"y"}}}`,
		"notifications.SendNotification", "tendshelf:notify {}", "tendshelf:probe x y", "tendshelf:stat {}", "",
	} {
		b.Handle(m, page)
	}
	if ran != 0 || len(b.win.(*fakeWindow).calls) != 0 {
		t.Fatalf("a message outside the bridge's list did something (%d, %v)", ran, b.win.(*fakeWindow).calls)
	}

	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "application.NewService("); n != 1 || !strings.Contains(string(src), "application.NewService(notes)") {
		t.Errorf("main.go registers %d services, want exactly the notifications one", n)
	}
	files, _ := os.ReadDir(".")
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".go") || strings.HasSuffix(f.Name(), "_test.go") {
			continue
		}
		body, _ := os.ReadFile(f.Name())
		if regexp.MustCompile(`cors_enabled|register_uri_scheme_as_(cors|local|secure)`).Match(body) {
			t.Errorf("%s makes a scheme reachable from other origins", f.Name())
		}
	}
}
