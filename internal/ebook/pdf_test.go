package ebook

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

func docText(d *Doc) string {
	var sb strings.Builder
	for _, c := range d.Chapters {
		for _, b := range c.Blocks {
			sb.WriteString(b.Text + "\n")
		}
	}
	return sb.String()
}

// filler is a plain line of 12 point text that is long enough to count as a full line.
func filler(i int) string {
	return fmt.Sprintf("Line %d of the long quiet passage says nothing at all about anything.", i)
}

func proseLines(first, n int, y float64) []ebooktest.Line {
	var out []ebooktest.Line
	for i := 0; i < n; i++ {
		out = append(out, ebooktest.Line{Text: filler(first + i), X: 72, Y: y - float64(i)*16.8, Size: 12})
	}
	return out
}

func TestPDFSpike(t *testing.T) {
	base := func() ebooktest.PDFOptions {
		return ebooktest.PDFOptions{Title: "Spike", Author: "Ana Pérez", Pages: [][]ebooktest.Line{
			append(proseLines(1, 3, 700), ebooktest.Line{Text: "First page café marker.", X: 72, Y: 600, Size: 12}),
			append(proseLines(4, 3, 700), ebooktest.Line{Text: "Second page marker (real).", X: 72, Y: 600, Size: 12}),
		}}
	}
	mods := map[string]func(*ebooktest.PDFOptions){
		"plain":       func(o *ebooktest.PDFOptions) {},
		"type0":       func(o *ebooktest.PDFOptions) { o.Type0ToUnicode = true },
		"xrefstream":  func(o *ebooktest.PDFOptions) { o.XRefStream = true },
		"incremental": func(o *ebooktest.PDFOptions) { o.Incremental = true },
		"incxref":     func(o *ebooktest.PDFOptions) { o.Incremental = true; o.XRefStream = true },
	}
	for name, mod := range mods {
		t.Run(name, func(t *testing.T) {
			o := base()
			mod(&o)
			// Add enough letters for the no-text check.
			for i := range o.Pages {
				o.Pages[i] = append(o.Pages[i], proseLines(10, 4, 540)...)
			}
			d := mustRead(t, "spike.pdf", ebooktest.PDF(t, o))
			txt := docText(d)
			if !strings.Contains(txt, "Second page marker (real).") {
				t.Errorf("%s: last page text missing: %q", name, txt)
			}
			if strings.Contains(txt, "Superseded") {
				t.Errorf("%s: old version of the page was read", name)
			}
			if name != "incremental" && name != "incxref" && !strings.Contains(txt, "First page café marker.") {
				t.Errorf("%s: first page text missing: %q", name, txt)
			}
			if d.PageCount != 2 || d.Format != "pdf" || d.Layout != "flow" {
				t.Errorf("%s: pageCount=%d format=%s layout=%s", name, d.PageCount, d.Format, d.Layout)
			}
			if d.Meta.Title != "Spike" || len(d.Meta.Authors) != 1 || d.Meta.Authors[0] != "Ana Pérez" {
				t.Errorf("%s: meta = %+v", name, d.Meta)
			}
		})
	}
}

func TestPDFParse(t *testing.T) {
	page1 := []ebooktest.Line{
		{Text: "Chapter One", X: 72, Y: 690, Size: 20},
		{Text: "The keeper walked slowly through the long corri-", X: 72, Y: 650, Size: 12},
		{Text: "dor and lit the lamp before the first guest came.", X: 72, Y: 633.2, Size: 12},
		{Text: "A second paragraph begins here after a clear gap.", X: 72, Y: 590, Size: 12},
		{Text: "It carries on for a second line of plain words.", X: 72, Y: 573.2, Size: 12},
		{Text: "A quiet Subheading", X: 72, Y: 520, Size: 16},
		{Text: "Words under the subheading keep the story going on.", X: 72, Y: 490, Size: 12},
	}
	page2 := []ebooktest.Line{
		{Text: "Chapter Two", X: 72, Y: 690, Size: 20},
		{Text: "Rain fell on the roof of the keeper house all night.", X: 72, Y: 650, Size: 12},
		{Text: "In the morning the harbour was calm and bright again.", X: 72, Y: 633.2, Size: 12},
	}
	page3 := proseLines(1, 4, 690)
	page4 := proseLines(5, 4, 690)
	data := ebooktest.PDF(t, ebooktest.PDFOptions{
		Pages:       [][]ebooktest.Line{page1, page2, page3, page4},
		Header:      "The Lantern Keeper",
		Footer:      "A Tendshelf test book",
		PageNumbers: true,
	})
	d := mustRead(t, "book.pdf", data)
	txt := docText(d)
	for _, gone := range []string{"The Lantern Keeper", "A Tendshelf test book"} {
		if strings.Contains(txt, gone) {
			t.Errorf("header/footer %q kept", gone)
		}
	}
	if !strings.Contains(txt, "through the long corridor and lit the lamp") {
		t.Errorf("hyphen not joined: %q", txt)
	}
	if strings.Contains(txt, "corri-") {
		t.Errorf("hyphen left in text")
	}
	for _, b := range d.Chapters[0].Blocks {
		if b.Text == "1" || b.Text == "2" {
			t.Errorf("page number kept: %q", b.Text)
		}
	}
	if d.Layout != "flow" || d.PageCount != 4 {
		t.Errorf("layout=%s pageCount=%d", d.Layout, d.PageCount)
	}
	if got := chapterTitles(d); len(got) != 2 || got[0] != "Chapter One" || got[1] != "Chapter Two" {
		t.Fatalf("chapters = %q", got)
	}
	// Chapter one: h1, paragraph (joined), paragraph, h2, paragraph.
	if k := kindsOf(d.Chapters[0]); k != "h,p,p,h,p" {
		t.Errorf("chapter 1 kinds = %s", k)
	}
	h := d.Chapters[0].Blocks[3]
	if h.Level != 2 || h.Text != "A quiet Subheading" {
		t.Errorf("subheading = %+v", h)
	}
	if h1 := d.Chapters[0].Blocks[0]; h1.Level != 1 || h1.Src == nil || h1.Src.Part != "page:1" {
		t.Errorf("chapter heading = %+v", h1)
	}
	// The second chapter's body continues over pages 3 and 4 into one paragraph per gap rule.
	if p := d.Chapters[1].Blocks[1]; p.Src == nil || p.Src.Part != "page:2" {
		t.Errorf("paragraph src = %+v", p.Src)
	}
}

