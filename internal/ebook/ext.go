package ebook

import (
	"github.com/Tend-Stack/tendshelf/internal/ebookfile"
)

// ExtOf returns the lower-case ebook extension of name ("fb2.zip" and "kfx-zip" are two-part), or ""
// when the name is not an ebook extension Tendshelf recognises.
func ExtOf(name string) string { return ebookfile.ExtOf(name) }

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
