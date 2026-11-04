package ebook

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// mobiMarkup is two chapters of invented text, long enough for several 4096-byte records.
func mobiMarkup(paras int) string {
	var sb strings.Builder
	sb.WriteString("<html><head><title>The Lantern Keeper</title></head><body>")
	for c := 1; c <= 2; c++ {
		fmt.Fprintf(&sb, "<h1>Chapter %d</h1>", c)
		for i := 1; i <= paras; i++ {
			fmt.Fprintf(&sb, "<p>Paragraph %d of chapter %d: the keeper climbed the north stair, lit the lamp and listened to the sea. It was café weather.</p>", i, c)
		}
	}
	sb.WriteString("</body></html>")
	return sb.String()
}

func checkMobiDoc(t *testing.T, name string, d *Doc, wantFormat string) {
	t.Helper()
	if d.Format != wantFormat {
		t.Errorf("%s: format = %s", name, d.Format)
	}
	if got := chapterTitles(d); len(got) != 2 || got[0] != "Chapter 1" || got[1] != "Chapter 2" {
		t.Fatalf("%s: chapters = %q", name, got)
	}
	if k := kindsOf(d.Chapters[0]); !strings.HasPrefix(k, "h,p,p") {
		t.Errorf("%s: kinds = %s", name, k)
	}
	if txt := docText(d); !strings.Contains(txt, "Paragraph 12 of chapter 2: the keeper climbed the north stair, lit the lamp and listened to the sea. It was café weather.") {
		t.Errorf("%s: text lost", name)
	}
}

func TestMOBIParse(t *testing.T) {
	markup := mobiMarkup(30)
	t.Run("uncompressed", func(t *testing.T) {
		d := mustRead(t, "a.mobi", ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: markup, Compression: 1, Title: "Keeper", Author: "Ana Pérez", ISBN: "9780000000002", Lang: "en"}))
		checkMobiDoc(t, "uncompressed", d, "mobi")
		if d.Meta.Title != "Keeper" || len(d.Meta.Authors) != 1 || d.Meta.Authors[0] != "Ana Pérez" || d.Meta.Identifiers.ISBN != "9780000000002" || d.Meta.Language != "en" {
			t.Errorf("meta = %+v", d.Meta)
		}
	})
	t.Run("lz77", func(t *testing.T) {
		data := ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: markup, ASIN: "B0ABCDEFGH"})
		d := mustRead(t, "b.mobi", data)
		checkMobiDoc(t, "lz77", d, "mobi")
		if d.Meta.Identifiers.ASIN != "B0ABCDEFGH" {
			t.Errorf("asin = %q", d.Meta.Identifiers.ASIN)
		}
		if len(data) >= len(markup) {
			t.Errorf("not compressed: %d >= %d", len(data), len(markup))
		}
	})
	t.Run("cp1252 and trailing entries", func(t *testing.T) {
		d := mustRead(t, "c.mobi", ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: markup, CP1252: true, Trailing: true}))
		checkMobiDoc(t, "cp1252", d, "mobi")
		d = mustRead(t, "c2.mobi", ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: markup, Compression: 1, Trailing: true}))
		checkMobiDoc(t, "trailing", d, "mobi")
	})
	t.Run("azw3", func(t *testing.T) {
		d := mustRead(t, "d.azw3", ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: markup, Version: 8, Title: "Keeper"}))
		checkMobiDoc(t, "azw3", d, "azw3")
	})
	t.Run("combined uses the KF8 part", func(t *testing.T) {
		old := strings.ReplaceAll(markup, "keeper", "MOBI6ONLY")
		d := mustRead(t, "e.azw3", ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: old, KF8Markup: markup, Combined: true, Title: "Keeper"}))
		checkMobiDoc(t, "combined", d, "azw3")
		if strings.Contains(docText(d), "MOBI6ONLY") {
			t.Error("combined: read the MOBI 6 part")
		}
	})
	t.Run("page breaks start chapters", func(t *testing.T) {
		m := "<html><body><p>Opening words of the first part, said plainly.</p><mbp:pagebreak/><h2>Second Part</h2><p>More words follow here in the second part.</p></body></html>"
		d := mustRead(t, "f.mobi", ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: m}))
		if got := chapterTitles(d); len(got) != 2 || got[0] != "" || got[1] != "Second Part" {
			t.Errorf("chapters = %q", got)
		}
	})
}

func TestMOBIRefusals(t *testing.T) {
	for _, enc := range []int{1, 2} {
		_, err := readBytes(t, "x.mobi", ebooktest.MOBI(t, ebooktest.MOBIOptions{Encryption: enc}), DefaultLimits())
		wantRefusal(t, err, ReasonDRM, "mobi_encrypted")
	}
	_, err := readBytes(t, "x.azw3", ebooktest.MOBI(t, ebooktest.MOBIOptions{Encryption: 2, Version: 8}), DefaultLimits())
	wantRefusal(t, err, ReasonDRM, "mobi_encrypted")
	// Encrypted KF8 part inside a combined file.
	_, err = readBytes(t, "y.azw3", ebooktest.MOBI(t, ebooktest.MOBIOptions{Encryption: 2, Combined: true}), DefaultLimits())
	wantRefusal(t, err, ReasonDRM, "mobi_encrypted")
	_, err = readBytes(t, "h.mobi", ebooktest.MOBI(t, ebooktest.MOBIOptions{Compression: 17480}), DefaultLimits())
	wantRefusal(t, err, ReasonUnsupported, "huffcdic")
	_, err = readBytes(t, "k.kfx", []byte("CONT\x00\x00"), DefaultLimits())
	wantRefusal(t, err, ReasonDRM, "kfx")
}

func TestMOBIHostile(t *testing.T) {
	good := ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(5)})
	cases := map[string][]byte{
		"offset past end":      ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(5), RecordOffsetPastEnd: true}),
		"backref before start": ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(5), BadBackref: true}),
		"text length lie":      ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(5), DeclaredTextLength: 1 << 20}),
		"text length huge":     ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(5), DeclaredTextLength: 0xfffffff0}),
		"text length small":    ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(5), DeclaredTextLength: 50}),
		"truncated":            good[:len(good)/3],
		"header only":          good[:70],
	}
	// A record table that claims more records than the file holds.
	many := append([]byte(nil), good...)
	many[76], many[77] = 0xff, 0xff
	cases["record count lie"] = many
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readBytes(t, "h.mobi", data, DefaultLimits())
			r, ok := err.(*Refusal)
			if !ok || r.Reason != ReasonDamaged {
				t.Fatalf("err = %v (%T), want a damaged refusal", err, err)
			}
		})
	}
	// Every prefix of a small file is refused or read, never a panic (Read would report parser_panic).
	small := ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(1), Trailing: true})
	for n := 0; n < len(small); n += 7 {
		_, err := readBytes(t, "p.mobi", small[:n], DefaultLimits())
		if r, ok := err.(*Refusal); ok && r.Code == "parser_panic" {
			t.Fatalf("prefix %d panicked", n)
		}
	}
}

func TestMOBIDeterministic(t *testing.T) {
	data := ebooktest.MOBI(t, ebooktest.MOBIOptions{Markup: mobiMarkup(20), Title: "Keeper", Author: "Ana"})
	var first []byte
	for i := 0; i < 3; i++ {
		enc, err := Encode(mustRead(t, "d.mobi", data))
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = enc
		} else if !bytes.Equal(first, enc) {
			t.Fatal("document differs between runs")
		}
	}
}
