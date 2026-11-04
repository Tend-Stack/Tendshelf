package ebook

import (
	"bytes"
	"io"
	"strings"

	"golang.org/x/net/html"
)

// hblock is one block found in an HTML or XHTML part, before it reaches the builder.
type hblock struct {
	Kind  string
	Level int
	Segs  []seg
	Src   SrcRef // Part, Start, End; Map is filled by the builder
	Img   string // raw src of an img block
	Links []rawLink
	Anch  []string // ids found on or inside the block (resolved to part#id by feed)
}

// rawLink is an <a href> as the walker saw it; feed turns it into a Link target.
type rawLink struct {
	Href string
	Note bool
}

// htmlDoc is what the tokenizer walk found in one part.
type htmlDoc struct {
	Title  string
	Meta   map[string]string // <meta name content>, lower-case names, first wins
	Types  []string          // epub:type tokens of body, section and nav, in order
	Blocks []hblock
}

type hframe struct {
	name   string
	kind   bool   // pushed a block kind
	style  string // pushed an inline style
	isNote bool
	block  bool // a container: closing it ends the open block
	link   bool // an <a href>: closing it ends the link
}

var containerTags = map[string]bool{
	"div": true, "section": true, "article": true, "ul": true, "ol": true, "table": true, "tr": true,
	"td": true, "th": true, "figure": true, "nav": true, "header": true, "footer": true, "main": true,
	"body": true, "dl": true, "dt": true, "dd": true, "thead": true, "tbody": true, "tfoot": true,
	"figcaption": true, "aside": true, "caption": true, "address": true,
}

var voidTags = map[string]bool{
	"br": true, "hr": true, "img": true, "meta": true, "link": true, "input": true, "wbr": true,
	"col": true, "area": true, "base": true, "source": true, "track": true, "embed": true,
}

func inlineStyle(name string) string {
	switch name {
	case "em", "i", "cite", "dfn":
		return "em"
	case "strong", "b":
		return "strong"
	case "code", "tt", "kbd", "samp":
		return "code"
	}
	return ""
}

func isNoteType(t string) bool {
	for _, f := range strings.Fields(t) {
		switch f {
		case "footnote", "endnote", "rearnote", "footnotes", "endnotes":
			return true
		}
	}
	return false
}

// isLocalRef reports whether a src is a path inside the book, not a URL.
func isLocalRef(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "#") {
		return false
	}
	if i := strings.IndexAny(s, ":/?#"); i > 0 && s[i] == ':' {
		return false // a scheme: http:, https:, data:, file:
	}
	return true
}

