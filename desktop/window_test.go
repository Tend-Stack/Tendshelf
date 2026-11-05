package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/app"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
)

type fakeUI struct {
	mu     sync.Mutex
	loads  []string
	warns  []string
	events []string // "load:<url>" and "stopped" in order, to prove stop-before-switch
}

func (f *fakeUI) Load(u string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads = append(f.loads, u)
	f.events = append(f.events, "load:"+u)
}
func (f *fakeUI) Warn(m string) { f.mu.Lock(); f.warns = append(f.warns, m); f.mu.Unlock() }
func (f *fakeUI) log(e string)  { f.mu.Lock(); f.events = append(f.events, e); f.mu.Unlock() }
func (f *fakeUI) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.loads) == 0 {
		return ""
	}
	return f.loads[len(f.loads)-1]
}

// libraryRun is a stand-in for app.Run: ready at once, then it waits for its context.
func libraryRun(ui *fakeUI, info app.Info, fail error) func(context.Context, app.Config) error {
	return func(ctx context.Context, cfg app.Config) error {
		defer cfg.Listener.Close()
		if fail != nil {
			return fail
		}
		cfg.Ready(info)
		<-ctx.Done()
		ui.log("stopped")
		return nil
	}
}

func newTestSession(t *testing.T, ui UI, run func(context.Context, app.Config) error) *Session {
	t.Helper()
	home := t.TempDir()
	t.Setenv(desktopshell.HomeEnv, home)
	dirs, err := desktopshell.Paths()
	if err != nil {
		t.Fatal(err)
	}
	s := NewSession(dirs, "test", ui)
	s.run = run
	s.stopWait = 5 * time.Second
	s.readyMax = 5 * time.Second
	t.Cleanup(s.Stop)
	return s
}

func TestWindowSessionSwitch(t *testing.T) {
	ui := &fakeUI{}
	s := newTestSession(t, ui, libraryRun(ui, app.Info{SetupURL: "http://localhost:1/setup?code=ab%20c"}, nil))

	if err := s.StartPersonal(); err != nil {
		t.Fatal(err)
	}
	got := ui.last()
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Hostname() != "127.0.0.1" || u.Path != "/" || u.Query().Get("shell") != "desktop" {
		t.Fatalf("personal URL = %q", got)
	}
	if next := u.Query().Get("next"); next != "/setup?code=ab%20c" {
		t.Fatalf("next = %q, want the setup path and query", next)
	}
	if s.Origin() != "http://"+u.Host || s.State().Kind != desktopshell.Idle {
		t.Fatalf("origin %q state %+v", s.Origin(), s.State())
	}
	cfg, _ := desktopshell.Load(s.dirs.File())
	if cfg.Mode != desktopshell.ModePersonal || cfg.Port == 0 {
		t.Fatalf("saved %+v", cfg)
	}

	// Switching to a server stops the library first, then opens the server.
	if err := s.Connect("https://books.example.com"); err != nil {
		t.Fatal(err)
	}
	ui.mu.Lock()
	events := strings.Join(ui.events, " | ")
	ui.mu.Unlock()
	if !strings.Contains(events, "stopped | load:https://books.example.com/?shell=desktop") {
		t.Fatalf("events = %s", events)
	}
	if s.Origin() != "https://books.example.com" {
		t.Fatalf("origin = %q", s.Origin())
	}
	cfg, _ = desktopshell.Load(s.dirs.File())
	if cfg.Mode != desktopshell.ModeConnect || cfg.Server != "https://books.example.com" || cfg.Port == 0 {
		t.Fatalf("saved %+v (the personal port is kept for a return)", cfg)
	}

	// Back to personal on the same port.
	if err := s.StartPersonal(); err != nil {
		t.Fatal(err)
	}
	again, _ := url.Parse(ui.last())
	if again.Host != u.Host {
		t.Fatalf("port changed from %s to %s", u.Host, again.Host)
	}
}

func TestWindowStartFailed(t *testing.T) {
	ui := &fakeUI{}
	cases := []struct {
		name  string
		err   error
		words string
	}{
		{"newer library", db.ErrSchemaTooNew, "This library was made by a newer Tendshelf. Update the app to open it."},
		{"foreign file", db.ErrNotTendshelf, "That folder holds a file that isn't a Tendshelf library, so the app left it alone."},
		{"other", errors.New("disk full"), "Couldn't open your library. Please try again."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestSession(t, ui, libraryRun(ui, app.Info{}, c.err))
			err := s.StartPersonal()
			if err == nil {
				t.Fatal("want an error")
			}
			if got := desktopshell.Words(err); got != c.words {
				t.Fatalf("words = %q", got)
			}
			if st := s.State(); st.Kind != desktopshell.Failed || st.Words != c.words {
				t.Fatalf("state = %+v", st)
			}
			if s.Origin() != "" {
				t.Fatal("a failed start must not leave an origin")
			}
			if cfg, _ := desktopshell.Load(s.dirs.File()); cfg.Mode != "" {
				t.Fatalf("a failed start must not be saved as the mode: %+v", cfg)
			}
		})
	}
}

