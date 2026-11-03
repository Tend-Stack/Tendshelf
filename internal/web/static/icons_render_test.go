package static

import (
	"bytes"
	"image"
	"image/color"
	_ "image/png" // registers the PNG decoder for image.Decode
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The install icons are rendered once from img/tendshelf-app-icon.svg and committed: the build needs
// no rasteriser. TestRenderInstallIcons redraws them (opt-in: TENDSHELF_RENDER_ICONS=1 and
// TENDSHELF_TEST_BROWSER=/path/to/chrome); TestInstallIcons runs always and checks the committed files.

// installIcon is one committed PNG: its size, whether the picture runs to the edge, and its name.
type installIcon struct {
	name string
	size int
	// fullBleed icons carry the brand colour to every edge (the platform masks them); the others
	// keep the rounded corners of the SVG on a transparent ground.
	fullBleed bool
}

var installIcons = []installIcon{
	{"img/icon-192.png", 192, false},
	{"img/icon-512.png", 512, false},
	// Maskable: Android may crop to a circle, so the picture must sit in the central 80 %. The glyph of
	// the SVG spans about 30 % to 72 % of the width, well inside.
	{"img/icon-maskable-512.png", 512, true},
	// iOS fills transparency with black, so its Home Screen icon runs to the edge as well.
	{"img/apple-touch-icon.png", 180, true},
}

func TestRenderInstallIcons(t *testing.T) {
	if os.Getenv("TENDSHELF_RENDER_ICONS") != "1" {
		t.Skip("TENDSHELF_RENDER_ICONS=1 redraws the committed install icons")
	}
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	svg, err := files.ReadFile("img/tendshelf-app-icon.svg")
	if err != nil {
		t.Fatal(err)
	}
	b := cdp.Start(t, chrome)
	for _, ic := range installIcons {
		markup := string(svg)
		if ic.fullBleed {
			markup = strings.Replace(markup, ` rx="22"`, "", 1)
		}
		page := "<style>html,body{margin:0;background:transparent}svg{display:block;width:100vw;height:100vh}</style>" + markup
		tab := b.NewTab(ic.size, ic.size, false, true)
		if err := tab.Call("Emulation.setDefaultBackgroundColorOverride", map[string]any{"color": map[string]any{"r": 0, "g": 0, "b": 0, "a": 0}}, nil); err != nil {
			t.Fatal(err)
		}
		tab.Navigate("data:text/html;charset=utf-8," + url.PathEscape(page))
		tab.Screenshot(filepath.FromSlash(ic.name))
	}
}

func TestInstallIcons(t *testing.T) {
	const brand = 0xE2703A
	for _, ic := range installIcons {
		raw, err := os.ReadFile(filepath.FromSlash(ic.name))
		if err != nil {
			t.Errorf("%s: %v", ic.name, err)
			continue
		}
		img, format, err := image.Decode(bytes.NewReader(raw))
		if err != nil || format != "png" {
			t.Errorf("%s: not a PNG (%v)", ic.name, err)
			continue
		}
		if b := img.Bounds(); b.Dx() != ic.size || b.Dy() != ic.size {
			t.Errorf("%s is %dx%d, want %dx%d", ic.name, b.Dx(), b.Dy(), ic.size, ic.size)
		}
		corner := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
		switch {
		case ic.fullBleed && (corner.A != 255 || uint32(corner.R)<<16|uint32(corner.G)<<8|uint32(corner.B) != brand):
			t.Errorf("%s corner is %v, want opaque #E2703A to the edge", ic.name, corner)
		case !ic.fullBleed && corner.A != 0:
			t.Errorf("%s corner is %v, want transparent", ic.name, corner)
		}
		// Every icon is served from /static/ under its hashed name.
		s, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if a, ok := s.Asset(ic.name); !ok || a.Type != "image/png" {
			t.Errorf("%s is not served as image/png", ic.name)
		}
	}
}
