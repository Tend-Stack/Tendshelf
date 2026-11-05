package ebook

import (
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// resolveRef resolves href (relative to the zip directory baseDir) to a zip name. A reference that
// leaves the zip root, or starts with '/', is skipped (ok is false). Only path (zip names) is used.
func resolveRef(baseDir, href string) (string, bool) {
	if i := strings.IndexAny(href, "#?"); i >= 0 {
		href = href[:i]
	}
	if href == "" || strings.HasPrefix(href, "/") || !isLocalRef(href) {
		return "", false
	}
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	p := path.Join(baseDir, href)
	if p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return "", false
	}
	return p, true
}

type opfItem struct {
	ID, Href, Type, Props string
}

type opfData struct {
	Title    string
	Creators []opfCreator
	Lang     string
	IDs      []string
	Items    []opfItem
	Spine    []string // linear itemrefs
	SpineTOC string
	Guide    map[string][]string // resolved path -> guide types
	roles    map[string]string   // refines id -> role

	Description, Publisher, Date string
	Series, SeriesIndex          string
	CoverID                      string            // <meta name="cover" content="item id">
	collID                       string            // id of the belongs-to-collection meta
	positions                    map[string]string // refines id -> group-position
}

type opfCreator struct{ ID, Role, Name string }

func epubTypeAttr(se xml.StartElement) string {
	for _, a := range se.Attr {
		if a.Name.Local == "type" && (a.Name.Space == "epub" || strings.HasSuffix(a.Name.Space, "/ops")) {
			return a.Value
		}
	}
	return ""
}

func readElementText(d *xmlDec) (string, error) {
	var sb strings.Builder
	depth := 1
	for depth > 0 {
		t, err := d.Next()
		if err != nil {
			return "", err
		}
		switch v := t.(type) {
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			sb.Write(v)
		}
	}
	return sb.String(), nil
}

func parseOPF(data []byte, opfDir string, lim Limits) (*opfData, error) {
	d := newXMLBytes(data, lim)
	o := &opfData{Guide: map[string][]string{}, roles: map[string]string{}, positions: map[string]string{}}
	for {
		t, err := d.Next()
		if err == io.EOF {
			return o, nil
		}
		if err != nil {
			return nil, err
		}
		se, ok := t.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "title":
			if txt, err := readElementText(d); err != nil {
				return nil, err
			} else if o.Title == "" {
				o.Title = txt
			}
		case "creator":
			role := attr(se, "role")
			txt, err := readElementText(d)
			if err != nil {
				return nil, err
			}
			o.Creators = append(o.Creators, opfCreator{ID: attr(se, "id"), Role: role, Name: txt})
		case "language":
			txt, err := readElementText(d)
			if err != nil {
				return nil, err
			}
			if o.Lang == "" {
				o.Lang = strings.TrimSpace(txt)
			}
		case "identifier":
			txt, err := readElementText(d)
			if err != nil {
				return nil, err
			}
			o.IDs = append(o.IDs, strings.TrimSpace(txt))
		case "description":
			txt, err := readElementText(d)
			if err != nil {
				return nil, err
			}
			if o.Description == "" {
				o.Description = txt
			}
		case "publisher":
			txt, err := readElementText(d)
			if err != nil {
				return nil, err
			}
			if o.Publisher == "" {
				o.Publisher = txt
			}
		case "date":
			txt, err := readElementText(d)
			if err != nil {
				return nil, err
			}
			if o.Date == "" {
				o.Date = strings.TrimSpace(txt)
			}
		case "meta":
			r, prop := attr(se, "refines"), attr(se, "property")
			switch name := attr(se, "name"); {
			case name == "cover" && o.CoverID == "":
				o.CoverID = attr(se, "content")
			case name == "calibre:series" && o.Series == "":
				o.Series = attr(se, "content")
			case name == "calibre:series_index" && o.SeriesIndex == "":
				o.SeriesIndex = attr(se, "content")
			case prop == "belongs-to-collection" && r == "" && o.Series == "":
				txt, err := readElementText(d)
				if err != nil {
					return nil, err
				}
				o.Series, o.collID = strings.TrimSpace(txt), attr(se, "id")
			case r != "" && (prop == "role" || prop == "group-position"):
				txt, err := readElementText(d)
				if err != nil {
					return nil, err
				}
				if prop == "role" {
					o.roles[strings.TrimPrefix(r, "#")] = strings.TrimSpace(txt)
				} else {
					o.positions[strings.TrimPrefix(r, "#")] = strings.TrimSpace(txt)
				}
			}
		case "item":
			o.Items = append(o.Items, opfItem{ID: attr(se, "id"), Href: attr(se, "href"), Type: attr(se, "media-type"), Props: attr(se, "properties")})
		case "spine":
			o.SpineTOC = attr(se, "toc")
		case "itemref":
			if attr(se, "linear") != "no" {
				o.Spine = append(o.Spine, attr(se, "idref"))
			}
		case "reference":
			if p, ok := resolveRef(opfDir, attr(se, "href")); ok {
				o.Guide[p] = append(o.Guide[p], attr(se, "type"))
			}
		}
	}
}

