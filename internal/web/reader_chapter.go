package web

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strconv"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// rdrChap is one chapter part ready to draw.
type rdrChap struct {
	view   pages.ReaderChapterView
	blocks map[string]pages.ReaderBlockView
	// units is the text of the book before the part, and total the whole book (UTF-16 units).
	total int
}

// rsFragURL is the fragment address of a chapter part. Part 1 leaves the query out.
func (rc readerCtx) rsFragURL(idx, part int) string {
	q := url.Values{}
	if part > 1 {
		q.Set("part", strconv.Itoa(part))
	}
	q.Set("edition", rc.ed.ID)
	return "/hx/books/" + rc.book.ID + "/read/chapters/" + strconv.Itoa(idx) + "?" + q.Encode()
}

// rsPageURL is the plain page address of a chapter part.
func (rc readerCtx) rsPageURL(idx, part int) string {
	p := ""
	if part > 1 {
		p = strconv.Itoa(part)
	}
	return rc.href("ch", strconv.Itoa(idx), "part", p)
}

// rsLinkTo builds the address of an in-book link: the page that holds its target block, and for a
// footnote the address of the popup fragment (RD3).
func (s *server) rsLinkTo(rc readerCtx) func(ebook.Link) (string, string, bool) {
	memo := map[int][]rsPart{}
	return func(l ebook.Link) (string, string, bool) {
		c, bi, ok := rsBlockPos(rc.doc, l.To)
		if !ok {
			return "", "", false
		}
		ps, seen := memo[c]
		if !seen {
			ps = rsParts(rc.doc.Chapters[c-1])
			memo[c] = ps
		}
		blk := rc.doc.Chapters[c-1].Blocks[bi]
		part := strconv.Itoa(rsPartIndex(ps, bi))
		if part == "1" {
			part = ""
		}
		var href string
		if len(blk.Sentences) > 0 {
			sid := ebook.SentenceID(blk.ID, 0)
			href = rc.href("ch", strconv.Itoa(c), "part", part, "at", sid) + "#s-" + sid
		} else {
			href = rc.href("ch", strconv.Itoa(c), "part", part) + "#b-" + blk.ID
		}
		note := "/hx/books/" + rc.book.ID + "/read/note/" + blk.ID + "?edition=" + url.QueryEscape(rc.ed.ID)
		return href, note, true
	}
}

// rsImage is the markup of an EPUB picture: our address, never the publisher's. The picture is read out of the
// file by the image route.
func rsImage(rc readerCtx, b ebook.Block) string {
	src := "/books/" + rc.book.ID + "/read/images/" + b.ID + "?edition=" + url.QueryEscape(rc.ed.ID)
	out := `<img class="ts-reader__image" src="` + html.EscapeString(src) + `" alt="` + html.EscapeString(b.Text) + `" loading="lazy"`
	if b.ImgW > 0 && b.ImgH > 0 {
		out += ` width="` + strconv.Itoa(b.ImgW) + `" height="` + strconv.Itoa(b.ImgH) + `"`
	}
	return out + `>`
}

// readerChapter builds a part (1-based) of chapter idx (1-based) for the person: its blocks with their sentences,
// links, highlights and flags. Part 0 means the whole chapter in one piece (the blocks a highlight changed).
func (s *server) readerChapter(ctx context.Context, u users.User, rc readerCtx, idx, part int) (rdrChap, error) {
	ch := rc.doc.Chapters[idx-1]
	out := rdrChap{blocks: map[string]pages.ReaderBlockView{}}
	hls, err := s.progress.ChapterHighlights(ctx, u.ID, rc.ed.ID, idx)
	if err != nil {
		return out, err
	}
	list, err := s.progress.Bookmarks(ctx, u.ID, rc.book.ID, false)
	if err != nil {
		return out, err
	}
	bms := map[string]bool{}
	for _, b := range list {
		if c, _, _, ok := ebook.ParseSID(b.SID); ok && c == idx && b.EditionID == rc.ed.ID {
			bms[b.SID] = true
		}
	}
	// Text before this chapter, for the page numbers.
	before, total := rsCharsBefore(rc.doc, idx)
	out.total = total
	parts := rsParts(ch)
	sel := rsPart{From: 0, To: len(ch.Blocks)}
	if part > 0 {
		sel = parts[part-1]
	}
	v := pages.ReaderChapterView{Idx: idx, Title: ch.Title, Part: max(part, 1), Parts: len(parts), Head: part <= 1,
		Before: before + sel.Before}
	linkTo := s.rsLinkTo(rc)
	cum, dropDone := before+sel.Before, false
	c := sel.Before
	for bi := sel.From; bi < sel.To; bi++ {
		b := ch.Blocks[bi]
		pg := 0
		if pc := rc.doc.PageCount; pc > 0 && total > 0 {
			pg = min(pc, cum*pc/total+1)
		}
		n := rsUnits(b.Text)
		bv := pages.ReaderBlockView{Kind: b.Kind, ID: "b-" + b.ID, Pg: pg, C: c}
		cum += n
		c += n
		switch {
		case b.Kind == ebook.KindHR:
		case b.Kind == ebook.KindImg && rc.doc.Format == "epub" && b.Img != "":
			bv.HTML = rsImage(rc, b)
		case b.Kind == ebook.KindImg && b.Text == "":
			continue
		default:
			opts := rdrBlockOpts{Hls: hls, Bookmarks: bms, LinkTo: linkTo}
			if b.Kind == ebook.KindP && !dropDone && part <= 1 {
				opts.DropCap, dropDone = true, true
			}
			bv.HTML = rdrInner(b, opts)
		}
		if v.Pg == 0 {
			v.Pg = pg
		}
		v.Blocks = append(v.Blocks, bv)
		out.blocks[b.ID] = bv
	}
	if ch.Front {
		v.Overline = readerFrontOnly
	} else {
		n := 0
		for _, c := range rc.doc.Chapters[:idx] {
			if !c.Front {
				n++
			}
		}
		v.Overline = fmt.Sprintf(readerChapter, n)
	}
	if rc.doc.PageCount > 0 && v.Pg > 0 {
		v.Page = fmt.Sprintf(readerPageOf, v.Pg, rc.doc.PageCount)
	}
	if part > 0 {
		ni, np, ok := rsNext(rc.doc, idx, part, len(parts))
		if ok {
			v.NextFrag, v.NextHref, v.NextLabel = rc.rsFragURL(ni, np), rc.rsPageURL(ni, np), readerNextChapter
		}
		pi, pp, ok := rsPrev(rc.doc, idx, part)
		if ok {
			v.PrevFrag, v.PrevHref, v.PrevLabel = rc.rsFragURL(pi, pp), rc.rsPageURL(pi, pp), readerPrevChapter
		}
	}
	out.view = v
	return out, nil
}

