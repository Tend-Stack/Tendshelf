package ebook

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// PDF text comes from glyph runs (github.com/ledongthuc/pdf): the module gives each glyph's position
// and size, which the line, header/footer, heading and reflow rules below need. Encrypted PDFs are
// refused as copy-protected before any text is read.

var (
	pageNumberRE = regexp.MustCompile(`(?i)^(\d{1,5}|[ivxlcdm]{1,8})$`)
	digitsRE     = regexp.MustCompile(`\d+`)
	endsHyphenRE = regexp.MustCompile(`\pL-$`)
)

// pdfLine is one visual line (or one column segment of it) on a page.
type pdfLine struct {
	page  int
	text  string
	x, y  float64
	w     float64
	size  float64
	pageW float64
	pageH float64
}

// hasEncryptMarker looks for a /Encrypt key (not a longer name) anywhere in the file, in chunks so a
// large file is never held whole.
func hasEncryptMarker(ra io.ReaderAt, size int64) bool {
	const chunk = 1 << 20
	marker := []byte("/Encrypt")
	buf := make([]byte, chunk+len(marker)+1)
	for off := int64(0); off < size; off += chunk {
		n, _ := ra.ReadAt(buf, off)
		b := buf[:n]
		for i := 0; ; {
			j := bytes.Index(b[i:], marker)
			if j < 0 {
				break
			}
			end := i + j + len(marker)
			if end >= len(b) || !isPDFNameChar(b[end]) {
				return true
			}
			i = end
		}
		if int64(n) < int64(len(buf)) {
			break
		}
	}
	return false
}

func isPDFNameChar(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.'
}

func parsePDF(ra io.ReaderAt, size int64, lim Limits) (doc *Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, damaged("parser_panic")
		}
	}()
	if hasEncryptMarker(ra, size) {
		return nil, refuse(ReasonDRM, "pdf_encrypt")
	}
	r, err := pdf.NewReader(ra, size)
	if err != nil {
		if errors.Is(err, pdf.ErrInvalidPassword) {
			return nil, refuse(ReasonDRM, "pdf_encrypt")
		}
		return nil, damaged("pdf_open")
	}
	if tr := r.Trailer(); tr.Kind() == pdf.Dict && tr.Key("Encrypt").Kind() != pdf.Null {
		return nil, refuse(ReasonDRM, "pdf_encrypt")
	}
	n := r.NumPage()
	if n <= 0 {
		return nil, damaged("pdf_no_pages")
	}
	if !pageTreeSane(r) {
		return nil, damaged("pdf_page_tree")
	}
	start := time.Now()
	var lines []pdfLine
	letters := 0
	for p := 1; p <= n; p++ {
		if time.Since(start) > lim.ParseTimeout {
			return nil, damaged("timeout")
		}
		pl := pdfPageLines(r, p)
		for _, l := range pl {
			for _, c := range l.text {
				if unicode.IsLetter(c) {
					letters++
				}
			}
		}
		lines = append(lines, pl...)
	}
	if letters < 200 || letters < 20*n {
		return nil, refuse(ReasonNoText, "image_only")
	}

	lines = dropHeadersFooters(lines, n)
	body := bodySize(lines)
	page := isPageLayout(lines)

	b := newBuilder("pdf", lim)
	if page {
		b.layout = "page"
		sort.SliceStable(lines, func(i, j int) bool {
			if lines[i].page != lines[j].page {
				return lines[i].page < lines[j].page
			}
			ci, cj := lines[i].x > 0.45*lines[i].pageW, lines[j].x > 0.45*lines[j].pageW
			if ci != cj {
				return !ci
			}
			return lines[i].y > lines[j].y
		})
	}
	assemblePDF(b, lines, body, page)
	if b.err != nil {
		return nil, b.err
	}
	meta := Meta{}
	if info := r.Trailer().Key("Info"); info.Kind() == pdf.Dict {
		meta.Title = info.Key("Title").Text()
		if a := normalizeString(info.Key("Author").Text()); a != "" {
			meta.Authors = []string{a}
		}
	}
	var out *Doc
	out, err = b.Finish(meta, n)
	if err != nil {
		return nil, err
	}
	out.PageList = pdfPageList(out)
	return out, nil
}

// pdfPageList marks the first block of each PDF page, labelled with its page number. A page that has no
// block of its own (a picture-only page) has no mark.
func pdfPageList(d *Doc) []PageMark {
	var out []PageMark
	seen := map[string]bool{}
	for _, c := range d.Chapters {
		for _, bl := range c.Blocks {
			if bl.Src == nil || !strings.HasPrefix(bl.Src.Part, "page:") || seen[bl.Src.Part] {
				continue
			}
			seen[bl.Src.Part] = true
			if len(out) < maxPageMarks {
				out = append(out, PageMark{Label: strings.TrimPrefix(bl.Src.Part, "page:"), Block: bl.ID})
			}
		}
	}
	return out
}

