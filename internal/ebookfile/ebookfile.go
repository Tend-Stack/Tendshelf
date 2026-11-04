// Package ebookfile holds what the scanner-side packages (details, covers) need to know about an ebook file
// without importing the ebook service: which extensions count, whether a folder holds one book, what a picture is.
package ebookfile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var knownExts = map[string]bool{
	"epub": true, "pdf": true, "txt": true, "md": true, "markdown": true, "html": true, "htm": true,
	"xhtml": true, "docx": true, "fb2": true, "mobi": true, "azw3": true, "azw": true, "kfx": true,
}

// ExtOf returns the lower-case ebook extension of name ("fb2.zip" and "kfx-zip" are two-part), or ""
// when the name is not an ebook extension Tendshelf recognises.
func ExtOf(name string) string {
	n := strings.ToLower(name)
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	if strings.HasSuffix(n, ".fb2.zip") {
		return "fb2.zip"
	}
	if strings.HasSuffix(n, ".kfx-zip") {
		return "kfx-zip"
	}
	i := strings.LastIndexByte(n, '.')
	if i < 0 {
		return ""
	}
	e := n[i+1:]
	if knownExts[e] {
		return e
	}
	return ""
}

// ErrNoCover means an ebook has no usable cover picture.
var ErrNoCover = errors.New("ebook: no cover")

// SniffImage says what a picture is by its first bytes: "jpeg", "png" or "webp". Anything else, SVG and GIF
// included, is "": an SVG is a document that can carry script and is never shown as a cover.
func SniffImage(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "jpeg"
	case len(b) >= 8 && bytes.Equal(b[:8], []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

// SoleStemDir says whether a library ebook's folder belongs to that one book, as in Calibre's "Title (id)/":
// the folder holds ebook files of exactly one name stem (an EPUB and a MOBI of one title count as one).
// Only then may the folder's sidecar files and picture be lent to the book. An upload root has no
// extension and never qualifies.
func SoleStemDir(root string) (string, bool) {
	if ExtOf(root) == "" {
		return "", false
	}
	dir := filepath.Dir(root)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	stems := map[string]bool{}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		if x := ExtOf(e.Name()); x != "" {
			stems[strings.ToLower(strings.TrimSuffix(e.Name(), e.Name()[len(e.Name())-len(x)-1:]))] = true
		}
	}
	return dir, len(stems) == 1
}