// rsNext is the part after (idx, part): the next part of the chapter, else the first of the next chapter.
func rsNext(d *ebook.Doc, idx, part, parts int) (int, int, bool) {
	switch {
	case part < parts:
		return idx, part + 1, true
	case idx < len(d.Chapters):
		return idx + 1, 1, true
	}
	return 0, 0, false
}

// rsPrev is the part before (idx, part): the previous part of the chapter, else the last of the previous chapter.
func rsPrev(d *ebook.Doc, idx, part int) (int, int, bool) {
	switch {
	case part > 1:
		return idx, part - 1, true
	case idx > 1:
		return idx - 1, len(rsParts(d.Chapters[idx-2])), true
	}
	return 0, 0, false
}

func (s *server) getReader(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	ctx, u, q := r.Context(), currentUser(r), r.URL.Query()
	sheet := q.Get("sheet")
	if sheet != "" && sheet != "contents" && sheet != "marks" && sheet != "reading" {
		s.notFound(w, r)
		return
	}
	pl, ok, err := s.rsStartPlace(ctx, u, rc, q)
	if err != nil {
		s.serverError(w, r, "reader start place", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	chap, err := s.readerChapter(ctx, u, rc, pl.Ch, pl.Part)
	if err != nil {
		s.serverError(w, r, "reader chapter", err)
		return
	}
	v, err := s.readerView(r, rc, chap, pl)
	if err != nil {
		s.serverError(w, r, "reader view", err)
		return
	}
	var sh templ.Component
	switch sheet {
	case "":
	case "reading":
		sv, err := s.readingSheet(r, rc, pl.Ch)
		if err != nil {
			s.serverError(w, r, "reading settings sheet", err)
			return
		}
		sh = pages.ReadingSheet(sv)
	default:
		sv, err := s.readerSheet(r, rc, sheet, pl.Ch)
		if err != nil {
			s.serverError(w, r, "reader sheet", err)
			return
		}
		sh = pages.ReaderSheet(sv)
	}
	s.pageSheetStyled(w, r, v.Title, pages.Reader(v), sh)
}

func (s *server) getReaderChapter(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if err != nil || idx < 1 || idx > len(rc.doc.Chapters) {
		s.notFound(w, r)
		return
	}
	part := 1
	if p := r.URL.Query().Get("part"); p != "" {
		if part, err = strconv.Atoi(p); err != nil || part < 1 || part > len(rsParts(rc.doc.Chapters[idx-1])) {
			s.notFound(w, r)
			return
		}
	}
	chap, err := s.readerChapter(r.Context(), currentUser(r), rc, idx, part)
	if err != nil {
		s.serverError(w, r, "reader chapter fragment", err)
		return
	}
	// A fragment is appended or swapped in place; the page-only previous link has no use there.
	chap.view.PrevHref, chap.view.PrevLabel = "", ""
	s.fragment(w, r, http.StatusOK, pages.ReaderChapter(chap.view))
}

// rsRuler is the footer's progress line: the percent of the book before the part's first block and a tick at
// each chapter start.
func rsRuler(d *ebook.Doc, chap rdrChap) components.RulerView {
	v := components.RulerView{Label: readerRulerLabel, Now: "0"}
	if chap.total <= 0 {
		return v
	}
	v.Now = strconv.Itoa(min(100, chap.view.Before*100/chap.total))
	before := 0
	for i, c := range d.Chapters {
		if i > 0 {
			v.Ticks = append(v.Ticks, strconv.Itoa(min(100, before*100/chap.total)))
		}
		for _, b := range c.Blocks {
			before += rsUnits(b.Text)
		}
	}
	return v
}