// authors returns creators without a role, or with role aut, in order.
func (o *opfData) authors() []string {
	var out []string
	for _, c := range o.Creators {
		role := c.Role
		if role == "" && c.ID != "" {
			role = o.roles[c.ID]
		}
		if role != "" && role != "aut" {
			continue
		}
		if n := normalizeString(c.Name); n != "" {
			out = append(out, n)
		}
	}
	return out
}

var asinRE = regexp.MustCompile(`^B0[0-9A-Z]{8}$`)

func identifiers(ids []string) Identifiers {
	var out Identifiers
	for _, id := range ids {
		if out.ISBN == "" {
			if v := textmatch.ISBN(id); v != "" {
				out.ISBN = v
			}
		}
		if out.ASIN == "" {
			a := strings.ToUpper(id)
			if i := strings.LastIndexByte(a, ':'); i >= 0 {
				a = a[i+1:]
			}
			if asinRE.MatchString(a) {
				out.ASIN = a
			}
		}
	}
	return out
}

type tocEntry struct{ Href, Text string }

type navData struct {
	TOC         []tocEntry
	Landmarks   map[string][]string // resolved path -> epub:type values
	PageTargets []tocEntry          // print pages: Href is as written in the file until rebasePages
}

// parseNav reads an EPUB 3 navigation document.
func parseNav(data []byte, navDir string, lim Limits) (*navData, error) {
	d := newXMLBytes(data, lim)
	n := &navData{Landmarks: map[string][]string{}}
	navType := ""
	for {
		t, err := d.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "nav":
				navType = epubTypeAttr(v)
			case "a":
				if navType == "" {
					continue
				}
				href := attr(v, "href")
				ty := epubTypeAttr(v)
				txt, err := readElementText(d)
				if err != nil {
					return nil, err
				}
				switch {
				case strings.Contains(navType, "page-list"):
					n.PageTargets = append(n.PageTargets, tocEntry{Href: href, Text: txt})
				case strings.Contains(navType, "landmarks"):
					if p, ok := resolveRef(navDir, href); ok && ty != "" {
						n.Landmarks[p] = append(n.Landmarks[p], strings.Fields(ty)...)
					}
				case strings.Contains(navType, "toc"):
					n.TOC = append(n.TOC, tocEntry{Href: href, Text: txt})
				}
			}
		case xml.EndElement:
			if v.Name.Local == "nav" {
				navType = ""
			}
		}
	}
}

// parseNCX reads an EPUB 2 navigation file.
func parseNCX(data []byte, lim Limits) (*navData, error) {
	d := newXMLBytes(data, lim)
	n := &navData{Landmarks: map[string][]string{}}
	var labels []string
	var page *tocEntry // the pageTarget being read
	for {
		t, err := d.Next()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return nil, err
		}
		switch v := t.(type) {
		case xml.StartElement:
			switch v.Name.Local {
			case "navPoint":
				labels = append(labels, "")
			case "text":
				txt, err := readElementText(d)
				if err != nil {
					return nil, err
				}
				if page != nil && page.Text == "" {
					page.Text = txt
				}
				if len(labels) > 0 && labels[len(labels)-1] == "" {
					labels[len(labels)-1] = txt
				}
			case "content":
				if page != nil {
					page.Href = attr(v, "src")
				} else if len(labels) > 0 {
					n.TOC = append(n.TOC, tocEntry{Href: attr(v, "src"), Text: labels[len(labels)-1]})
				}
			case "pageTarget":
				page = &tocEntry{}
			}
		case xml.EndElement:
			if v.Name.Local == "pageTarget" && page != nil {
				n.PageTargets = append(n.PageTargets, *page)
				page = nil
			}
			if v.Name.Local == "navPoint" && len(labels) > 0 {
				labels = labels[:len(labels)-1]
			}
		}
	}
}

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "." {
		return "."
	}
	return d
}

