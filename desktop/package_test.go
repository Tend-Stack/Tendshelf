package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPackageControl checks the control template's required fields and that no maintainer script exists:
// the package installs files and nothing else.
func TestPackageControl(t *testing.T) {
	raw, err := os.ReadFile("package/control.in")
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok && !strings.HasPrefix(line, " ") {
			fields[k] = v
		}
	}
	for k, want := range map[string]string{"Package": "tendshelf-desktop", "Version": "@VERSION@", "Architecture": "@ARCH@"} {
		if fields[k] != want {
			t.Errorf("%s = %q, want %q", k, fields[k], want)
		}
	}
	deps := strings.Split(fields["Depends"], ", ")
	for _, want := range []string{"libgtk-4-1", "libwebkitgtk-6.0-4", "gstreamer1.0-plugins-good", "gstreamer1.0-libav", "ffmpeg"} {
		found := false
		for _, d := range deps {
			found = found || d == want
		}
		if !found {
			t.Errorf("Depends lacks %s: %q", want, fields["Depends"])
		}
	}
	if fields["Recommends"] != "gnome-shell-extension-appindicator" {
		t.Errorf("Recommends = %q", fields["Recommends"])
	}
	for _, f := range []string{"preinst", "postinst", "prerm", "postrm"} {
		if _, err := os.Stat("package/" + f); err == nil {
			t.Errorf("maintainer script %s must not exist", f)
		}
	}
}

// TestPackageVersionParse: the package Version is the plain YEAR.RELEASE.FIX of the last tag, whatever
// `git describe --tags --long --dirty` appends.
func TestPackageVersionParse(t *testing.T) {
	cases := map[string]string{
		"v2026.1.0-366-g650d5a1":      "2026.1.0",
		"v2026.1.0-0-g650d5a1":        "2026.1.0",
		"v2026.12.3-4-g1a2b3c4-dirty": "2026.12.3",
		"v2027.1.0-0-g1a2b3c4-dirty":  "2027.1.0",
	}
	for describe, want := range cases {
		out, err := exec.Command("sh", "package/build-deb.sh", "--version", describe).Output()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(out)); got != want {
			t.Errorf("%s -> %q, want %q", describe, got, want)
		}
	}
}