// walkHTML walks data with the x/net/html tokenizer, which keeps byte offsets for src maps.
func walkHTML(data []byte, part string, lim Limits) (*htmlDoc, error) {
	z := html.NewTokenizer(bytes.NewReader(data))
	doc := &htmlDoc{Meta: map[string]string{}}
	var frames []hframe
	kinds := []string{KindP}
	level := 0
	var styles []string
	var cur []seg
	curKind, curLevel, curStart := KindP, 0, -1
	begun := false // a block has an owner element or first text; curStart is set once per block
	pos := 0
	skip := "" // script / style: ignore until its end tag
	inHead := false
	inTitle := false
	svgDepth := 0
	var svgTitle strings.Builder
	svgHref, svgInTitle := "", false
	var titleBuf strings.Builder
	var links []rawLink  // every <a href> of the part; seg.Link is index+1
	var linkStack []int  // open links, innermost last
	var pend []string    // ids seen before their block began
	var curAnch []string // ids of the open block
	supDepth := 0

	flush := func(end int) error {
		defer func() { cur, curStart, begun, curAnch = nil, -1, false, nil }()
		if len(cur) == 0 {
			return nil
		}
		if len(doc.Blocks) >= lim.MaxBlocks {
			return damaged("too_many_blocks")
		}
		hb := hblock{Kind: curKind, Level: curLevel, Segs: cur, Anch: curAnch, Src: SrcRef{Part: part, Start: curStart, End: end}}
		hb.Segs, hb.Links = localLinks(cur, links)
		doc.Blocks = append(doc.Blocks, hb)
		return nil
	}
	curStyle := func() string {
		if len(styles) == 0 {
			return ""
		}
		return styles[len(styles)-1]
	}
	begin := func(start int) {
		if !begun {
			begun = true
			curKind = kinds[len(kinds)-1]
			curLevel = level
			curStart = start
			curAnch = append(curAnch, pend...)
			pend = nil
		}
	}
	// takePend gives the ids waiting for a block to a block that is made in one step (img, hr).
	takePend := func() []string {
		a := pend
		pend = nil
		return a
	}
	anchor := func(id string) {
		if id == "" {
			return
		}
		if begun {
			curAnch = append(curAnch, id)
		} else {
			pend = append(pend, id)
		}
	}
	curLink := func() int {
		if len(linkStack) == 0 {
			return 0
		}
		return linkStack[len(linkStack)-1]
	}
	popTo := func(i int, tokEnd int) error {
		for _, f := range frames[i:] {
			if f.style == "" && (f.kind || f.block) {
				if err := flush(tokEnd); err != nil {
					return err
				}
				break
			}
		}
		for len(frames) > i {
			f := frames[len(frames)-1]
			frames = frames[:len(frames)-1]
			if f.kind {
				kinds = kinds[:len(kinds)-1]
				level = 0
			}
			if f.style != "" {
				styles = styles[:len(styles)-1]
			}
			if f.link {
				linkStack = linkStack[:len(linkStack)-1]
			}
			if f.name == "sup" {
				supDepth--
			}
		}
		return nil
	}

	for {
		tt := z.Next()
		raw := z.Raw()
		start, end := pos, pos+len(raw)
		pos = end
		switch tt {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				if err := flush(end); err != nil {
					return nil, err
				}
				doc.Title = normalizeString(titleBuf.String())
				return doc, nil
			}
			return nil, damaged("bad_html")
		case html.TextToken:
			txt := string(z.Text())
			switch {
			case skip != "":
			case svgDepth > 0:
				if svgInTitle {
					svgTitle.WriteString(txt)
				}
			case inTitle:
				titleBuf.WriteString(txt)
			case inHead:
			default:
				if len(cur) == 0 && !begun && kinds[len(kinds)-1] != KindPre && strings.TrimSpace(txt) == "" {
					continue // blank space between blocks starts nothing
				}
				begin(start)
				cur = append(cur, seg{Text: txt, Style: curStyle(), ByteOff: start, Link: curLink()})
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			nameB, hasAttr := z.TagName()
			name := string(nameB)
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				if _, dup := attrs[string(k)]; !dup {
					attrs[string(k)] = string(v)
				}
			}
			if skip != "" {
				if tt == html.EndTagToken && name == skip {
					skip = ""
				}
				continue
			}
			if tt == html.EndTagToken {
				switch name {
				case "head":
					inHead = false
				case "title":
					inTitle = false
					svgInTitle = false
				case "svg":
					if svgDepth > 0 {
						svgDepth--
						if svgDepth == 0 && isLocalRef(svgHref) {
							if err := flush(start); err != nil {
								return nil, err
							}
							doc.Blocks = append(doc.Blocks, hblock{Kind: KindImg, Segs: []seg{{Text: svgTitle.String(), ByteOff: -1}}, Img: svgHref, Anch: takePend(), Src: SrcRef{Part: part, Start: start, End: end}})
						}
					}
				}
				if svgDepth > 0 {
					continue
				}
				for i := len(frames) - 1; i >= 0; i-- {
					if frames[i].name == name {
						if err := popTo(i, end); err != nil {
							return nil, err
						}
						break
					}
				}
				continue
			}
			// start or self-closing tag
			if svgDepth > 0 {
				switch name {
				case "svg":
					svgDepth++
				case "title":
					svgInTitle = tt == html.StartTagToken
				case "image":
					if svgHref == "" {
						svgHref = firstNonEmpty(attrs["xlink:href"], attrs["href"])
						if svgTitle.Len() == 0 {
							svgTitle.WriteString(attrs["aria-label"])
						}
					}
				}
				continue
			}
			switch name {
			case "script", "style":
				if tt == html.StartTagToken {
					skip = name
				}
				continue
			case "head":
				inHead = true
				continue
			case "body":
				inHead = false
				doc.Types = appendTypes(doc.Types, attrs["epub:type"])
			case "title":
				if inHead || len(doc.Blocks) == 0 && len(cur) == 0 {
					inTitle = tt == html.StartTagToken
				}
				continue
			case "meta":
				if n := strings.ToLower(attrs["name"]); n != "" {
					if _, dup := doc.Meta[n]; !dup {
						doc.Meta[n] = attrs["content"]
					}
				}
				continue
			case "svg":
				if err := flush(start); err != nil {
					return nil, err
				}
				svgDepth = 1
				svgTitle.Reset()
				svgHref = ""
				svgInTitle = false
				if tt == html.SelfClosingTagToken {
					svgDepth = 0
				}
				continue
			}
			if inHead {
				continue
			}
			id := firstNonEmpty(attrs["id"], attrs["name"])
			switch {
			case name == "br":
				anchor(id)
				begin(start)
				sep := " "
				if kinds[len(kinds)-1] == KindPre {
					sep = "\n"
				}
				cur = append(cur, seg{Text: sep, Style: curStyle(), ByteOff: -1, Link: curLink()})
			case name == "hr":
				if err := flush(start); err != nil {
					return nil, err
				}
				anchor(id)
				doc.Blocks = append(doc.Blocks, hblock{Kind: KindHR, Anch: takePend(), Src: SrcRef{Part: part, Start: start, End: end}})
			case name == "img":
				if err := flush(start); err != nil {
					return nil, err
				}
				anchor(id)
				if isLocalRef(attrs["src"]) {
					doc.Blocks = append(doc.Blocks, hblock{Kind: KindImg, Segs: []seg{{Text: attrs["alt"], ByteOff: -1}}, Img: attrs["src"], Anch: takePend(), Src: SrcRef{Part: part, Start: start, End: end}})
				}
			case len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6':
				if err := flush(start); err != nil {
					return nil, err
				}
				anchor(id)
				if tt == html.StartTagToken {
					frames = push(frames, hframe{name: name, kind: true}, lim)
					kinds = append(kinds, KindH)
					level = int(name[1] - '0')
					begin(start)
				}
			case name == "p" || name == "blockquote" || name == "li" || name == "pre":
				if err := flush(start); err != nil {
					return nil, err
				}
				anchor(id)
				if tt == html.StartTagToken {
					k := map[string]string{"p": kinds[len(kinds)-1], "blockquote": KindQuote, "li": KindLI, "pre": KindPre}[name]
					if name == "p" && k == KindH {
						k = KindP
					}
					frames = push(frames, hframe{name: name, kind: true}, lim)
					kinds = append(kinds, k)
					level = 0
					begin(start)
				}
			case containerTags[name]:
				if err := flush(start); err != nil {
					return nil, err
				}
				anchor(id)
				if tt == html.StartTagToken {
					f := hframe{name: name, block: true}
					et := attrs["epub:type"]
					if name == "section" || name == "nav" || name == "body" || name == "article" {
						doc.Types = appendTypes(doc.Types, et)
					}
					if (name == "aside" || name == "section") && isNoteType(et) {
						f.kind, f.isNote = true, true
						kinds = append(kinds, KindNote)
						level = 0
					}
					frames = push(frames, f, lim)
				}
			default:
				anchor(id)
				if st := inlineStyle(name); st != "" && tt == html.StartTagToken {
					frames = push(frames, hframe{name: name, style: st}, lim)
					styles = append(styles, st)
				} else if name == "a" && tt == html.StartTagToken && strings.TrimSpace(attrs["href"]) != "" {
					links = append(links, rawLink{
						Href: strings.TrimSpace(attrs["href"]),
						Note: hasToken(attrs["epub:type"], "noteref") || supDepth > 0,
					})
					linkStack = append(linkStack, len(links))
					frames = push(frames, hframe{name: name, link: true}, lim)
				} else if tt == html.StartTagToken && !voidTags[name] {
					if name == "sup" {
						supDepth++
						if n := len(linkStack); n > 0 {
							links[linkStack[n-1]-1].Note = true // <a><sup>1</sup></a>
						}
					}
					frames = push(frames, hframe{name: name}, lim)
				}
			}
			if len(frames) > lim.MaxXMLDepth {
				return nil, damaged("xml_depth")
			}
		}
	}
}

