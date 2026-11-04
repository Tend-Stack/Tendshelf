package ebook

import "strings"

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

// FormatLabel is the plain word for a format.
func FormatLabel(format string) string {
	switch format {
	case "epub":
		return "EPUB"
	case "pdf":
		return "PDF"
	case "docx":
		return "Word"
	case "fb2":
		return "FB2"
	case "mobi":
		return "MOBI"
	case "azw3":
		return "AZW3"
	case "txt":
		return "Plain text"
	case "md":
		return "Markdown"
	case "html":
		return "HTML"
	}
	return format
}
