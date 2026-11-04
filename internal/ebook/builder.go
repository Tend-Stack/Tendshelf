package ebook

import "strings"

// builder collects chapters and blocks for every parser, so IDs, offsets and front rules exist once.
type builder struct {
	format string
	lim    Limits
	layout string

	chapters []Chapter
	kinds    [][]string
	words    []int
	blocks   int
	total    int
	err      error

	// dims reads the pixel size of an image by its zip path (EPUB only; nil elsewhere).
	dims func(path string) (w, h int)
}

// extra is what an HTML block carries beyond text: its links (Link targets as part#id or URL) and the
// source ids that point at it.
type extra struct {
	links   []Link
	anchors []string
}

func newBuilder(format string, lim Limits) *builder {
	return &builder{format: format, lim: lim, layout: "flow"}
}

// Chapter starts a chapter; kinds are the source's own labels (cover, toc, ...).
func (b *builder) Chapter(title, src string, kinds []string) {
	b.chapters = append(b.chapters, Chapter{Title: normalizeString(title), Src: src})
	b.kinds = append(b.kinds, kinds)
	b.words = append(b.words, 0)
}

// current returns the open chapter, starting an untitled one if needed.
func (b *builder) current() int {
	if len(b.chapters) == 0 {
		b.Chapter("", "", nil)
	}
	return len(b.chapters) - 1
}

// setKinds adds source kinds to the open chapter.
func (b *builder) addKinds(k ...string) {
	i := b.current()
	b.kinds[i] = append(b.kinds[i], k...)
}

func (b *builder) fail(r *Refusal) {
	if b.err == nil {
		b.err = r
	}
}

func (b *builder) add(blk Block, nWords int) {
	if b.err != nil {
		return
	}
	b.blocks++
	if b.blocks > b.lim.MaxBlocks {
		b.fail(damaged("too_many_blocks"))
		return
	}
	b.total += nWords
	if b.total > b.lim.MaxWords {
		b.fail(refuse(ReasonTooLarge, "too_many_words"))
		return
	}
	i := b.current()
	b.words[i] += nWords
	b.chapters[i].Blocks = append(b.chapters[i].Blocks, blk)
}

// Block adds a block to the open chapter. Empty blocks are dropped. src may be nil; when set, its Map
// is filled from the segments' byte offsets.
func (b *builder) Block(kind string, level int, segs []seg, src *SrcRef) {
	b.block(kind, level, segs, src, "", nil)
}

func (b *builder) block(kind string, level int, segs []seg, src *SrcRef, img string, ex *extra) {
	if b.err != nil {
		return
	}
	nr := normalizeSegs(segs, kind == KindPre)
	if len(nr) == 0 && kind != KindHR && kind != KindImg {
		return
	}
	var sb strings.Builder
	for _, n := range nr {
		sb.WriteRune(n.r)
	}
	text := sb.String()
	rs := make([]rune, len(nr))
	for i, n := range nr {
		rs[i] = n.r
	}
	idx := u16Index(rs)
	blk := Block{Kind: kind, Level: level, Text: text, Img: img, Runs: []Run{}}
	if kind != KindH {
		blk.Level = 0
	}
	// Runs: consecutive runes of one non-empty style.
	for i := 0; i < len(nr); {
		if nr[i].style == "" {
			i++
			continue
		}
		j := i
		for j < len(nr) && nr[j].style == nr[i].style {
			j++
		}
		blk.Runs = append(blk.Runs, Run{Start: idx[i], End: idx[j], Style: nr[i].style})
		i = j
	}
	if ex != nil {
		blk.anchors = ex.anchors
		blk.Links = placeLinks(nr, idx, ex.links)
	}
	if kind == KindImg && img != "" && b.dims != nil {
		blk.ImgW, blk.ImgH = b.dims(img)
	}
	blk.Sentences, blk.Words = segmentText(text, kind == KindH || kind == KindImg || kind == KindHR || kind == KindPre)
	if src != nil {
		s := *src
		s.Map = [][2]int{}
		for i, n := range nr {
			if n.first && n.off >= 0 {
				s.Map = append(s.Map, [2]int{idx[i], n.off})
			}
		}
		blk.Src = &s
	}
	b.add(blk, len(blk.Words))
}

