package ebook

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// dv2EPUB builds a two-chapter EPUB: chapter 1 is raw body markup, chapter 2 holds a note and a heading.
func dv2EPUB(t *testing.T, body1 string, o ebooktest.EPUBOptions) []byte {
	t.Helper()
	o.Version = 3
	o.Title = "Links"
	o.Chapters = []ebooktest.EPUBChapter{
		{Raw: body1},
		{Raw: `<h2 id="sec">Second</h2><p>Body of the second chapter.</p><aside epub:type="footnote" id="n1"><p>The note text.</p></aside>`},
	}
	return ebooktest.EPUB(t, o)
}

func blockWith(d *Doc, text string) (Block, bool) {
	for _, c := range d.Chapters {
		for _, b := range c.Blocks {
			if strings.Contains(b.Text, text) {
				return b, true
			}
		}
	}
	return Block{}, false
}

func TestDocV2Links(t *testing.T) {
	body := `<p>Alpha<a epub:type="noteref" href="ch2.xhtml#n1">1</a> beta <a href="ch2.xhtml#sec">jump</a> gamma ` +
		`<a href="https://example.com/x">out</a> delta <a href="javascript:alert(1)">evil</a> eps <a href="#nowhere">gone</a> ` +
		`<sup><a href="ch2.xhtml#n1">2</a></sup> zeta <a href="mailto:a@b.c">mail</a> eta <a href="data:text/html,x">data</a> ` +
		`<span>spans</span> stay <a href="#here">same</a></p><p id="here">Target paragraph.</p>`
	d := mustRead(t, "links.epub", dv2EPUB(t, body, ebooktest.EPUBOptions{}))
	b, ok := blockWith(d, "Alpha")
	if !ok {
		t.Fatal("block not found")
	}
	if !strings.Contains(b.Text, "spans stay") {
		t.Errorf("an inline span split the paragraph: %q", b.Text)
	}
	type want struct {
		text, to, url string
		note          bool
	}
	got := map[string]Link{}
	for _, l := range b.Links {
		got[slice16(b.Text, [2]int{l.Start, l.End})] = l
	}
	ch2, _ := blockWith(d, "Second")
	note, _ := blockWith(d, "The note text.")
	here, _ := blockWith(d, "Target paragraph.")
	for _, w := range []want{
		{"1", note.ID, "", true},
		{"jump", ch2.ID, "", false},
		{"out", "", "https://example.com/x", false},
		{"2", note.ID, "", true},
		{"mail", "", "mailto:a@b.c", false},
		{"same", here.ID, "", false},
	} {
		l, ok := got[w.text]
		if !ok {
			t.Errorf("link %q missing; have %v", w.text, got)
			continue
		}
		if l.To != w.to || l.URL != w.url || l.Note != w.note {
			t.Errorf("link %q = %+v, want to=%q url=%q note=%v", w.text, l, w.to, w.url, w.note)
		}
	}
	for _, bad := range []string{"evil", "gone", "data"} {
		if _, ok := got[bad]; ok {
			t.Errorf("link %q should have been dropped", bad)
		}
		if !strings.Contains(b.Text, bad) {
			t.Errorf("text of dropped link %q vanished", bad)
		}
	}
	js, _ := Encode(d)
	if bytes.Contains(js, []byte("javascript:")) || bytes.Contains(js, []byte("data:text")) {
		t.Error("a banned scheme reached the document")
	}
}

func TestDocV2Determinism(t *testing.T) {
	body := `<p>Alpha<a epub:type="noteref" href="ch2.xhtml#n1">1</a> and <a href="https://x.test/">x</a>.</p>`
	data := dv2EPUB(t, body, ebooktest.EPUBOptions{MetaXML: `<dc:publisher>P</dc:publisher><dc:date>2019-05-01</dc:date>`})
	a, b := mustRead(t, "a.epub", data), mustRead(t, "a.epub", data)
	ja, _ := Encode(a)
	jb, _ := Encode(b)
	if !bytes.Equal(ja, jb) || DocSHA(ja) != DocSHA(jb) {
		t.Error("the same input gave different bytes")
	}
	if a.Recipe != "tendshelf-ebook-v2" {
		t.Errorf("recipe %q", a.Recipe)
	}
	// A v1 document (none of the new fields) still loads and hashes the same.
	a.Recipe = "tendshelf-ebook-v1"
	a.Meta = Meta{Title: "t", Authors: []string{}}
	for ci := range a.Chapters {
		for bi := range a.Chapters[ci].Blocks {
			a.Chapters[ci].Blocks[bi].Links = nil
		}
	}
	dir := t.TempDir()
	js, _ := Encode(a)
	sha, err := WriteDoc(dir, js)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDoc(dir, sha); err != nil {
		t.Errorf("v1 shaped document refused: %v", err)
	}
}

func tinyPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.White)
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestDocV2MetaImages(t *testing.T) {
	webp := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"), 0x1f, 0x03, 0x00, 0xc7, 0x00, 0x00) // 800x200
	o := ebooktest.EPUBOptions{
		MetaXML: `<dc:description>&lt;p&gt;A &lt;b&gt;bold&lt;/b&gt; tale.&lt;/p&gt;</dc:description><dc:publisher>Salt House</dc:publisher>` +
			`<dc:date>2019-05-01</dc:date><meta name="calibre:series" content="The Road"/><meta name="calibre:series_index" content="2"/>` +
			`<meta name="cover" content="cov"/>`,
		ManifestXML: `<item id="cov" href="cover.png" media-type="image/png"/><item id="w" href="w.webp" media-type="image/webp"/>`,
		Extra: []ebooktest.Entry{
			{Name: "OEBPS/cover.png", Data: tinyPNG(3, 2)},
			{Name: "OEBPS/w.webp", Data: webp},
		},
	}
	d := mustRead(t, "meta.epub", dv2EPUB(t, `<p>Text.</p><img src="cover.png" alt="Cover"/><img src="w.webp" alt="Wide"/>`, o))
	m := d.Meta
	if m.Description != "A bold tale." || m.Publisher != "Salt House" || m.Year != 2019 || m.Series != "The Road" || m.SeriesIndex != "2" || m.CoverPart != "OEBPS/cover.png" {
		t.Errorf("meta = %+v", m)
	}
	png1, ok := blockWith(d, "Cover")
	if !ok || png1.ImgW != 3 || png1.ImgH != 2 {
		t.Errorf("png size = %dx%d (found %v)", png1.ImgW, png1.ImgH, ok)
	}
	wp, ok := blockWith(d, "Wide")
	if !ok || wp.ImgW != 800 || wp.ImgH != 200 {
		t.Errorf("webp size = %dx%d (found %v)", wp.ImgW, wp.ImgH, ok)
	}
	if got := ImageKind([]byte("<svg xmlns='http://www.w3.org/2000/svg'><script/></svg>")); got != "" {
		t.Errorf("svg sniffed as %q", got)
	}
	// EPUB 3 collection metadata.
	o3 := ebooktest.EPUBOptions{MetaXML: `<meta property="belongs-to-collection" id="c1">Saga</meta><meta refines="#c1" property="group-position">3</meta>`}
	d3 := mustRead(t, "col.epub", dv2EPUB(t, `<p>Text.</p>`, o3))
	if d3.Meta.Series != "Saga" || d3.Meta.SeriesIndex != "3" {
		t.Errorf("collection = %q %q", d3.Meta.Series, d3.Meta.SeriesIndex)
	}
}

func TestDocV2Reanchor(t *testing.T) {
	long := "The harbour was quiet. The harbour was cold. A gull called once."
	d := &Doc{Chapters: []Chapter{
		{Idx: 1, Blocks: []Block{
			{ID: "c1.p1", Kind: KindP, Text: "Opening line here.", Sentences: [][2]int{{0, 18}}},
			{ID: "c1.p2", Kind: KindP, Text: long, Sentences: [][2]int{{0, 22}, {23, 44}, {45, 64}}},
		}},
		{Idx: 2, Blocks: []Block{
			{ID: "c2.p1", Kind: KindP, Text: "Only in chapter two: lantern keeper.", Sentences: [][2]int{{0, 36}}},
			{ID: "c2.p2", Kind: KindP, Text: "A gull called once.", Sentences: [][2]int{{0, 19}}},
		}},
	}}
	cases := []struct {
		name          string
		quote, pre, s string
		near          Anchor
		want          Anchor
		ok            bool
	}{
		{"exact at near", "gull called", "", "", Anchor{"c1.p2.s2", 47}, Anchor{"c1.p2.s2", 47}, true},
		{"tail found in the chapter first", "A gull called once.", "", "", Anchor{"c1.p1.s0", 3}, Anchor{"c1.p2.s2", 45}, true},
		{"unique in the chapter", "Opening line", "", "", Anchor{"c1.p2.s0", 5}, Anchor{"c1.p1.s0", 0}, true},
		{"unique in the book", "lantern keeper", "", "", Anchor{"c1.p1.s0", 0}, Anchor{"c2.p1.s0", 21}, true},
		{"repeated, context picks", "The harbour was", "", " cold", Anchor{"c1.p1.s0", 0}, Anchor{"c1.p2.s1", 23}, true},
		{"repeated, no context", "The harbour was", "", "", Anchor{"c1.p1.s0", 0}, Anchor{}, false},
		{"gone", "never written", "", "", Anchor{"c1.p1.s0", 0}, Anchor{}, false},
		{"bad sid still searches the book", "lantern keeper", "", "", Anchor{"zz", 0}, Anchor{"c2.p1.s0", 21}, true},
		{"empty quote", "", "", "", Anchor{"c1.p1.s0", 0}, Anchor{}, false},
	}
	for _, c := range cases {
		got, ok := Reanchor(d, c.quote, c.pre, c.s, c.near)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
