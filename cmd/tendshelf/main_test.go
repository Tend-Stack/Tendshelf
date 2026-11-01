package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// startRun starts run with args and waits until start-up has finished (the serving hook), not
// until a file appears: on a slow machine migrations can still be running when the database file
// exists. The returned stop cancels run and returns its error.
func startRun(t *testing.T, args ...string) (stop func() error) {
	t.Helper()
	ready := make(chan struct{})
	serving = func(net.Addr) { close(ready) }
	t.Cleanup(func() { serving = func(net.Addr) {} })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, append(args, "-addr", "127.0.0.1:0")) }()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("run returned before serving: %v", err)
	case <-time.After(30 * time.Second):
		cancel()
		t.Fatal("run did not finish start-up within 30 s")
	}
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			t.Fatal("run did not return after the context was cancelled")
			return nil
		}
	}
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func TestRunRefusesNewerDatabase(t *testing.T) {
	dir := t.TempDir()
	st, err := db.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	path := db.FilePath(dir)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	var v [4]byte
	binary.BigEndian.PutUint32(v[:], db.SchemaVersion+1)
	if _, err := f.WriteAt(v[:], 60); err != nil {
		t.Fatal(err)
	}
	f.Close()
	before := fileHash(t, path)

	err = run(context.Background(), []string{"-data", dir, "-addr", "127.0.0.1:0"})
	if err == nil {
		t.Fatal("run must refuse a newer database")
	}
	msg := err.Error()
	for _, want := range []string{path, "newer version of Tendshelf", fmt.Sprintf("schema %d", db.SchemaVersion+1), fmt.Sprintf("up to %d", db.SchemaVersion), "The file was not changed."} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
	if strings.Contains(msg, "SQLITE") || strings.Contains(msg, "sqlite") {
		t.Errorf("message leaks driver details: %q", msg)
	}
	if fileHash(t, path) != before {
		t.Fatal("the refused database changed")
	}
	for _, side := range []string{path + "-wal", path + "-shm", filepath.Join(dir, "backups")} {
		if _, err := os.Stat(side); err == nil {
			t.Fatalf("%s was created", side)
		}
	}
}

func TestRunRefusesForeignFile(t *testing.T) {
	dir := t.TempDir()
	path := db.FilePath(dir)
	if err := os.WriteFile(path, []byte(strings.Repeat("not sqlite ", 30)), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"-data", dir, "-addr", "127.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "is not a Tendshelf database. The file was not changed.") {
		t.Fatalf("got %v", err)
	}
}

func TestRunOpensMigratesAndStops(t *testing.T) {
	dir := t.TempDir()
	stop := startRun(t, "-data", dir)
	if err := stop(); err != nil {
		t.Fatalf("run = %v", err)
	}

	st, err := db.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var uv int
	if err := st.Reader().QueryRow("PRAGMA user_version").Scan(&uv); err != nil || uv != db.SchemaVersion {
		t.Fatalf("user_version = %d, %v", uv, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "backups")); err == nil {
		t.Fatal("a fresh start must not back up")
	}
}

func TestDefaultDataDir(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		t.Skip("covers the XDG branch")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", xdg)
	if got, err := defaultDataDir(); err != nil || got != filepath.Join(xdg, "tendshelf") {
		t.Fatalf("XDG_DATA_HOME: got %q, %v", got, err)
	}
	home := t.TempDir()
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)
	if got, err := defaultDataDir(); err != nil || got != filepath.Join(home, ".local", "share", "tendshelf") {
		t.Fatalf("HOME: got %q, %v", got, err)
	}
	t.Setenv("HOME", "")
	if _, err := defaultDataDir(); err == nil || !strings.Contains(err.Error(), "pass -data") {
		t.Fatalf("no home: got %v", err)
	}
}

func TestDataFlagDefaultsToEnvironment(t *testing.T) {
	// TENDSHELF_DATA feeds the -data flag default; the flag wins when both are set.
	envDir, flagDir := t.TempDir(), t.TempDir()
	t.Setenv("TENDSHELF_DATA", envDir)
	for _, c := range []struct {
		args []string
		want string
	}{
		{nil, envDir},
		{[]string{"-data", flagDir}, flagDir},
	} {
		stop := startRun(t, c.args...)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(db.FilePath(c.want)); err != nil {
			t.Fatalf("no database in %s: %v", c.want, err)
		}
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

func TestRunRejectsBadTrustedProxy(t *testing.T) {
	err := run(context.Background(), []string{"-data", t.TempDir(), "-addr", "127.0.0.1:0", "-trusted-proxy", "not-an-ip"})
	if err == nil || !strings.Contains(err.Error(), "-trusted-proxy") {
		t.Fatalf("err = %v", err)
	}
}
