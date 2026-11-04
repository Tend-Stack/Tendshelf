package ebook

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// seg is a piece of block text with one style ("", em, strong or code) and the byte offset of its
// first byte in the source part (-1 when unknown).
type seg struct {
	Text    string
	Style   string
	ByteOff int
}

// nrune is a normalised rune with the style and source offset of the segment it came from.
type nrune struct {
	r     rune
	style string
	off   int  // source byte offset of the segment start; -1 if none
	first bool // first rune kept from its segment
}

func isDropped(r rune) bool { return r == 0x00AD || r == 0x200B || r == 0xFEFF }

func isCollapsible(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v'
}

// normalizeSegs applies the rules of normalize: NFC, drop soft hyphen, zero-width space and BOM,
// collapse ASCII whitespace into one space (not when keepSpace), trim.
func normalizeSegs(segs []seg, keepSpace bool) []nrune {
	var out []nrune
	for _, s := range segs {
		text := norm.NFC.String(s.Text)
		first := true
		for _, r := range text {
			if isDropped(r) {
				continue
			}
			if !keepSpace && isCollapsible(r) {
				r = ' '
				if n := len(out); n > 0 && out[n-1].r == ' ' {
					continue
				}
			}
			out = append(out, nrune{r: r, style: s.Style, off: s.ByteOff, first: first})
			first = false
		}
	}
	if keepSpace {
		// pre keeps inner whitespace; only blank edges are trimmed.
		for len(out) > 0 && (out[0].r == '\n' || out[0].r == '\r') {
			out = out[1:]
		}
		for len(out) > 0 && (out[len(out)-1].r == '\n' || out[len(out)-1].r == '\r' || out[len(out)-1].r == ' ') {
			out = out[:len(out)-1]
		}
		return out
	}
	for len(out) > 0 && out[0].r == ' ' {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1].r == ' ' {
		out = out[:len(out)-1]
	}
	return out
}

// normalizeString normalises plain text (metadata, titles).
func normalizeString(s string) string {
	ns := normalizeSegs([]seg{{Text: s, ByteOff: -1}}, false)
	var sb strings.Builder
	for _, n := range ns {
		sb.WriteRune(n.r)
	}
	return sb.String()
}
