package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestQRColoursAreFixedInEveryTheme: a QR code is dark modules on a light plate in dark, light, sepia
// and contrast alike. The two colours are defined once, on :root, and no theme block or media query
// redefines them; the drawing reads them and no other colour.
func TestQRColoursAreFixedInEveryTheme(t *testing.T) {
	tokens, err := os.ReadFile(filepath.Join("static", "css", "tokens.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(tokens)
	if n := strings.Count(css, "--qr-ink:"); n != 1 {
		t.Errorf("--qr-ink is defined %d times, want once", n)
	}
	if n := strings.Count(css, "--qr-paper:"); n != 1 {
		t.Errorf("--qr-paper is defined %d times, want once", n)
	}
	ink := regexp.MustCompile(`--qr-ink:\s*(#[0-9a-fA-F]+)`).FindStringSubmatch(css)
	paper := regexp.MustCompile(`--qr-paper:\s*(#[0-9a-fA-F]+)`).FindStringSubmatch(css)
	if ink == nil || paper == nil || ink[1] != "#000" || paper[1] != "#fff" {
		t.Fatalf("ink %v, paper %v: want #000 on #fff", ink, paper)
	}
	// The declarations sit in a block selected by :root alone, which every theme inherits.
	at := strings.Index(css, "--qr-ink:")
	open := strings.LastIndex(css[:at], "{")
	if sel := strings.TrimSpace(css[strings.LastIndex(css[:open], "}")+1 : open]); sel != ":root" {
		t.Errorf("the QR colours sit in %q, want :root alone", sel)
	}
	for _, name := range []string{"bundle.css", "pages-invite.css", "pages-me.css", "pages-admin.css", "pages-share.css"} {
		raw, err := os.ReadFile(filepath.Join("static", "css", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "--qr-") && name != "bundle.css" {
			t.Errorf("%s touches the QR colours", name)
		}
	}
	bundle, _ := os.ReadFile(filepath.Join("static", "css", "bundle.css"))
	for _, want := range []string{".ts-qr__paper { fill: var(--qr-paper); }", ".ts-qr__ink { fill: var(--qr-ink); }", "shape-rendering: crispEdges"} {
		if !strings.Contains(string(bundle), want) {
			t.Errorf("bundle.css lacks %q", want)
		}
	}
}
