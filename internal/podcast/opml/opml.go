// Package opml reads and writes the OPML subscription lists podcast apps exchange (decision 0038).
// It does no network I/O.
package opml

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html/charset"
)

const (
	// MaxBytes is the largest file Parse reads.
	MaxBytes = 1 << 20
	// MaxOutlines is the most shows Parse returns.
	MaxOutlines = 200
	maxDepth    = 32
)

// ErrTooLarge means the file is over MaxBytes.
var ErrTooLarge = errors.New("opml: file too large")

// ErrNotOPML means the file is not an OPML document.
var ErrNotOPML = errors.New("opml: not an OPML file")

// Outline is one show: its name and feed address.
type Outline struct {
	Title  string
	XMLURL string
}

// Parse reads an OPML file. Nested outlines are flattened; an outline counts when it has an xmlUrl
// (any type, though "rss" is the usual one). Repeated addresses are dropped and at most 200 return.
func Parse(r io.Reader) ([]Outline, error) {
	lr := &io.LimitedReader{R: r, N: MaxBytes + 1}
	dec := xml.NewDecoder(lr)
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = charset.NewReaderLabel
	var (
		out     []Outline
		seen    = map[string]bool{}
		depth   int
		sawRoot bool
	)
	for {
		tok, err := dec.Token()
		if lr.N <= 0 {
			return nil, ErrTooLarge
		}
		if err != nil {
			if err == io.EOF && sawRoot {
				return out, nil
			}
			if sawRoot && len(out) > 0 {
				return out, nil
			}
			return nil, ErrNotOPML
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > maxDepth {
				return out, nil
			}
			name := strings.ToLower(t.Name.Local)
			if !sawRoot {
				if name != "opml" {
					return nil, ErrNotOPML
				}
				sawRoot = true
				continue
			}
			if name != "outline" {
				continue
			}
			var o Outline
			var text string
			for _, a := range t.Attr {
				switch strings.ToLower(a.Name.Local) {
				case "xmlurl":
					o.XMLURL = strings.TrimSpace(a.Value)
				case "title":
					o.Title = strings.Join(strings.Fields(a.Value), " ")
				case "text":
					text = strings.Join(strings.Fields(a.Value), " ")
				}
			}
			if o.Title == "" {
				o.Title = text
			}
			if o.XMLURL == "" || len(o.XMLURL) > 2048 || seen[o.XMLURL] {
				continue
			}
			if len(out) >= MaxOutlines {
				return out, nil
			}
			seen[o.XMLURL] = true
			out = append(out, o)
		case xml.EndElement:
			depth--
		}
	}
}

// Write writes an OPML 2.0 file in UTF-8.
func Write(w io.Writer, title string, outlines []Outline) error {
	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<opml version="2.0"><head><title>`)
	xml.EscapeText(&b, []byte(title))
	b.WriteString(`</title></head><body>`)
	for _, o := range outlines {
		b.WriteString(`<outline type="rss" text="`)
		attr(&b, o.Title)
		b.WriteString(`" title="`)
		attr(&b, o.Title)
		b.WriteString(`" xmlUrl="`)
		attr(&b, o.XMLURL)
		b.WriteString(`"/>`)
	}
	b.WriteString("</body></opml>\n")
	_, err := io.WriteString(w, b.String())
	if err != nil {
		return fmt.Errorf("opml: %w", err)
	}
	return nil
}

func attr(b *strings.Builder, s string) { xml.EscapeText(b, []byte(s)) }
