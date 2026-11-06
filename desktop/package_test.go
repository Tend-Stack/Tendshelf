package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/png" // registers the PNG decoder
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// iconSizes are the hicolor sizes the package installs as PNG; 192 and 512 come from the pages' assets.
var iconSizes = []int{16, 22, 24, 32, 48, 64, 128, 192, 256, 512}

// TestDeskIconsSizes: each committed desktop picture is a PNG of exactly its size, and the tray
// marks are 64 px with a transparent corner.
func TestDeskIconsSizes(t *testing.T) {
	check := func(raw []byte, name string, size int, transparent bool) {
		t.Helper()
		img, format, err := image.Decode(bytes.NewReader(raw))
		if err != nil || format != "png" {
			t.Errorf("%s: not a PNG (%v)", name, err)
			return
		}
		if b := img.Bounds(); b.Dx() != size || b.Dy() != size {
			t.Errorf("%s is %dx%d, want %dx%d", name, b.Dx(), b.Dy(), size, size)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); transparent && a != 0 {
			t.Errorf("%s corner is not transparent", name)
		}
	}
	for _, size := range iconSizes {
		if size == 192 || size == 512 {
			continue
		}
		name := fmt.Sprintf("package/icons/icon-%d.png", size)
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Error(err)
			continue
		}
		check(raw, name, size, false)
	}
	for _, name := range []string{trayIconLight, trayIconDark} {
		raw := iconBytes(name)
		if raw == nil {
			t.Errorf("%s is not embedded", name)
			continue
		}
		check(raw, name, 64, true)
	}
}

// TestDeskPackageIcons: the built .deb lists every icon size and the scalable drawing, and the
// desktop file names the icon by its theme name. It needs dpkg-deb and the built binary
// (make deb builds both); otherwise it is skipped.
func TestDeskPackageIcons(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skip("dpkg-deb not installed")
	}
	if _, err := os.Stat("../bin/tendshelf-desktop"); err != nil {
		t.Skip("bin/tendshelf-desktop not built; run make desktop")
	}
	out := filepath.Join(t.TempDir(), "x.deb")
	script := exec.Command("sh", "package/build-deb.sh")
	script.Env = append(os.Environ(), "TENDSHELF_DEB_OUT="+out)
	if raw, err := script.CombinedOutput(); err != nil {
		t.Fatalf("build-deb.sh: %v\n%s", err, raw)
	}
	list, err := exec.Command("dpkg-deb", "-c", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range iconSizes {
		want := fmt.Sprintf("./usr/share/icons/hicolor/%dx%d/apps/org.tendshelf.Tendshelf.png", size, size)
		if !strings.Contains(string(list), want) {
			t.Errorf("deb lacks %s", want)
		}
	}
	if want := "./usr/share/icons/hicolor/scalable/apps/org.tendshelf.Tendshelf.svg"; !strings.Contains(string(list), want) {
		t.Errorf("deb lacks %s", want)
	}
	desk, err := os.ReadFile("package/tendshelf-desktop.desktop")
	if err != nil || !strings.Contains(string(desk), "Icon=org.tendshelf.Tendshelf\n") {
		t.Errorf("desktop file Icon line: %v", err)
	}
}

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
