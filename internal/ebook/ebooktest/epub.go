package ebooktest

import (
	"fmt"
	"html"
	"strings"
	"testing"
)

// EPUBChapter is one spine document.
type EPUBChapter struct {
	Title    string
	EpubType string // e.g. "titlepage", "chapter"; shown in landmarks (v3) or guide (v2)
	Paras    []string
	Raw      string // body markup used instead of Title and Paras when set
}

// EPUBOptions describes a synthetic EPUB.
type EPUBOptions struct {
	Version         int // 2 or 3
	Title           string
	Authors         []string
	Lang            string
	ISBN            string
	Chapters        []EPUBChapter
	PageList        int // number of page-list entries (v3 nav, v2 NCX pageList)
	Rights          bool
	Apple           bool
	LCP             bool
	EncryptAlg      string // adds META-INF/encryption.xml with this algorithm for a chapter file
	FontObfuscation bool   // encryption.xml with the IDPF font algorithm only
	Extra           []Entry
}

func esc(s string) string { return html.EscapeString(s) }

// EPUB builds the file.
func EPUB(t testing.TB, o EPUBOptions) []byte {
	t.Helper()
	if o.Version == 0 {
		o.Version = 3
	}
	if o.Lang == "" {
		o.Lang = "en"
	}
	var es []Entry
	es = append(es, Entry{Name: "mimetype", Data: []byte("application/epub+zip")})
	es = append(es, Entry{Name: "META-INF/container.xml", Data: []byte(`<?xml version="1.0"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)})
	var manifest, spine, guide strings.Builder
	for i, c := range o.Chapters {
		id := fmt.Sprintf("ch%d", i+1)
		manifest.WriteString(fmt.Sprintf(`<item id="%s" href="%s.xhtml" media-type="application/xhtml+xml"/>`, id, id))
		spine.WriteString(fmt.Sprintf(`<itemref idref="%s"/>`, id))
		body := c.Raw
		if body == "" {
			if c.Title != "" {
				body = "<h1>" + esc(c.Title) + "</h1>"
			}
			for _, p := range c.Paras {
				body += "<p>" + esc(p) + "</p>"
			}
		}
		attrs := ""
		if o.Version == 3 && c.EpubType != "" {
			attrs = ` epub:type="` + c.EpubType + `"`
		}
		es = append(es, Entry{Name: "OEBPS/" + id + ".xhtml", Data: []byte(`<?xml version="1.0" encoding="utf-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>x</title></head><body` + attrs + `>` + body + `</body></html>`)})
		if o.Version == 2 && c.EpubType != "" {
			guide.WriteString(fmt.Sprintf(`<reference type="%s" title="%s" href="%s.xhtml"/>`, c.EpubType, esc(c.Title), id))
		}
	}
	var meta strings.Builder
	meta.WriteString(`<dc:title>` + esc(o.Title) + `</dc:title><dc:language>` + o.Lang + `</dc:language>`)
	for _, a := range o.Authors {
		meta.WriteString(`<dc:creator>` + esc(a) + `</dc:creator>`)
	}
	if o.ISBN != "" {
		meta.WriteString(`<dc:identifier>urn:isbn:` + o.ISBN + `</dc:identifier>`)
	}
	meta.WriteString(`<dc:identifier id="uid">urn:uuid:00000000-0000-0000-0000-000000000001</dc:identifier>`)

	var nav strings.Builder
	if o.Version == 3 {
		manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
		nav.WriteString(`<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><body><nav epub:type="toc"><ol>`)
		for i, c := range o.Chapters {
			if c.Title != "" {
				nav.WriteString(fmt.Sprintf(`<li><a href="ch%d.xhtml">%s</a></li>`, i+1, esc(c.Title)))
			}
		}
		nav.WriteString(`</ol></nav>`)
		var lm strings.Builder
		for i, c := range o.Chapters {
			if c.EpubType != "" {
				lm.WriteString(fmt.Sprintf(`<li><a epub:type="%s" href="ch%d.xhtml">x</a></li>`, c.EpubType, i+1))
			}
		}
		if lm.Len() > 0 {
			nav.WriteString(`<nav epub:type="landmarks"><ol>` + lm.String() + `</ol></nav>`)
		}
		if o.PageList > 0 {
			nav.WriteString(`<nav epub:type="page-list"><ol>`)
			for i := 1; i <= o.PageList; i++ {
				nav.WriteString(fmt.Sprintf(`<li><a href="ch1.xhtml#p%d">%d</a></li>`, i, i))
			}
			nav.WriteString(`</ol></nav>`)
		}
		nav.WriteString(`</body></html>`)
		es = append(es, Entry{Name: "OEBPS/nav.xhtml", Data: []byte(nav.String())})
	} else {
		manifest.WriteString(`<item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>`)
		var ncx strings.Builder
		ncx.WriteString(`<?xml version="1.0" encoding="utf-8"?><ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1"><navMap>`)
		n := 0
		for i, c := range o.Chapters {
			if c.Title == "" {
				continue
			}
			n++
			ncx.WriteString(fmt.Sprintf(`<navPoint id="n%d" playOrder="%d"><navLabel><text>%s</text></navLabel><content src="ch%d.xhtml"/></navPoint>`, n, n, esc(c.Title), i+1))
		}
		ncx.WriteString(`</navMap>`)
		if o.PageList > 0 {
			ncx.WriteString(`<pageList>`)
			for i := 1; i <= o.PageList; i++ {
				ncx.WriteString(fmt.Sprintf(`<pageTarget id="pt%d" type="normal" value="%d"><navLabel><text>%d</text></navLabel><content src="ch1.xhtml#p%d"/></pageTarget>`, i, i, i, i))
			}
			ncx.WriteString(`</pageList>`)
		}
		ncx.WriteString(`</ncx>`)
		es = append(es, Entry{Name: "OEBPS/toc.ncx", Data: []byte(ncx.String())})
	}
	g := ""
	if guide.Len() > 0 {
		g = `<guide>` + guide.String() + `</guide>`
	}
	tocAttr := ""
	if o.Version == 2 {
		tocAttr = ` toc="ncx"`
	}
	opf := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?><package xmlns="http://www.idpf.org/2007/opf" version="%d.0" unique-identifier="uid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">%s</metadata><manifest>%s</manifest><spine%s>%s</spine>%s</package>`,
		o.Version, meta.String(), manifest.String(), tocAttr, spine.String(), g)
	es = append(es, Entry{Name: "OEBPS/content.opf", Data: []byte(opf)})

	if o.Rights {
		es = append(es, Entry{Name: "META-INF/rights.xml", Data: []byte(`<rights/>`)})
	}
	if o.Apple {
		es = append(es, Entry{Name: "META-INF/sinf.xml", Data: []byte(`<sinf/>`)})
	}
	if o.LCP {
		es = append(es, Entry{Name: "META-INF/license.lcpl", Data: []byte(`{}`)})
	}
	alg := o.EncryptAlg
	if o.FontObfuscation {
		alg = "http://www.idpf.org/2008/embedding"
	}
	if alg != "" {
		es = append(es, Entry{Name: "META-INF/encryption.xml", Data: []byte(`<?xml version="1.0"?><encryption xmlns="urn:oasis:names:tc:opendocument:xmlns:container" xmlns:enc="http://www.w3.org/2001/04/xmlenc#"><enc:EncryptedData><enc:EncryptionMethod Algorithm="` + alg + `"/><enc:CipherData><enc:CipherReference URI="OEBPS/ch1.xhtml"/></enc:CipherData></enc:EncryptedData></encryption>`)})
	}
	es = append(es, o.Extra...)
	return Zip(t, es)
}
