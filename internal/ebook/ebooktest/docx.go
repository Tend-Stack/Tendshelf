package ebooktest

import (
	"fmt"
	"strings"
	"testing"
)

// DocxRun is a run of text in a DocxPara.
type DocxRun struct {
	Text       string
	Em, Strong bool
}

// DocxPara is a paragraph: Style is a style id such as Heading1, Heading2 or Title; Footnote is the
// 1-based footnote it cites (0 = none).
type DocxPara struct {
	Style    string
	Runs     []DocxRun
	Numbered bool
	Footnote int
}

// DocxOptions describes a synthetic Word file.
type DocxOptions struct {
	Title     string
	Author    string
	Paras     []DocxPara
	Footnotes []string
}

// Para is a plain paragraph.
func Para(style, text string) DocxPara {
	return DocxPara{Style: style, Runs: []DocxRun{{Text: text}}}
}

const wns = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

// DOCX builds the file.
func DOCX(t testing.TB, o DocxOptions) []byte {
	t.Helper()
	var doc strings.Builder
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document ` + wns + `><w:body>`)
	for _, p := range o.Paras {
		doc.WriteString(`<w:p><w:pPr>`)
		if p.Style != "" {
			doc.WriteString(`<w:pStyle w:val="` + p.Style + `"/>`)
		}
		if p.Numbered {
			doc.WriteString(`<w:numPr><w:ilvl w:val="0"/><w:numId w:val="1"/></w:numPr>`)
		}
		doc.WriteString(`</w:pPr>`)
		for _, r := range p.Runs {
			doc.WriteString(`<w:r><w:rPr>`)
			if r.Em {
				doc.WriteString(`<w:i/>`)
			}
			if r.Strong {
				doc.WriteString(`<w:b/>`)
			}
			doc.WriteString(`</w:rPr><w:t xml:space="preserve">` + esc(r.Text) + `</w:t></w:r>`)
		}
		if p.Footnote > 0 {
			doc.WriteString(fmt.Sprintf(`<w:r><w:footnoteReference w:id="%d"/></w:r>`, p.Footnote))
		}
		doc.WriteString(`</w:p>`)
	}
	doc.WriteString(`</w:body></w:document>`)
	styles := `<?xml version="1.0"?><w:styles ` + wns + `>` +
		`<w:style w:styleId="Title"><w:name w:val="Title"/></w:style>` +
		`<w:style w:styleId="Heading1"><w:name w:val="heading 1"/></w:style>` +
		`<w:style w:styleId="Heading2"><w:name w:val="heading 2"/></w:style>` +
		`<w:style w:styleId="Heading3"><w:name w:val="heading 3"/></w:style></w:styles>`
	es := []Entry{
		{Name: "[Content_Types].xml", Data: []byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`)},
		{Name: "word/document.xml", Data: []byte(doc.String())},
		{Name: "word/styles.xml", Data: []byte(styles)},
		{Name: "docProps/core.xml", Data: []byte(`<?xml version="1.0"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>` + esc(o.Title) + `</dc:title><dc:creator>` + esc(o.Author) + `</dc:creator></cp:coreProperties>`)},
	}
	if len(o.Footnotes) > 0 {
		var fn strings.Builder
		fn.WriteString(`<?xml version="1.0"?><w:footnotes ` + wns + `><w:footnote w:type="separator" w:id="-1"><w:p><w:r><w:t>---</w:t></w:r></w:p></w:footnote>`)
		for i, f := range o.Footnotes {
			fn.WriteString(fmt.Sprintf(`<w:footnote w:id="%d"><w:p><w:r><w:t>%s</w:t></w:r></w:p></w:footnote>`, i+1, esc(f)))
		}
		fn.WriteString(`</w:footnotes>`)
		es = append(es, Entry{Name: "word/footnotes.xml", Data: []byte(fn.String())})
	}
	return Zip(t, es)
}

// OLE returns the header of an old binary Office container, which is how a protected Word file looks.
func OLE() []byte {
	b := []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	return append(b, make([]byte, 504)...)
}