// Image adds an img block; alt becomes its text. An image stays even with an empty alt.
func (b *builder) Image(zipPath, alt string, src *SrcRef) {
	b.block(KindImg, 0, []seg{{Text: alt, ByteOff: -1}}, src, zipPath, nil)
}

// placeLinks turns the runes' link numbers into Link spans over the block text. table[i-1] is the
// target of link number i. Spans of only blank text are dropped, and so are links with no target.
func placeLinks(nr []nrune, idx []int, table []Link) []Link {
	var out []Link
	for i := 0; i < len(nr); {
		n := nr[i].link
		if n == 0 || n > len(table) {
			i++
			continue
		}
		j := i
		blank := true
		for j < len(nr) && nr[j].link == n {
			if nr[j].r != ' ' {
				blank = false
			}
			j++
		}
		l := table[n-1]
		if !blank && (l.To != "" || l.URL != "") {
			l.Start, l.End = idx[i], idx[j]
			out = append(out, l)
		}
		i = j
	}
	return out
}

// resolveLinks points every link at a block ID. A link to a part#id the book does not have is dropped
// (its text stays); one that lands on a note block, or was marked as a note, opens as a footnote.
func resolveLinks(chs []Chapter) {
	target := map[string]*Block{}
	partFirst := map[string]*Block{}
	for ci := range chs {
		c := &chs[ci]
		if _, dup := partFirst[c.Src]; !dup && len(c.Blocks) > 0 {
			partFirst[c.Src] = &c.Blocks[0]
		}
		for bi := range c.Blocks {
			bl := &c.Blocks[bi]
			for _, a := range bl.anchors {
				if _, dup := target[a]; !dup {
					target[a] = bl
				}
			}
		}
	}
	for ci := range chs {
		for bi := range chs[ci].Blocks {
			bl := &chs[ci].Blocks[bi]
			if len(bl.Links) == 0 {
				continue
			}
			kept := bl.Links[:0]
			for _, l := range bl.Links {
				if l.To != "" {
					t := target[l.To]
					if t == nil {
						if part, frag, ok := strings.Cut(l.To, "#"); ok && frag == "" {
							t = partFirst[part]
						}
					}
					if t == nil {
						continue
					}
					l.To = t.ID
					l.Note = l.Note || t.Kind == KindNote
				}
				kept = append(kept, l)
			}
			if len(kept) == 0 {
				kept = nil
			}
			bl.Links = kept
		}
	}
}

// Finish drops empty chapters, numbers chapters and blocks, marks front matter and returns the
// document (SourceSHA256 is set by the caller). pageCount 0 means ceil(chars / 1800).
func (b *builder) Finish(meta Meta, pageCount int) (*Doc, error) {
	if b.err != nil {
		return nil, b.err
	}
	var chs []Chapter
	var kinds [][]string
	var words []int
	chars := 0
	for i, c := range b.chapters {
		if len(c.Blocks) == 0 {
			continue
		}
		chs = append(chs, c)
		kinds = append(kinds, b.kinds[i])
		words = append(words, b.words[i])
	}
	if len(chs) == 0 {
		return nil, refuse(ReasonNoText, "empty")
	}
	markFront(chs, kinds, words)
	for i := range chs {
		chs[i].Idx = i + 1
		for j := range chs[i].Blocks {
			bl := &chs[i].Blocks[j]
			bl.ID = "c" + itoa(i+1) + ".p" + itoa(j+1)
			chars += u16Len(bl.Text)
		}
	}
	resolveLinks(chs)
	if pageCount <= 0 {
		pageCount = (chars + 1799) / 1800
	}
	if meta.Authors == nil {
		meta.Authors = []string{}
	}
	meta.Title = normalizeString(meta.Title)
	meta.Subtitle = normalizeString(meta.Subtitle)
	meta.Language = strings.TrimSpace(meta.Language)
	return &Doc{V: DocV, Recipe: Recipe, Format: b.format, Layout: b.layout, Meta: meta, PageCount: pageCount, Chapters: chs}, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d [20]byte
	i := len(d)
	for n > 0 {
		i--
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d[i:])
}