// pdfPageLines turns one page's glyphs into lines, left to right inside each line.
// Limits on the page-tree walk: a real tree is a few levels deep, and a node budget bounds a wide one.
const (
	pdfTreeMaxDepth = 64
	pdfTreeMaxNodes = 500000
)

// pageTreeSane walks /Kids from the root before r.Page does. r.Page follows a node that lists itself
// forever, so a tree that is too deep (a loop always is) or too large reads as damaged.
func pageTreeSane(r *pdf.Reader) bool {
	budget := pdfTreeMaxNodes
	var walk func(v pdf.Value, depth int) bool
	walk = func(v pdf.Value, depth int) bool {
		budget--
		if depth > pdfTreeMaxDepth || budget < 0 {
			return false
		}
		if v.Key("Type").Name() != "Pages" {
			return true
		}
		kids := v.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			if !walk(kids.Index(i), depth+1) {
				return false
			}
		}
		return true
	}
	return walk(r.Trailer().Key("Root").Key("Pages"), 0)
}

func pdfPageLines(r *pdf.Reader, num int) []pdfLine {
	pg := r.Page(num)
	if pg.V.IsNull() {
		return nil
	}
	w, h := 612.0, 792.0
	var mb pdf.Value
	for v, hops := pg.V, 0; !v.IsNull() && hops < 32; v, hops = v.Key("Parent"), hops+1 {
		if m := v.Key("MediaBox"); !m.IsNull() {
			mb = m
			break
		}
	}
	if mb.Kind() == pdf.Array && mb.Len() == 4 {
		if x := mb.Index(2).Float64() - mb.Index(0).Float64(); x > 0 {
			w = x
		}
		if y := mb.Index(3).Float64() - mb.Index(1).Float64(); y > 0 {
			h = y
		}
	}
	items := append([]pdf.Text(nil), pg.Content().Text...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Y > items[j].Y })
	var out []pdfLine
	for i := 0; i < len(items); {
		size := items[i].FontSize
		if size <= 0 {
			size = 10
		}
		j := i
		for j < len(items) && math.Abs(items[j].Y-items[i].Y) <= 0.3*size {
			j++
		}
		row := items[i:j]
		sort.SliceStable(row, func(a, b int) bool { return row[a].X < row[b].X })
		out = append(out, splitSegments(row, num, w, h)...)
		i = j
	}
	return out
}

// splitSegments joins glyphs of one row into lines, starting a new line at a gap wider than two ems
// (a column gutter). A space is added where the gap looks like a word space and none is present.
func splitSegments(row []pdf.Text, page int, pw, ph float64) []pdfLine {
	var out []pdfLine
	var sb strings.Builder
	var cur *pdfLine
	var prev pdf.Text
	var counts = map[float64]int{}
	flush := func() {
		if cur == nil {
			return
		}
		cur.text = strings.TrimSpace(sb.String())
		best, bc := 0.0, -1
		var keys []float64
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Float64s(keys)
		for _, k := range keys {
			if counts[k] > bc {
				best, bc = k, counts[k]
			}
		}
		cur.size = best
		last := prev.X + prev.W
		est := float64(utf8.RuneCountInString(cur.text)) * 0.5 * best
		cur.w = last - cur.x
		if cur.w < est/2 {
			cur.w = est
		}
		if cur.text != "" {
			out = append(out, *cur)
		}
		sb.Reset()
		counts = map[float64]int{}
		cur = nil
	}
	for _, it := range row {
		size := it.FontSize
		if size <= 0 {
			size = 10
		}
		if cur != nil {
			gap := it.X - (prev.X + prev.W)
			switch {
			case gap > 2*size:
				flush()
			case prev.W > 0 && gap > 0.2*size && !strings.HasSuffix(sb.String(), " ") && !strings.HasPrefix(it.S, " "):
				sb.WriteString(" ")
			}
		}
		if cur == nil {
			cur = &pdfLine{page: page, x: it.X, y: it.Y, pageW: pw, pageH: ph}
		}
		sb.WriteString(it.S)
		counts[math.Round(size*2)/2] += utf8.RuneCountInString(it.S)
		prev = it
	}
	flush()
	return out
}

func inZone(l pdfLine) bool {
	return l.y > 0.92*l.pageH || l.y < 0.08*l.pageH
}

