package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// TestWriteEbooks writes the synthetic ebooks the Android offline-reading checks use (A9), only when
// ANDROID_EBOOK_DIR names a folder (android/tools/test-server.sh with WITH_EBOOKS=1). Nothing real: invented text,
// a generated picture.
func TestWriteEbooks(t *testing.T) {
	dir := os.Getenv("ANDROID_EBOOK_DIR")
	if dir == "" {
		t.Skip("ANDROID_EBOOK_DIR not set")
	}
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	for x := 0; x < 60; x++ {
		for y := 0; y < 40; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 6), B: 120, A: 255})
		}
	}
	var pic bytes.Buffer
	if err := png.Encode(&pic, img); err != nil {
		t.Fatal(err)
	}
	long := func(n int, tag string) string {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString(fmt.Sprintf("<p>%s paragraph %d. The lamp burned low and the old clock ticked on, and nobody in the house said a word about the weather outside the window.</p>", tag, i))
		}
		return sb.String()
	}
	epub := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Lamplight", Authors: []string{"Test Author"},
		Chapters: []ebooktest.EPUBChapter{
			{Title: "First", Raw: `<p>Opening words of the lamplight book. See <a href="ch2.xhtml">the pictures</a>.</p>` + long(40, "First")},
			{Title: "Pictures", Raw: `<p>Before the picture.</p><img src="pic.png" alt="A lamp"/><p>After the picture.</p>`},
			{Title: "Long", Raw: long(2500, "Long")},
		},
		ManifestXML: `<item id="p" href="pic.png" media-type="image/png"/>`,
		Extra:       []ebooktest.Entry{{Name: "OEBPS/pic.png", Data: pic.Bytes()}}})
	folder := filepath.Join(dir, "Test Author", "Lamplight")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "Lamplight.epub"), epub, 0o644); err != nil {
		t.Fatal(err)
	}
	// Plain Words has no picture; Single shares its title and author with the audiobook "Single", so the two become one book.
	for _, name := range []string{"Plain Words", "Single"} {
		b := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: name, Authors: []string{"Test Author"},
			Chapters: []ebooktest.EPUBChapter{{Title: "One", Raw: long(60, name+" one")}, {Title: "Two", Raw: long(60, name+" two")}}})
		d := filepath.Join(dir, "Test Author", name+" text")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, name+".epub"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
