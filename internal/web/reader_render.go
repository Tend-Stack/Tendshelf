package web

import (
	"html"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// The reader's text, rendered from the canonical document (M5 R4 P2). A block's text is cut at every sentence
// edge, run edge and highlight edge; each cut piece becomes its own small element, so the markup is always
// well formed and the text nodes inside one sentence span join to exactly Text[start:end] (offsets are UTF-16
// units, the same numbers the client counts with).

// rdrMark is a note dot or a bookmark flag drawn after a sentence.
type rdrMark struct {
	Kind string // "note" or "bm"
	ID   string // the highlight id for a note
}

// rdrPiece is a stretch of a block that has the same sentence, styles and highlights throughout.
type rdrPiece struct {
	Text       string
	Start, End int      // UTF-16 offsets in the block
	Sent       int      // index into Block.Sentences, -1 outside every sentence
	Styles     []string // em, strong, code
	Hls        []string // ids of the highlights over the piece
	OpenSent   bool     // first piece of its sentence
	CloseSent  bool     // last piece of its sentence
	After      []rdrMark
	Link       *ebook.Link // the link the piece sits in, or nil
}

// rdrHl is a highlight clipped to one block: [Start,End) in that block's offsets.
type rdrHl struct {
	ID         string
	Start, End int
	HasNote    bool
	Colour     string
}

// rdrBlockOf is the block id inside a sentence id ("c1.p3.s2" gives "c1.p3").
func rdrBlockOf(sid string) string {
	i := strings.LastIndexByte(sid, '.')
	if i < 0 {
		return sid
	}
	return sid[:i]
}

// rdrHlsFor clips highlights to block b. A highlight runs from (StartSID's block, StartOff) to (EndSID's block,
// EndOff); the blocks between are covered whole.
func rdrHlsFor(b ebook.Block, units int, hls []progress.Highlight) []rdrHl {
	var out []rdrHl
	for _, h := range hls {
		sb, eb := rdrBlockOf(h.StartSID), rdrBlockOf(h.EndSID)
		start, end := 0, units
		if b.ID == sb {
			start = h.StartOff
		} else if ebook.CompareSID(b.ID+".s0", sb+".s0") < 0 {
			continue
		}
		if b.ID == eb {
			end = h.EndOff
		} else if ebook.CompareSID(b.ID+".s0", eb+".s0") > 0 {
			continue
		}
		start, end = max(start, 0), min(end, units)
		if start >= end {
			continue
		}
		out = append(out, rdrHl{ID: h.ID, Start: start, End: end, HasNote: h.Note != "", Colour: h.Colour})
	}
	return out
}

// rdrSegments cuts block into pieces and puts the flags in. bookmarkSents holds the sentence indexes that carry
// a bookmark.
func rdrSegments(b ebook.Block, hls []rdrHl, bookmarkSents map[int]bool) []rdrPiece {
	units := utf16.Encode([]rune(b.Text))
	n := len(units)
	if n == 0 {
		return nil
	}
	cuts := map[int]bool{0: true, n: true}
	clip := func(x int) int { return min(max(x, 0), n) }
	for _, s := range b.Sentences {
		cuts[clip(s[0])], cuts[clip(s[1])] = true, true
	}
	for _, r := range b.Runs {
		cuts[clip(r.Start)], cuts[clip(r.End)] = true, true
	}
	for _, h := range hls {
		cuts[clip(h.Start)], cuts[clip(h.End)] = true, true
	}
	for _, l := range b.Links {
		cuts[clip(l.Start)], cuts[clip(l.End)] = true, true
	}
	points := make([]int, 0, len(cuts))
	for c := range cuts {
		points = append(points, c)
	}
	slices.Sort(points)
	var out []rdrPiece
	for i := 0; i+1 < len(points); i++ {
		a, e := points[i], points[i+1]
		p := rdrPiece{Text: string(utf16.Decode(units[a:e])), Start: a, End: e, Sent: -1}
		for k, s := range b.Sentences {
			if a >= s[0] && a < s[1] {
				p.Sent = k
				break
			}
		}
		for _, r := range b.Runs {
			if a >= r.Start && e <= r.End && !slices.Contains(p.Styles, r.Style) {
				p.Styles = append(p.Styles, r.Style)
			}
		}
		for _, h := range hls {
			if a >= h.Start && e <= h.End {
				p.Hls = append(p.Hls, h.ID)
			}
		}
		for i := range b.Links {
			if l := &b.Links[i]; a >= l.Start && e <= l.End {
				p.Link = l
				break
			}
		}
		out = append(out, p)
	}
	for i := range out {
		if out[i].Sent < 0 {
			continue
		}
		out[i].OpenSent = i == 0 || out[i-1].Sent != out[i].Sent
		out[i].CloseSent = i == len(out)-1 || out[i+1].Sent != out[i].Sent
	}
	closeOf := func(i int) int {
		for out[i].Sent >= 0 && !out[i].CloseSent {
			i++
		}
		return i
	}
	for _, h := range hls {
		if !h.HasNote {
			continue
		}
		for i := range out {
			if out[i].Start < h.End && h.End <= out[i].End {
				j := closeOf(i)
				out[j].After = append(out[j].After, rdrMark{Kind: "note", ID: h.ID})
				break
			}
		}
	}
	for k := range bookmarkSents {
		for i := range out {
			if out[i].Sent == k && out[i].CloseSent {
				out[i].After = append(out[i].After, rdrMark{Kind: "bm"})
			}
		}
	}
	return out
}

// rdrBlockOpts is what writing one block needs besides the block.
type rdrBlockOpts struct {
	DropCap   bool // wrap the first letter of the first sentence
	Hls       []progress.Highlight
	Bookmarks map[string]bool // sentence ids that carry a bookmark
	// LinkTo gives the address of an in-book link and, for a footnote, the address of its popup
	// fragment. ok is false for a link the page cannot take (its text is then written plain).
	LinkTo func(l ebook.Link) (href, noteHref string, ok bool)
}

// rdrNoteIcon is the pencil of the note dot, the same shape as the "edit" icon.
const rdrNoteIcon = `<svg class="ts-icon" viewBox="0 0 24 24" aria-hidden="true"><path d="M4 20h4L19 9l-4-4L4 16z"/></svg>`

// rdrInner writes the inside of a block: sentence spans, runs, highlights and flags. Every string is escaped.
func rdrInner(b ebook.Block, o rdrBlockOpts) string {
	var sb strings.Builder
	n := len(utf16.Encode([]rune(b.Text)))
	bms := map[int]bool{}
	for k := range b.Sentences {
		if o.Bookmarks[ebook.SentenceID(b.ID, k)] {
			bms[k] = true
		}
	}
	drop := o.DropCap
	hls := rdrHlsFor(b, n, o.Hls)
	colour := map[string]string{}
	for _, h := range hls {
		colour[h.ID] = h.Colour
	}
	for _, p := range rdrSegments(b, hls, bms) {
		if p.OpenSent {
			sid := ebook.SentenceID(b.ID, p.Sent)
			sb.WriteString(`<span class="ts-s" id="s-` + html.EscapeString(sid) + `" data-sid="` + html.EscapeString(sid) + `" data-o="` + strconv.Itoa(p.Start) + `">`)
		}
		for _, s := range p.Styles {
			sb.WriteString("<" + s + ">")
		}
		for _, id := range p.Hls {
			sb.WriteString(`<span class="ts-hl" data-hl="` + html.EscapeString(id) + `" data-colour="` + html.EscapeString(colour[id]) + `">`)
		}
		closeLink := ""
		if p.Link != nil {
			closeLink = rdrOpenLink(&sb, *p.Link, o)
		}
		text := p.Text
		if drop && p.OpenSent {
			drop = false
			if r := []rune(text); len(r) > 0 && unicode.IsLetter(r[0]) {
				sb.WriteString(`<span class="ts-reader__drop">` + html.EscapeString(string(r[0])) + `</span>`)
				text = string(r[1:])
			}
		}
		sb.WriteString(html.EscapeString(text))
		sb.WriteString(closeLink)
		sb.WriteString(strings.Repeat(`</span>`, len(p.Hls)))
		for i := len(p.Styles) - 1; i >= 0; i-- {
			sb.WriteString("</" + p.Styles[i] + ">")
		}
		if p.CloseSent {
			sb.WriteString(`</span>`)
		}
		if p.CloseSent || p.Sent < 0 {
			for _, m := range p.After {
				switch m.Kind {
				case "note":
					sb.WriteString(`<button type="button" class="ts-notedot" data-hl-note="` + html.EscapeString(m.ID) + `" aria-label="` + html.EscapeString(readerNoteLabel) + `">` + rdrNoteIcon + `</button>`)
				case "bm":
					sb.WriteString(`<span class="ts-reader__bm" role="img" aria-label="` + html.EscapeString(readerBookmarkLbl) + `"></span>`)
				}
			}
		}
	}
	return sb.String()
}

// rdrOpenLink writes the opening <a> of a link piece and returns its closing tag ("" when the link
// cannot be drawn). An outside address opens in a new tab with no opener; a footnote carries the address
// of its popup. The href is ours or passed the scheme allowlist when the document was made.
func rdrOpenLink(sb *strings.Builder, l ebook.Link, o rdrBlockOpts) string {
	if l.URL != "" {
		sb.WriteString(`<a class="ts-reader__link" href="` + html.EscapeString(l.URL) + `" rel="noopener noreferrer" target="_blank">`)
		return "</a>"
	}
	if o.LinkTo == nil {
		return ""
	}
	href, note, ok := o.LinkTo(l)
	if !ok {
		return ""
	}
	sb.WriteString(`<a class="ts-reader__link" href="` + html.EscapeString(href) + `"`)
	if l.Note && note != "" {
		sb.WriteString(` data-note-href="` + html.EscapeString(note) + `"`)
	}
	sb.WriteString(">")
	return "</a>"
}
