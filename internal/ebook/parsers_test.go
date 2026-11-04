package ebook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// readBytes writes data to a temp file named name and reads it.
func readBytes(t *testing.T, name string, data []byte, lim Limits) (*Doc, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return Read(context.Background(), p, ExtOf(name), lim)
}

func mustRead(t *testing.T, name string, data []byte) *Doc {
	t.Helper()
	d, err := readBytes(t, name, data, DefaultLimits())
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return d
}

func chapterTitles(d *Doc) []string {
	var out []string
	for _, c := range d.Chapters {
		out = append(out, c.Title)
	}
	return out
}

func kindsOf(c Chapter) string {
	var ks []string
	for _, b := range c.Blocks {
		ks = append(ks, b.Kind)
	}
	return strings.Join(ks, ",")
}

const mdSample = `# The Lantern Keeper

Mara climbed the *north stair* and found **nothing** but a lamp.

> A quote from nowhere in particular.

- first item
- second item

` + "```" + `
go run lamp
` + "```" + `

---

## Second Light

Dr. Voss arrived at dawn. Then she left.

## Third Light

The end came quietly.
`

const txtSample = `Chapter 1

The kettle was loud. Nobody answered.

Chapter 2

It rained for three days.
`

const htmlSample = `<!DOCTYPE html><html><head><title>Field Notes</title><meta name="author" content="Ines Ortega"><style>p{color:red}</style><script>var x=1;</script></head>
<body><h1>First</h1><p>Alpha <em>beta</em> gamma.</p><img src="http://example.com/x.png" alt="remote"><h1>Second</h1><p>Delta &amp; epsilon.</p></body></html>`

func TestEbookParsers(t *testing.T) {
	longPara := strings.Repeat("The river kept moving. ", 20)
	epub := func(v int, pages int) []byte {
		return ebooktest.EPUB(t, ebooktest.EPUBOptions{
			Version: v, Title: "The Salt Road", Authors: []string{"Lena Park", "Omar Diaz"}, Lang: "en-GB",
			ISBN: "9780306406157", PageList: pages,
			Chapters: []ebooktest.EPUBChapter{
				{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Salt Road"}},
				{Title: "Departure", EpubType: "chapter", Paras: []string{longPara, "Second paragraph here."}},
				{Title: "Arrival", EpubType: "chapter", Paras: []string{"They arrived. It was late."}},
			},
		})
	}
	cases := []struct {
		name     string
		file     string
		data     []byte
		title    string
		authors  []string
		chapters []string
		kinds    string // block kinds of the first non-front chapter
		first    string // text of the first block of the first non-front chapter
		pages    int
	}{
		{"epub2", "a.epub", epub(2, 0), "The Salt Road", []string{"Lena Park", "Omar Diaz"}, []string{"Title Page", "Departure", "Arrival"}, "h,p,p", "Departure", 0},
		{"epub3", "a.epub", epub(3, 0), "The Salt Road", []string{"Lena Park", "Omar Diaz"}, []string{"Title Page", "Departure", "Arrival"}, "h,p,p", "Departure", 0},
		{"epub3 page-list", "a.epub", epub(3, 7), "The Salt Road", []string{"Lena Park", "Omar Diaz"}, []string{"Title Page", "Departure", "Arrival"}, "h,p,p", "Departure", 7},
		{"txt", "a.txt", []byte(txtSample), "", nil, []string{"Chapter 1", "Chapter 2"}, "h,p", "Chapter 1", 0},
		{"md", "a.md", []byte(mdSample), "The Lantern Keeper", nil, []string{"", "Second Light", "Third Light"}, "h,p", "The Lantern Keeper", 0},
		{"html", "a.html", []byte(htmlSample), "Field Notes", []string{"Ines Ortega"}, []string{"First", "Second"}, "h,p", "First", 0},
		{"docx", "a.docx", ebooktest.DOCX(t, ebooktest.DocxOptions{Title: "Ledger", Author: "Pia Roy", Paras: []ebooktest.DocxPara{
			ebooktest.Para("Heading1", "Opening"), ebooktest.Para("", "Body of the opening."), ebooktest.Para("Heading2", "A sub"),
			ebooktest.Para("Heading1", "Closing"), ebooktest.Para("", "Last words.")}}), "Ledger", []string{"Pia Roy"}, []string{"Opening", "Closing"}, "h,p,h", "Opening", 0},
		{"fb2 utf-8", "a.fb2", ebooktest.FB2(ebooktest.FB2Options{Title: "Moss", Authors: []ebooktest.FB2Author{{First: "Kai", Last: "Lund"}}, Lang: "en", ISBN: "0-306-40615-2",
			Sections: []ebooktest.FB2Section{{Title: "Rise", Paras: []string{"One. Two."}}, {Title: "Fall", Paras: []string{"Three."}, Sub: []ebooktest.FB2Section{{Title: "Deeper", Paras: []string{"Four."}}}}}}),
			"Moss", []string{"Kai Lund"}, []string{"Rise", "Fall"}, "h,p", "Rise", 0},
		{"fb2 windows-1251", "a.fb2", ebooktest.FB2(ebooktest.FB2Options{Encoding: "windows-1251", Title: "Привет", Authors: []ebooktest.FB2Author{{First: "Иван", Last: "Петров"}},
			Sections: []ebooktest.FB2Section{{Title: "Начало", Paras: []string{"Первый абзац."}}}}),
			"Привет", []string{"Иван Петров"}, []string{"Начало"}, "h,p", "Начало", 0},
		{"fb2 zip", "a.fb2.zip", ebooktest.FB2Zip(t, "book.fb2", ebooktest.FB2(ebooktest.FB2Options{Title: "Zipped", Sections: []ebooktest.FB2Section{{Title: "Only", Paras: []string{"Text."}}}})),
			"Zipped", nil, []string{"Only"}, "h,p", "Only", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := mustRead(t, c.file, c.data)
			if d.Meta.Title != c.title {
				t.Errorf("title = %q, want %q", d.Meta.Title, c.title)
			}
			if strings.Join(d.Meta.Authors, "|") != strings.Join(c.authors, "|") {
				t.Errorf("authors = %q, want %q", d.Meta.Authors, c.authors)
			}
			if got := chapterTitles(d); strings.Join(got, "|") != strings.Join(c.chapters, "|") {
				t.Errorf("chapters = %q, want %q", got, c.chapters)
			}
			var ch Chapter
			for _, x := range d.Chapters {
				if !x.Front {
					ch = x
					break
				}
			}
			if len(ch.Blocks) == 0 {
				t.Fatalf("no body chapter in %+v", chapterTitles(d))
			}
			if got := kindsOf(ch); !strings.HasPrefix(got, c.kinds) {
				t.Errorf("kinds = %q, want prefix %q", got, c.kinds)
			}
			if ch.Blocks[0].Text != c.first {
				t.Errorf("first block = %q, want %q", ch.Blocks[0].Text, c.first)
			}
			if c.pages != 0 && d.PageCount != c.pages {
				t.Errorf("pageCount = %d, want %d", d.PageCount, c.pages)
			}
			if d.V != 1 || d.Recipe != Recipe || len(d.SourceSHA256) != 64 {
				t.Errorf("header = %d %q %q", d.V, d.Recipe, d.SourceSHA256)
			}
		})
	}
}

