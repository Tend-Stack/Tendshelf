package app

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// startApp runs Run with cfg and waits for Ready. The returned stop cancels Run and returns its error.
func startApp(t *testing.T, cfg Config) (info Info, stop func() error) {
	t.Helper()
	if cfg.Addr == "" && cfg.Listener == nil {
		cfg.Addr = "127.0.0.1:0"
	}
	if cfg.CompatCacheGiB == 0 {
		cfg.CompatCacheGiB = 5
	}
	cfg.Version = "test"
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	ready := make(chan Info, 1)
	cfg.Ready = func(i Info) { ready <- i }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	select {
	case info = <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("Run returned before Ready: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("Run did not finish start-up within 30 s")
	}
	return info, func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return after the context was cancelled")
			return nil
		}
	}
}

func TestAppRunGivenListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	info, stop := startApp(t, Config{Listener: ln, DataDir: t.TempDir(), Addr: "127.0.0.1:1"})
	if info.Addr.String() != ln.Addr().String() {
		t.Fatalf("Info.Addr = %s, want the given listener %s", info.Addr, ln.Addr())
	}
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d", resp.StatusCode)
	}
	if err := stop(); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second); err == nil {
		c.Close()
		t.Fatal("the listener is still open after Run returned")
	}
}

func TestAppRunReadyInfo(t *testing.T) {
	dir := t.TempDir()
	info, stop := startApp(t, Config{DataDir: dir})
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(info.SetupURL)
	if err != nil || u.Path != "/setup" || u.Query().Get("code") == "" || u.Host != info.Addr.String() {
		t.Fatalf("SetupURL before an owner = %q (%v)", info.SetupURL, err)
	}

	st, err := db.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	svc := auth.NewService(st, users.New(st, users.Options{Hash: resetCheap}), auth.NewSessions(st, nil), auth.ServiceOptions{SetupCode: "ABCDE-FGHJK"})
	if _, err := svc.Setup(context.Background(), "ABCDE-FGHJK", users.NewUser{Username: "ana", DisplayName: "Ana", Password: "correct horse battery"}, netip.MustParseAddr("203.0.113.7"), "ua"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	info, stop = startApp(t, Config{DataDir: dir})
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if info.SetupURL != "" {
		t.Fatalf("SetupURL once an owner exists = %q, want empty", info.SetupURL)
	}
}

func TestAppRunToolsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-ffprobe")
	info, stop := startApp(t, Config{DataDir: t.TempDir(), FFprobe: missing, FFmpeg: missing})
	if err := stop(); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if !info.ToolsMissing {
		t.Fatal("ToolsMissing = false with ffprobe and ffmpeg missing")
	}
}

// TestAppRunTwice starts and stops the server twice in one process on one folder, which the
// desktop shell's mode switch relies on.
func TestAppRunTwice(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		_, stop := startApp(t, Config{DataDir: dir})
		if err := stop(); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
}

// The -compat-cache-gib minimum moved here with registerMediaJobs.
func TestAppRunCompatCacheMinimum(t *testing.T) {
	err := Run(context.Background(), Config{Addr: "127.0.0.1:0", DataDir: t.TempDir(), CompatCacheGiB: 0, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Fatalf("a cap below 1 GiB: err = %v", err)
	}
}

func TestDefaultDataDir(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("covers the XDG branch")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, err := DefaultDataDir(); err != nil || got != filepath.Join(xdg, "tendshelf") {
		t.Fatalf("XDG_DATA_HOME: got %q, %v", got, err)
	}
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)
	if got, err := DefaultDataDir(); err != nil || got != filepath.Join(home, ".local", "share", "tendshelf") {
		t.Fatalf("HOME: got %q, %v", got, err)
	}
	t.Setenv("HOME", "")
	if _, err := DefaultDataDir(); err == nil || !strings.Contains(err.Error(), "pass -data") {
		t.Fatalf("no home: got %v", err)
	}
}

func TestSetupURL(t *testing.T) {
	for _, tc := range []struct{ addr, want string }{
		{"127.0.0.1:8095", "http://127.0.0.1:8095/setup?code=ABCDE-FGHJK"},
		{"0.0.0.0:8095", "http://localhost:8095/setup?code=ABCDE-FGHJK"},
		{"[::]:8095", "http://localhost:8095/setup?code=ABCDE-FGHJK"},
		{"[::1]:8095", "http://[::1]:8095/setup?code=ABCDE-FGHJK"},
	} {
		a, err := net.ResolveTCPAddr("tcp", tc.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := setupURL(a, "ABCDE-FGHJK"); got != tc.want {
			t.Errorf("setupURL(%s) = %s, want %s", tc.addr, got, tc.want)
		}
	}
}
