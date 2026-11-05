package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The Makefile's version string is the plain tag on an exact tag and the long describe after it.
func TestMakefileVersion(t *testing.T) {
	for _, tool := range []string{"git", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " not installed")
		}
	}
	src, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("git", "init", "-q")
	run("git", "add", "Makefile")
	run("git", "commit", "-q", "-m", "one")
	run("git", "tag", "v2026.2.0")
	if got := run("make", "-s", "version"); got != "v2026.2.0" {
		t.Errorf("on the tag: %q, want v2026.2.0", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("git", "add", "x")
	run("git", "commit", "-q", "-m", "two")
	if got := run("make", "-s", "version"); !strings.HasPrefix(got, "v2026.2.0-1-g") {
		t.Errorf("after the tag: %q, want v2026.2.0-1-g<hash>", got)
	}
}