func parseEPUB(zb *zipBox, lim Limits) (*Doc, error) {
	cdata, err := zb.Read("META-INF/container.xml")
	if err != nil {
		if _, ok := err.(*Refusal); ok {
			return nil, err
		}
		return nil, damaged("no_container")
	}
	opfPath := ""
	cd := newXMLBytes(cdata, lim)
	for opfPath == "" {
		t, err := cd.Next()
		if err != nil {
			break
		}
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "rootfile" {
			if strings.EqualFold(attr(se, "media-type"), "application/oebps-package+xml") {
				if p, ok := resolveRef(".", attr(se, "full-path")); ok {
					opfPath = p
				}
			}
		}
	}
	if opfPath == "" {
		return nil, damaged("no_container")
	}
	odata, err := zb.Read(opfPath)
	if err != nil {
		if _, ok := err.(*Refusal); ok {
			return nil, err
		}
		return nil, damaged("no_container")
	}
	opfDir := dirOf(opfPath)
	o, err := parseOPF(odata, opfDir, lim)
	if err != nil {
		return nil, err
	}
	byID := map[string]opfItem{}
	for _, it := range o.Items {
		if _, dup := byID[it.ID]; !dup {
			byID[it.ID] = it
		}
	}
	if len(o.Spine) == 0 {
		return nil, damaged("no_spine")
	}

	// Navigation: the EPUB 3 nav document first, then the NCX for what it lacks.
	var nav *navData
	for _, it := range o.Items {
		if !hasProp(it.Props, "nav") {
			continue
		}
		if p, ok := resolveRef(opfDir, it.Href); ok {
			if nd, err := zb.Read(p); err == nil {
				if nv, err := parseNav(nd, dirOf(p), lim); err == nil {
					nv.TOC = rebase(nv.TOC, dirOf(p))
					nv.PageTargets = rebasePages(nv.PageTargets, dirOf(p))
					nav = nv
				}
			}
		}
		break
	}
	if nav == nil || len(nav.TOC) == 0 || len(nav.PageTargets) == 0 {
		for _, it := range o.Items {
			if !(it.Type == "application/x-dtbncx+xml" || (it.ID == o.SpineTOC && o.SpineTOC != "")) {
				continue
			}
			if p, ok := resolveRef(opfDir, it.Href); ok {
				if nd, err := zb.Read(p); err == nil {
					if nv, err := parseNCX(nd, lim); err == nil {
						nv.TOC = rebase(nv.TOC, dirOf(p))
						nv.PageTargets = rebasePages(nv.PageTargets, dirOf(p))
						if nav == nil {
							nav = nv
						} else {
							if len(nav.TOC) == 0 {
								nav.TOC = nv.TOC
							}
							if len(nav.PageTargets) == 0 {
								nav.PageTargets = nv.PageTargets
							}
						}
					}
				}
			}
			break
		}
	}
	titles := map[string]string{}
	if nav != nil {
		for _, e := range nav.TOC {
			if _, dup := titles[e.Href]; !dup {
				titles[e.Href] = e.Text
			}
		}
	}

	b := newBuilder("epub", lim)
	imgDims := map[string][2]int{}
	b.dims = func(p string) (int, int) {
		if d, ok := imgDims[p]; ok {
			return d[0], d[1]
		}
		var w, h int
		if data, err := zb.Read(p); err == nil {
			w, h = imageSize(data)
		}
		imgDims[p] = [2]int{w, h}
		return w, h
	}
	for _, id := range o.Spine {
		it, ok := byID[id]
		if !ok || !(it.Type == "application/xhtml+xml" || it.Type == "text/html") {
			continue
		}
		p, ok := resolveRef(opfDir, it.Href)
		if !ok {
			continue
		}
		data, err := zb.Read(p)
		if err != nil {
			if r, ok := err.(*Refusal); ok {
				return nil, r
			}
			continue
		}
		hd, err := walkHTML(data, p, lim)
		if err != nil {
			return nil, err
		}
		title := titles[p]
		if title == "" {
			title = firstHeading(hd)
		}
		var kinds []string
		kinds = append(kinds, o.Guide[p]...)
		if nav != nil {
			kinds = append(kinds, nav.Landmarks[p]...)
		}
		kinds = append(kinds, hd.Types...)
		b.Chapter(title, p, kinds)
		dir := dirOf(p)
		feed(b, hd, 0, func(src string) string {
			r, ok := resolveRef(dir, src)
			if !ok {
				return ""
			}
			return r
		})
		if b.err != nil {
			return nil, b.err
		}
	}
	if nav != nil {
		b.pageTargets = nav.PageTargets
	}
	meta := Meta{Title: o.Title, Authors: o.authors(), Language: o.Lang, Identifiers: identifiers(o.IDs)}
	meta.Description = normalizeString(stripTags(o.Description))
	meta.Publisher = normalizeString(o.Publisher)
	meta.Series = normalizeString(o.Series)
	meta.SeriesIndex = strings.TrimSpace(firstNonEmpty(o.SeriesIndex, o.positions[o.collID]))
	meta.Year = yearOf(o.Date)
	meta.CoverPart = o.coverPart(opfDir)
	return b.Finish(meta, 0)
}