func TestPDFLayoutPage(t *testing.T) {
	var lines []ebooktest.Line
	for i := 0; i < 24; i++ {
		lines = append(lines, ebooktest.Line{Text: fmt.Sprintf("Column text number %02d keeps going a little.", i), Size: 12})
	}
	d := mustRead(t, "cols.pdf", ebooktest.PDF(t, ebooktest.PDFOptions{TwoColumns: true, Pages: [][]ebooktest.Line{lines, lines}}))
	if d.Layout != "page" {
		t.Errorf("two columns: layout = %s", d.Layout)
	}
	if first := d.Chapters[0].Blocks[0].Text; first != "Column text number 00 keeps going a little." {
		t.Errorf("first block = %q", first)
	}

	// More than half the lines far shorter than a full line.
	var short []ebooktest.Line
	y := 700.0
	for i := 0; i < 16; i++ {
		short = append(short, ebooktest.Line{Text: "Tide in", X: 72, Y: y, Size: 12})
		y -= 16.8
	}
	for i := 0; i < 6; i++ {
		short = append(short, ebooktest.Line{Text: filler(i), X: 72, Y: y, Size: 12})
		y -= 16.8
	}
	// Letters per page: the plan wants 20 or more; 16 short lines carry 96 and the long ones 250.
	d = mustRead(t, "short.pdf", ebooktest.PDF(t, ebooktest.PDFOptions{Pages: [][]ebooktest.Line{short}}))
	if d.Layout != "page" {
		t.Errorf("short lines: layout = %s", d.Layout)
	}
}

func TestPDFRefusals(t *testing.T) {
	pages := [][]ebooktest.Line{proseLines(1, 8, 700), proseLines(9, 8, 700)}
	_, err := readBytes(t, "enc.pdf", ebooktest.PDF(t, ebooktest.PDFOptions{Pages: pages, Encrypt: true}), DefaultLimits())
	wantRefusal(t, err, ReasonDRM, "pdf_encrypt")
	_, err = readBytes(t, "enc2.pdf", ebooktest.PDF(t, ebooktest.PDFOptions{Pages: pages, Encrypt: true, XRefStream: true}), DefaultLimits())
	wantRefusal(t, err, ReasonDRM, "pdf_encrypt")
	_, err = readBytes(t, "img.pdf", ebooktest.PDF(t, ebooktest.PDFOptions{Pages: pages, ImageOnly: true}), DefaultLimits())
	wantRefusal(t, err, ReasonNoText, "image_only")
	// A name that merely starts with /Encrypt is not the key.
	data := ebooktest.PDF(t, ebooktest.PDFOptions{Pages: pages})
	if hasEncryptMarker(bytes.NewReader(data), int64(len(data))) {
		t.Error("marker found in a plain file")
	}
	if hasEncryptMarker(bytes.NewReader([]byte("/EncryptMetadata true")), 21) {
		t.Error("longer name taken for /Encrypt")
	}
	_, err = readBytes(t, "bad.pdf", []byte("%PDF-1.4\nnot a pdf at all"), DefaultLimits())
	wantRefusal(t, err, ReasonDamaged, "pdf_open")
}

func TestPDFDeterministic(t *testing.T) {
	data := ebooktest.PDF(t, ebooktest.PDFOptions{Pages: [][]ebooktest.Line{proseLines(1, 6, 700), proseLines(7, 6, 700)}, Type0ToUnicode: true})
	var first []byte
	for i := 0; i < 3; i++ {
		d := mustRead(t, "det.pdf", data)
		enc, err := Encode(d)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = enc
		} else if !bytes.Equal(first, enc) {
			t.Fatal("document differs between runs")
		}
	}
	if !bytes.Equal(ebooktest.PDF(t, ebooktest.PDFOptions{Pages: [][]ebooktest.Line{proseLines(1, 2, 700)}}), ebooktest.PDF(t, ebooktest.PDFOptions{Pages: [][]ebooktest.Line{proseLines(1, 2, 700)}})) {
		t.Error("fixture writer is not deterministic")
	}
}
