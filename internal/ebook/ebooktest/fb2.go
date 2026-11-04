package ebooktest

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// FB2Author is a name in title-info.
type FB2Author struct{ First, Last string }

// FB2Section is a section; Sub are nested sections.
type FB2Section struct {
	Title string
	Paras []string
	Sub   []FB2Section
}

// FB2Options describes a synthetic FictionBook 2 file. Encoding is "utf-8" (default) or
// "windows-1251".
type FB2Options struct {
	Encoding string
	Title    string
	Authors  []FB2Author
	Lang     string
	ISBN     string
	Sections []FB2Section
	Notes    []string
}

func fb2Section(sb *strings.Builder, s FB2Section) {
	sb.WriteString("<section>")
	if s.Title != "" {
		sb.WriteString("<title><p>" + esc(s.Title) + "</p></title>")
	}
	for _, p := range s.Paras {
		sb.WriteString("<p>" + esc(p) + "</p>")
	}
	for _, c := range s.Sub {
		fb2Section(sb, c)
	}
	sb.WriteString("</section>")
}

// FB2 builds the file bytes.
func FB2(o FB2Options) []byte {
	enc := o.Encoding
	if enc == "" {
		enc = "utf-8"
	}
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="` + enc + `"?><FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0"><description><title-info>`)
	for _, a := range o.Authors {
		sb.WriteString("<author><first-name>" + esc(a.First) + "</first-name><last-name>" + esc(a.Last) + "</last-name></author>")
	}
	sb.WriteString("<book-title>" + esc(o.Title) + "</book-title>")
	if o.Lang != "" {
		sb.WriteString("<lang>" + o.Lang + "</lang>")
	}
	sb.WriteString("</title-info>")
	if o.ISBN != "" {
		sb.WriteString("<publish-info><isbn>" + o.ISBN + "</isbn></publish-info>")
	}
	sb.WriteString("</description><body>")
	for _, s := range o.Sections {
		fb2Section(&sb, s)
	}
	sb.WriteString("</body>")
	if len(o.Notes) > 0 {
		sb.WriteString(`<body name="notes">`)
		for _, n := range o.Notes {
			sb.WriteString("<section><title><p>n</p></title><p>" + esc(n) + "</p></section>")
		}
		sb.WriteString("</body>")
	}
	sb.WriteString("</FictionBook>")
	if strings.EqualFold(enc, "windows-1251") {
		b, err := charmap.Windows1251.NewEncoder().Bytes([]byte(sb.String()))
		if err != nil {
			panic(err)
		}
		return b
	}
	return []byte(sb.String())
}

// FB2Zip wraps FB2 bytes as a .fb2.zip with the one entry name given.
func FB2Zip(t testing.TB, name string, fb2 []byte) []byte {
	t.Helper()
	return Zip(t, []Entry{{Name: name, Data: fb2}})
}
