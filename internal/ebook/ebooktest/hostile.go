package ebooktest

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Sentinel is text that must never reach a document built from a hostile file.
const Sentinel = "SENTINEL-OUTSIDE-THE-BOOK"

func good(t testing.TB) EPUBOptions {
	return EPUBOptions{Version: 3, Title: "Hostile Test", Chapters: []EPUBChapter{{Title: "One", Paras: []string{"A short invented paragraph."}}}}
}

// ZipBomb is an EPUB with a 4 MiB entry of zeros that deflates to a few kilobytes.
func ZipBomb(t testing.TB) []byte {
	o := good(t)
	o.Extra = []Entry{{Name: "OEBPS/bomb.bin", Data: make([]byte, 4<<20)}}
	return EPUB(t, o)
}

// ManyEntries is an EPUB with n extra, empty entries.
func ManyEntries(t testing.TB, n int) []byte {
	o := good(t)
	for i := 0; i < n; i++ {
		o.Extra = append(o.Extra, Entry{Name: fmt.Sprintf("OEBPS/x/%d.txt", i)})
	}
	return EPUB(t, o)
}

// TraversalEPUB is a book with one good chapter whose spine also names documents outside the zip
// root and entries called "../evil.xhtml" that holds Sentinel.
func TraversalEPUB(t testing.TB) []byte {
	es := []Entry{
		{Name: "mimetype", Data: []byte("application/epub+zip")},
		{Name: "META-INF/container.xml", Data: []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)},
		{Name: "OEBPS/content.opf", Data: []byte(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Traversal</dc:title></metadata><manifest>` +
			`<item id="a" href="a.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="b" href="../evil.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="c" href="/abs.xhtml" media-type="application/xhtml+xml"/>` +
			`<item id="d" href="../../evil.xhtml" media-type="application/xhtml+xml"/></manifest>` +
			`<spine><itemref idref="a"/><itemref idref="b"/><itemref idref="c"/><itemref idref="d"/></spine></package>`)},
		{Name: "OEBPS/a.xhtml", Data: []byte(`<html><body><h1>Inside</h1><p>Only this belongs to the book.</p></body></html>`)},
		{Name: "../evil.xhtml", Data: []byte(`<html><body><p>` + Sentinel + `</p></body></html>`)},
		{Name: "/abs.xhtml", Data: []byte(`<html><body><p>` + Sentinel + `</p></body></html>`)},
	}
	return Zip(t, es)
}

// laughs is a "billion laughs" DOCTYPE: nested entities that would expand to gigabytes.
const laughs = `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;"><!ENTITY lol3 "&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;&lol2;"><!ENTITY lol4 "&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;&lol3;"><!ENTITY lol5 "&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;&lol4;">]>`

// EntityEPUB carries a billion-laughs DOCTYPE in its package file and in a chapter.
func EntityEPUB(t testing.TB) []byte {
	es := []Entry{
		{Name: "mimetype", Data: []byte("application/epub+zip")},
		{Name: "META-INF/container.xml", Data: []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)},
		{Name: "OEBPS/content.opf", Data: []byte(`<?xml version="1.0"?>` + laughs + `<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>&lol5;</dc:title></metadata><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/></spine></package>`)},
		{Name: "OEBPS/a.xhtml", Data: []byte(`<?xml version="1.0"?>` + laughs + `<html><body><h1>Laughs</h1><p>Before &lol5; after.</p></body></html>`)},
	}
	return Zip(t, es)
}

// LyingSizeEPUB has a chapter whose header says 100 bytes but whose stream inflates to 2 MiB.
func LyingSizeEPUB(t testing.TB) []byte {
	big := append([]byte("<html><body><p>Short.</p>"), bytes.Repeat([]byte(" "), 2<<20)...)
	es := []Entry{
		{Name: "mimetype", Data: []byte("application/epub+zip")},
		{Name: "META-INF/container.xml", Data: []byte(`<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)},
		{Name: "OEBPS/content.opf", Data: []byte(`<package xmlns="http://www.idpf.org/2007/opf" version="3.0"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:title>Lie</dc:title></metadata><manifest><item id="a" href="a.xhtml" media-type="application/xhtml+xml"/></manifest><spine><itemref idref="a"/></spine></package>`)},
		{Name: "OEBPS/a.xhtml", Data: big, Declared: 100},
	}
	return Zip(t, es)
}

// DeepEPUB has a chapter nested n elements deep.
func DeepEPUB(t testing.TB, n int) []byte {
	o := good(t)
	o.Chapters[0].Raw = strings.Repeat("<div>", n) + "<p>Deep.</p>" + strings.Repeat("</div>", n)
	return EPUB(t, o)
}