func push(frames []hframe, f hframe, lim Limits) []hframe { return append(frames, f) }

func firstNonEmpty(a ...string) string {
	for _, s := range a {
		if s != "" {
			return s
		}
	}
	return ""
}

func appendTypes(dst []string, v string) []string {
	return append(dst, strings.Fields(v)...)
}

// feed gives the blocks of d to the builder; resolve maps a raw img src to a zip path ("" = skip).
func feed(b *builder, d *htmlDoc, from int, resolve func(string) string) {
	for _, hb := range d.Blocks[from:] {
		src := hb.Src
		ex := &extra{anchors: make([]string, len(hb.Anch))}
		for i, a := range hb.Anch {
			ex.anchors[i] = hb.Src.Part + "#" + a
		}
		if hb.Kind == KindImg {
			if p := resolve(hb.Img); p != "" {
				b.block(KindImg, 0, []seg{{Text: joinSegs(hb.Segs), ByteOff: -1}}, &src, p, ex)
			}
			continue
		}
		for _, rl := range hb.Links {
			ex.links = append(ex.links, linkTarget(rl, hb.Src.Part, resolve))
		}
		b.block(hb.Kind, hb.Level, hb.Segs, &src, "", ex)
	}
}

// linkTarget turns a raw href into a Link whose To is "part#id" (resolved to a block ID by Finish) or
// whose URL is an allowed outside address. Anything else (javascript:, data:, a file the book lacks)
// gives an empty Link, which placeLinks drops.
func linkTarget(rl rawLink, part string, resolve func(string) string) Link {
	href := rl.Href
	if strings.HasPrefix(href, "#") {
		return Link{To: part + href, Note: rl.Note}
	}
	if i := strings.IndexAny(href, ":/?#"); i > 0 && href[i] == ':' {
		switch strings.ToLower(href[:i]) {
		case "http", "https", "mailto":
			return Link{URL: href}
		}
		return Link{}
	}
	if !isLocalRef(href) {
		return Link{}
	}
	path, frag, _ := strings.Cut(href, "#")
	if r := resolve(path); r != "" {
		return Link{To: r + "#" + frag, Note: rl.Note}
	}
	return Link{}
}

func joinSegs(segs []seg) string {
	var sb strings.Builder
	for _, s := range segs {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

func hasToken(v, want string) bool {
	for _, f := range strings.Fields(v) {
		if f == want {
			return true
		}
	}
	return false
}

// localLinks gives a block its own link table: segs point at links[i-1] of the whole part, and the
// block's copy numbers only the links it uses.
func localLinks(segs []seg, all []rawLink) ([]seg, []rawLink) {
	var out []rawLink
	var remap map[int]int
	for i, s := range segs {
		if s.Link == 0 {
			continue
		}
		if remap == nil {
			remap = map[int]int{}
			segs = append([]seg(nil), segs...)
		}
		n, ok := remap[s.Link]
		if !ok {
			out = append(out, all[s.Link-1])
			n = len(out)
			remap[s.Link] = n
		}
		segs[i].Link = n
	}
	return segs, out
}