// dropHeadersFooters removes lines that recur in the top or bottom 8 % of at least half the pages
// (three pages or more; digits count as one symbol), and lone page numbers in those zones.
func dropHeadersFooters(lines []pdfLine, pages int) []pdfLine {
	seen := map[string]map[int]bool{}
	key := func(l pdfLine) string { return digitsRE.ReplaceAllString(strings.ToLower(l.text), "#") }
	for _, l := range lines {
		if !inZone(l) {
			continue
		}
		k := key(l)
		if seen[k] == nil {
			seen[k] = map[int]bool{}
		}
		seen[k][l.page] = true
	}
	var out []pdfLine
	for _, l := range lines {
		if inZone(l) {
			if pageNumberRE.MatchString(strings.TrimSpace(l.text)) {
				continue
			}
			if pages >= 3 && len(seen[key(l)])*2 >= pages {
				continue
			}
		}
		out = append(out, l)
	}
	return out
}

// bodySize is the font size carrying the most characters.
func bodySize(lines []pdfLine) float64 {
	counts := map[float64]int{}
	for _, l := range lines {
		counts[l.size] += utf8.RuneCountInString(l.text)
	}
	var keys []float64
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Float64s(keys)
	best, bc := 12.0, -1
	for _, k := range keys {
		if counts[k] > bc {
			best, bc = k, counts[k]
		}
	}
	return best
}

// isPageLayout reports a layout that will not reflow: columns, or mostly short lines.
func isPageLayout(lines []pdfLine) bool {
	if len(lines) == 0 {
		return false
	}
	far := 0
	widths := make([]float64, len(lines))
	for i, l := range lines {
		if l.x > 0.45*l.pageW {
			far++
		}
		widths[i] = l.w
	}
	if float64(far) > 0.3*float64(len(lines)) {
		return true
	}
	sort.Float64s(widths)
	// Reference is the 90th percentile: the width of a full line. (A median cannot work: fewer than
	// half the lines can be shorter than a fraction of it.)
	ref := widths[(len(widths)*9)/10]
	short := 0
	for _, w := range widths {
		if w < 0.4*ref {
			short++
		}
	}
	return float64(short) > 0.5*float64(len(lines))
}

// assemblePDF joins lines into paragraphs, headings and chapters.
func assemblePDF(b *builder, lines []pdfLine, body float64, pageLayout bool) {
	var para []string
	var paraPage int
	var prev *pdfLine
	var head []string
	var headPage int
	var headChapter bool

	flushPara := func() {
		if len(para) == 0 {
			return
		}
		b.Block(KindP, 0, []seg{{Text: strings.Join(para, " "), ByteOff: -1}}, &SrcRef{Part: fmt.Sprintf("page:%d", paraPage)})
		para = nil
	}
	flushHead := func() {
		if len(head) == 0 {
			return
		}
		text := strings.Join(head, " ")
		src := fmt.Sprintf("page:%d", headPage)
		level := 2
		if headChapter {
			level = 1
			b.Chapter(text, src, nil)
		}
		b.Block(KindH, level, []seg{{Text: text, ByteOff: -1}}, &SrcRef{Part: src})
		head, headChapter = nil, false
	}
	for i := range lines {
		l := &lines[i]
		isHead := l.size >= 1.3*body
		chapterLike := l.size >= 1.5*body || (utf8.RuneCountInString(l.text) <= 60 && chapterLineRE.MatchString(l.text))
		if chapterLike {
			isHead = true
		}
		if isHead {
			flushPara()
			if len(head) > 0 && (prev == nil || prev.page != l.page || prev.y-l.y > 2.2*l.size) {
				flushHead()
			}
			if len(head) == 0 {
				headPage = l.page
			}
			head = append(head, l.text)
			headChapter = headChapter || chapterLike
			prev = l
			continue
		}
		flushHead()
		newPara := len(para) == 0 || pageLayout
		if !newPara && prev != nil {
			if prev.page != l.page {
				last, _ := utf8.DecodeLastRuneInString(para[len(para)-1])
				newPara = strings.ContainsRune(".!?:\"”’»)", last)
			} else {
				newPara = prev.y-l.y > 1.8*l.size || l.x > prev.x+l.size
			}
		}
		if newPara {
			flushPara()
			paraPage = l.page
			para = []string{l.text}
		} else if endsHyphenRE.MatchString(para[len(para)-1]) && startsLower(l.text) {
			para[len(para)-1] = strings.TrimSuffix(para[len(para)-1], "-") + l.text
		} else {
			para = append(para, l.text)
		}
		prev = l
	}
	flushHead()
	flushPara()
}

func startsLower(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsLower(r)
}
