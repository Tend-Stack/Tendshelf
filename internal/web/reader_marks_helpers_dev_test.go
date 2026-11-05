//go:build dev

package web

import "github.com/Tend-Stack/tendshelf/internal/progress"

// rnWithColour is the plain highlight (no note) of a colour.
func rnWithColour(hls []progress.Highlight, colour string) progress.Highlight {
	for _, h := range hls {
		if h.Colour == colour && h.Note == "" {
			return h
		}
	}
	return progress.Highlight{}
}

// rnHasNote reports whether one of the highlights carries the note.
func rnHasNote(hls []progress.Highlight, note string) bool {
	for _, h := range hls {
		if h.Note == note {
			return true
		}
	}
	return false
}