func hasProp(props, want string) bool {
	for _, f := range strings.Fields(props) {
		if f == want {
			return true
		}
	}
	return false
}

// rebase turns hrefs relative to dir into zip names; references that leave the zip are dropped.
func rebase(es []tocEntry, dir string) []tocEntry {
	out := make([]tocEntry, 0, len(es))
	for _, e := range es {
		if p, ok := resolveRef(dir, e.Href); ok {
			out = append(out, tocEntry{Href: p, Text: e.Text})
		}
	}
	return out
}

// rebasePages is rebase for page targets: the fragment stays, as "part#id", because a page starts at an
// id and not at the top of a part. A target with no fragment becomes "part#" (the part's first block).
func rebasePages(es []tocEntry, dir string) []tocEntry {
	out := make([]tocEntry, 0, len(es))
	for _, e := range es {
		p, ok := resolveRef(dir, e.Href)
		if !ok {
			continue
		}
		frag := ""
		if i := strings.IndexByte(e.Href, '#'); i >= 0 {
			frag = e.Href[i+1:]
			if u, err := url.PathUnescape(frag); err == nil {
				frag = u
			}
		}
		out = append(out, tocEntry{Href: p + "#" + frag, Text: e.Text})
	}
	return out
}

func firstHeading(d *htmlDoc) string {
	for _, b := range d.Blocks {
		if b.Kind == KindH && b.Level >= 1 && b.Level <= 3 {
			return joinSegs(b.Segs)
		}
	}
	return ""
}

// coverPart returns the zip path of the cover picture: the item marked cover-image, else the item
// the legacy cover meta names. Empty when neither exists.
func (o *opfData) coverPart(opfDir string) string {
	for _, it := range o.Items {
		if hasProp(it.Props, "cover-image") {
			if p, ok := resolveRef(opfDir, it.Href); ok {
				return p
			}
		}
	}
	if o.CoverID != "" {
		for _, it := range o.Items {
			if it.ID == o.CoverID && strings.HasPrefix(it.Type, "image/") {
				if p, ok := resolveRef(opfDir, it.Href); ok {
					return p
				}
			}
		}
	}
	return ""
}

// yearOf reads the year from the start of a Dublin Core date ("2019", "2019-05-01"); 0 when absent
// or not plausible.
func yearOf(s string) int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0
	}
	n := 0
	for _, c := range s[:4] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	if n < 1000 || n > 2999 {
		return 0
	}
	return n
}

// stripTags removes markup from a description that the publisher wrote as HTML.
func stripTags(s string) string {
	if !strings.ContainsAny(s, "<&") {
		return s
	}
	var sb strings.Builder
	z := html.NewTokenizer(strings.NewReader(s))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return sb.String()
		case html.TextToken:
			sb.Write(z.Text())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			sb.WriteByte(' ')
		}
	}
}