func TestEbookParserDetails(t *testing.T) {
	t.Run("epub identifiers and front", func(t *testing.T) {
		d := mustRead(t, "a.epub", ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "T", ISBN: "9780306406157",
			Chapters: []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{"T"}}, {Title: "One", Paras: []string{"Body."}}}}))
		if d.Meta.Identifiers.ISBN != "9780306406157" {
			t.Errorf("isbn = %q", d.Meta.Identifiers.ISBN)
		}
		if !d.Chapters[0].Front || d.Chapters[1].Front {
			t.Errorf("front = %v %v", d.Chapters[0].Front, d.Chapters[1].Front)
		}
		if d.Chapters[1].Blocks[1].ID != "c2.p2" {
			t.Errorf("id = %q", d.Chapters[1].Blocks[1].ID)
		}
		if d.Chapters[1].Src != "OEBPS/ch2.xhtml" {
			t.Errorf("src = %q", d.Chapters[1].Src)
		}
	})
	t.Run("epub image and note", func(t *testing.T) {
		raw := `<h1>Pictures</h1><p>See <em>this</em> now.</p><img src="img/a.png" alt="A small map"/><img src="../../outside.png" alt="x"/><aside epub:type="footnote"><p>A note.</p></aside><hr/>`
		d := mustRead(t, "a.epub", ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "T", Chapters: []ebooktest.EPUBChapter{{Title: "Pictures", Raw: raw}}}))
		bs := d.Chapters[0].Blocks
		if got := kindsOf(d.Chapters[0]); got != "h,p,img,note,hr" {
			t.Fatalf("kinds = %q", got)
		}
		if bs[1].Runs[0] != (Run{4, 8, "em"}) {
			t.Errorf("runs = %+v", bs[1].Runs)
		}
		if bs[2].Img != "OEBPS/img/a.png" || bs[2].Text != "A small map" {
			t.Errorf("img = %q %q", bs[2].Img, bs[2].Text)
		}
		if bs[1].Src == nil || bs[1].Src.Part != "OEBPS/ch1.xhtml" || len(bs[1].Src.Map) == 0 || bs[1].Src.End <= bs[1].Src.Start {
			t.Errorf("src = %+v", bs[1].Src)
		}
	})
	t.Run("md inline and structure", func(t *testing.T) {
		d := mustRead(t, "a.md", []byte(mdSample))
		if got := kindsOf(d.Chapters[0]); got != "h,p,quote,li,li,pre,hr" {
			t.Errorf("kinds = %q", got)
		}
		p := d.Chapters[0].Blocks[1]
		if len(p.Runs) != 2 || p.Runs[0].Style != "em" || p.Runs[1].Style != "strong" {
			t.Errorf("runs = %+v", p.Runs)
		}
		if p.Text != "Mara climbed the north stair and found nothing but a lamp." {
			t.Errorf("text = %q", p.Text)
		}
		if d.Chapters[0].Blocks[2].Level != 0 || d.Chapters[0].Blocks[0].Level != 1 {
			t.Errorf("levels")
		}
	})
	t.Run("txt encodings", func(t *testing.T) {
		d := mustRead(t, "a.txt", []byte("Caf\xe9 au lait.\n"))
		if d.Chapters[0].Blocks[0].Text != "Café au lait." {
			t.Errorf("1252 = %q", d.Chapters[0].Blocks[0].Text)
		}
		u16 := []byte{0xFF, 0xFE, 'H', 0, 'i', 0, '.', 0}
		d = mustRead(t, "b.txt", u16)
		if d.Chapters[0].Blocks[0].Text != "Hi." {
			t.Errorf("utf16 = %q", d.Chapters[0].Blocks[0].Text)
		}
	})
	t.Run("docx footnote and styles", func(t *testing.T) {
		d := mustRead(t, "a.docx", ebooktest.DOCX(t, ebooktest.DocxOptions{Paras: []ebooktest.DocxPara{
			ebooktest.Para("Heading1", "One"),
			{Runs: []ebooktest.DocxRun{{Text: "Plain "}, {Text: "slanted", Em: true}, {Text: " bold", Strong: true}}, Footnote: 1},
			{Runs: []ebooktest.DocxRun{{Text: "item"}}, Numbered: true},
			ebooktest.Para("Heading1", "Two"), ebooktest.Para("", "Later.")}, Footnotes: []string{"Cited once."}}))
		if got := kindsOf(d.Chapters[0]); got != "h,p,li,note" {
			t.Errorf("kinds = %q", got)
		}
		if len(d.Chapters[1].Blocks) != 2 {
			t.Errorf("notes leaked into chapter 2")
		}
		if r := d.Chapters[0].Blocks[1].Runs; len(r) != 2 || r[0].Style != "em" || r[1].Style != "strong" {
			t.Errorf("runs = %+v", r)
		}
	})
	t.Run("fb2 notes chapter", func(t *testing.T) {
		d := mustRead(t, "a.fb2", ebooktest.FB2(ebooktest.FB2Options{Title: "N", Sections: []ebooktest.FB2Section{{Title: "A", Paras: []string{"Text."}}}, Notes: []string{"First note.", "Second note."}}))
		last := d.Chapters[len(d.Chapters)-1]
		if last.Title != "Notes" || kindsOf(last) != "note,note" {
			t.Errorf("last = %q %q", last.Title, kindsOf(last))
		}
	})
	t.Run("html drops remote images script style", func(t *testing.T) {
		d := mustRead(t, "a.html", []byte(htmlSample))
		for _, c := range d.Chapters {
			for _, b := range c.Blocks {
				if b.Kind == KindImg || strings.Contains(b.Text, "color") || strings.Contains(b.Text, "var x") {
					t.Errorf("leaked block %+v", b)
				}
			}
		}
	})
	t.Run("empty file is no_text", func(t *testing.T) {
		_, err := readBytes(t, "a.txt", []byte("  \n\n"), DefaultLimits())
		if r, ok := err.(*Refusal); !ok || r.Reason != ReasonNoText || r.Code != "empty" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("zip epub without spine", func(t *testing.T) {
		_, err := readBytes(t, "a.epub", ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "T"}), DefaultLimits())
		if r, ok := err.(*Refusal); !ok || r.Code != "no_spine" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not a zip", func(t *testing.T) {
		_, err := readBytes(t, "a.epub", []byte("plain"), DefaultLimits())
		if r, ok := err.(*Refusal); !ok || r.Reason != ReasonDamaged {
			t.Errorf("err = %v", err)
		}
	})
}

func TestEbookExt(t *testing.T) {
	for in, want := range map[string]string{"A.EPUB": "epub", "x.fb2.zip": "fb2.zip", "x.KFX-zip": "kfx-zip", "x.md": "md", "x.mp3": "", "x": "", "dir.epub/x.txt": "txt", "x.azw3": "azw3"} {
		if got := ExtOf(in); got != want {
			t.Errorf("ExtOf(%q) = %q, want %q", in, got, want)
		}
	}
	if FormatLabel("docx") != "Word" || FormatLabel("txt") != "Plain text" || FormatLabel("md") != "Markdown" {
		t.Error("labels")
	}
}