func TestWindowToolsMissingWarnsOnce(t *testing.T) {
	ui := &fakeUI{}
	s := newTestSession(t, ui, libraryRun(ui, app.Info{ToolsMissing: true}, nil))
	if err := s.StartPersonal(); err != nil {
		t.Fatal(err)
	}
	if len(ui.warns) != 1 || ui.warns[0] != missingToolsWords {
		t.Fatalf("warnings = %q", ui.warns)
	}
}

func TestWindowPortBusy(t *testing.T) {
	taken, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	busy := taken.Addr().(*net.TCPAddr).Port

	ln, err := listenLoopback(busy)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := ln.Addr().(*net.TCPAddr)
	if got.Port == busy || !got.IP.IsLoopback() {
		t.Fatalf("got %v with %d busy", got, busy)
	}

	// A free saved port is reused.
	free := got.Port
	ln.Close()
	again, err := listenLoopback(free)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if again.Addr().(*net.TCPAddr).Port != free {
		t.Fatalf("saved port %d not reused: %v", free, again.Addr())
	}
}

func TestWindowSendBeforeReady(t *testing.T) {
	var ran []string
	b := &Bridge{origin: func() string { return "http://127.0.0.1:9" }, exec: func(js string) { ran = append(ran, js) }}

	if b.Send("toggle") || len(ran) != 0 {
		t.Fatal("a command before the page is ready must be dropped")
	}
	b.Handle(msgReady, "wails://localhost/") // the picker is not the session
	if b.Send("toggle") {
		t.Fatal("the picker must not mark the page ready")
	}
	b.Handle(msgReady, "http://127.0.0.1:9/")
	if !b.Send("toggle") || len(ran) != 1 || !strings.Contains(ran[0], `"toggle"`) {
		t.Fatalf("ran = %q", ran)
	}
	if b.Send("rm -rf") || b.Send(`toggle"});alert(1);//`) {
		t.Fatal("only the fixed commands may be sent")
	}
	b.Reset()
	if b.Send("back") {
		t.Fatal("a new page must say ready again")
	}
}

func TestBridgeAllowOpen(t *testing.T) {
	const origin = "http://127.0.0.1:4000"
	cases := []struct {
		name, origin, from, target string
		want                       bool
	}{
		{"https link", origin, origin + "/books", "https://example.com/a?b=1", true},
		{"http link", origin, origin + "/", "http://example.com", true},
		{"mailto", origin, origin + "/x", "mailto:a@example.com", true},
		{"wrong origin", origin, "https://evil.example/", "https://example.com", false},
		{"look-alike origin", origin, "http://127.0.0.1:40001/", "https://example.com", false},
		{"picker page", origin, "wails://localhost/", "https://example.com", false},
		{"no session", "", origin + "/", "https://example.com", false},
		{"file", origin, origin + "/", "file:///etc/passwd", false},
		{"javascript", origin, origin + "/", "javascript:alert(1)", false},
		{"data", origin, origin + "/", "data:text/html,x", false},
		{"no host", origin, origin + "/", "https:///path", false},
		{"oversize", origin, origin + "/", "https://example.com/" + strings.Repeat("a", 2048), false},
		{"exactly at the limit", origin, origin + "/", "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/")), true},
		{"unparsable", origin, origin + "/", "https://exa mple.com/%zz", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := allowOpen(c.origin, c.from, c.target); got != c.want {
				t.Fatalf("allowOpen = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBridgeUnknownMessage(t *testing.T) {
	var opened []string
	b := &Bridge{
		origin: func() string { return "http://127.0.0.1:9" },
		open:   func(u string) error { opened = append(opened, u); return nil },
		exec:   func(string) { t.Fatal("no script may run for an unknown message") },
	}
	from := "http://127.0.0.1:9/"
	for _, m := range []string{"", "tendshelf:unknown x", "tendshelf:open", "wails:runtime:ready", "tendshelf:probe 1 {}", "TENDSHELF:READY"} {
		b.Handle(m, from)
	}
	if len(opened) != 0 || b.Send("toggle") {
		t.Fatalf("unknown messages had an effect: opened %q", opened)
	}
	b.Handle(msgOpen+"https://example.com/", from)
	b.Handle(msgOpen+"https://example.com/", "https://other.example/")
	if len(opened) != 1 {
		t.Fatalf("opened = %q, want exactly the one from the session", opened)
	}
}

func TestWindowMenuActions(t *testing.T) {
	var calls []string
	a := menuActions{
		change:    func() { calls = append(calls, "change") },
		quit:      func() { calls = append(calls, "quit") },
		zoomIn:    func() { calls = append(calls, "in") },
		zoomOut:   func() { calls = append(calls, "out") },
		zoomReset: func() { calls = append(calls, "reset") },
	}
	keys := zoomKeys(a)
	for _, k := range []string{"ctrl+plus", "ctrl+=", "ctrl+-", "ctrl+0"} {
		keys[k](nil)
	}
	if got := strings.Join(calls, ","); got != "in,in,out,reset" {
		t.Fatalf("calls = %s", got)
	}
}
