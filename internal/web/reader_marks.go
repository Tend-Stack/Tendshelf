package web

import (
	"cmp"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/textmatch"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Footnotes, search, export and the plain page for a book that cannot be opened (M5 RD3). Every route here goes
// through readerCtx, so it answers 404 unless share.Can(user, ReadText, book); nothing reads another person's rows.

func (s *server) registerReaderMarks(mux *http.ServeMux) {
	mux.HandleFunc("GET /hx/books/{id}/read/note/{blockID}", s.getReaderNote)
	mux.HandleFunc("GET /hx/books/{id}/read/search", s.getReaderSearch)
	mux.HandleFunc("GET /books/{id}/read/marks.md", s.getReaderMarksExport)
	mux.HandleFunc("GET /books/{id}/read/marks.txt", s.getReaderMarksExport)
	mux.HandleFunc("GET /books/{id}/read/marks.json", s.getReaderMarksExport)
}

// readerSwatches are the four highlight colours of the selection toolbar, in the order the server accepts them.
func readerSwatches() []pages.ReaderSwatch {
	return []pages.ReaderSwatch{
		{Colour: "yellow", Label: readerHlYellow}, {Colour: "green", Label: readerHlGreen},
		{Colour: "blue", Label: readerHlBlue}, {Colour: "pink", Label: readerHlPink},
	}
}

// ---- Plain errors ----

// readerTrouble answers a book whose document is missing, damaged or too large: plain words and the way back, the
// detail only in the log. A fragment request gets a toast, never a page.
func (s *server) readerTrouble(w http.ResponseWriter, r *http.Request, bookID string, cause error) {
	slog.Warn("reader cannot open the document", "book", bookID, "err", cause)
	if r.Header.Get("HX-Request") == "true" {
		s.readerToast(w, r, http.StatusInternalServerError, readerCannotOpen)
		return
	}
	s.pageStyled(w, r, http.StatusInternalServerError, readerCannotOpen, "",
		pages.ReaderTrouble(pages.ReaderProblem{Title: readerCannotOpen, Message: readerCannotOpen, BackLabel: readerBackToBook, BackHref: "/books/" + bookID}),
		nil, pages.ReaderStyles...)
}

// ---- Footnotes ----

// getReaderNote answers a footnote link: the target block and the note blocks that follow it, up to the next block
// another note link points at, at most ten, as a dialog the client opens.
func (s *server) getReaderNote(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	id := r.PathValue("blockID")
	c, bi, ok := rsBlockPos(rc.doc, id)
	if !ok {
		s.notFound(w, r)
		return
	}
	blocks := rc.doc.Chapters[c-1].Blocks
	targets := map[string]bool{}
	for _, b := range blocks {
		for _, l := range b.Links {
			if l.Note {
				targets[l.To] = true
			}
		}
	}
	var paras []string
	for i := bi; i < len(blocks) && len(paras) < readerNoteChain; i++ {
		b := blocks[i]
		if i > bi && (b.Kind != ebook.KindNote || targets[b.ID]) {
			break
		}
		b.Sentences, b.Links = nil, nil // no ids twice on the page, no links in the popup
		paras = append(paras, rdrInner(b, rdrBlockOpts{}))
		if b.Kind != ebook.KindNote {
			break
		}
	}
	goHref, _, _ := s.rsLinkTo(rc)(ebook.Link{To: id})
	s.fragment(w, r, http.StatusOK, pages.ReaderNote(pages.ReaderNoteView{
		Label: readerFootnote, GoLabel: readerGoToNote, GoHref: goHref, CloseLabel: readerCloseSheet, Paras: paras,
	}))
}

// readerNoteChain is the most blocks one footnote popup shows.
const readerNoteChain = 10

// ---- Search ----

const (
	readerSearchHits   = 200
	readerSearchMinLen = 2
	readerSearchMaxLen = 200
	readerSnippetUnits = 80
)

// rdrFold lowercases text, takes the accents off and joins runs of white space, the way a person types a search. For
// each folded rune it also gives the UTF-16 offsets of the source rune in the original text.
func rdrFold(text string) (out []rune, from, to []int) {
	u := 0
	for _, r := range text {
		n := 1
		if r >= 0x10000 {
			n = 2
		}
		low := unicode.ToLower(r)
		if unicode.IsSpace(low) {
			if len(out) > 0 && out[len(out)-1] != ' ' {
				out, from, to = append(out, ' '), append(from, u), append(to, u+n)
			}
		} else {
			for _, f := range textmatch.Fold(low) {
				out, from, to = append(out, f), append(from, u), append(to, u+n)
			}
		}
		u += n
	}
	return out, from, to
}

// rdrHit is one match: where it is in the book and the words around it.
type rdrHit struct {
	Chapter, Block int // 1-based chapter, 0-based block index
	Start, End     int // UTF-16 offsets in the block text
}

// rdrSearch finds q in the document: every non-overlapping match of the whole phrase, in reading order, at most
// limit of them. capped is true when there were more.
func rdrSearch(d *ebook.Doc, q string, limit int) (hits []rdrHit, capped bool) {
	needle, _, _ := rdrFold(q)
	for len(needle) > 0 && needle[len(needle)-1] == ' ' {
		needle = needle[:len(needle)-1]
	}
	for len(needle) > 0 && needle[0] == ' ' {
		needle = needle[1:]
	}
	if len(needle) == 0 {
		return nil, false
	}
	for ci, ch := range d.Chapters {
		for bi, b := range ch.Blocks {
			if b.Text == "" || b.Kind == ebook.KindHR {
				continue
			}
			hay, from, to := rdrFold(b.Text)
			for i := 0; i+len(needle) <= len(hay); {
				if !slices.Equal(hay[i:i+len(needle)], needle) {
					i++
					continue
				}
				if len(hits) == limit {
					return hits, true
				}
				hits = append(hits, rdrHit{Chapter: ci + 1, Block: bi, Start: from[i], End: to[i+len(needle)-1]})
				i += len(needle)
			}
		}
	}
	return hits, false
}

// rdrSnippet is about 80 UTF-16 units of the block around the match: before, the match, after.
func rdrSnippet(text string, start, end int) (before, hit, after string) {
	units := utf16.Encode([]rune(text))
	end = min(end, len(units))
	start = min(start, end)
	if end-start > readerSnippetUnits {
		end = start + readerSnippetUnits
	}
	room := (readerSnippetUnits - (end - start)) / 2
	a, b := max(0, start-room), min(len(units), end+room)
	if a < start && units[a] >= 0xDC00 && units[a] <= 0xDFFF { // do not start inside a pair
		a++
	}
	if b < len(units) && b > end && units[b] >= 0xDC00 && units[b] <= 0xDFFF {
		b--
	}
	before, hit, after = string(utf16.Decode(units[a:start])), string(utf16.Decode(units[start:end])), string(utf16.Decode(units[end:b]))
	if a > 0 {
		before = "…" + before
	}
	if b < len(units) {
		after += "…"
	}
	return before, hit, after
}

// readerHits turns the question into the answers shown in the sheet or the htmx fragment.
func (s *server) readerHits(rc readerCtx, q string) pages.ReaderHits {
	q = strings.TrimSpace(q)
	if q == "" {
		return pages.ReaderHits{}
	}
	if n := len([]rune(q)); n < readerSearchMinLen {
		return pages.ReaderHits{Message: readerSearchShort}
	} else if n > readerSearchMaxLen {
		q = string([]rune(q)[:readerSearchMaxLen])
	}
	found, capped := rdrSearch(rc.doc, q, readerSearchHits)
	if len(found) == 0 {
		return pages.ReaderHits{Message: fmt.Sprintf(readerSearchNone, q)}
	}
	linkTo := s.rsLinkTo(rc)
	var out pages.ReaderHits
	for _, h := range found {
		ch := rc.doc.Chapters[h.Chapter-1]
		b := ch.Blocks[h.Block]
		before, hit, after := rdrSnippet(b.Text, h.Start, h.End)
		title := ch.Title
		if title == "" {
			title = fmt.Sprintf(readerChapter, h.Chapter)
		}
		href, _, _ := linkTo(ebook.Link{To: b.ID})
		for k, sent := range b.Sentences {
			if h.Start >= sent[0] && h.Start < sent[1] {
				sid := ebook.SentenceID(b.ID, k)
				part := strconv.Itoa(rsPartAt(rc.doc, sid))
				if part == "1" {
					part = ""
				}
				href = rc.href("ch", strconv.Itoa(h.Chapter), "part", part, "at", sid, "off", strconv.Itoa(h.Start), "hit", strconv.Itoa(h.End-h.Start)) + "#s-" + sid
				break
			}
		}
		out.Items = append(out.Items, pages.ReaderHit{Chapter: title, Before: before, Hit: hit, After: after, Href: href})
	}
	if capped {
		out.Note = readerSearchMax
	}
	return out
}

// getReaderSearch is the htmx answer: the hit list alone.
func (s *server) getReaderSearch(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	s.fragment(w, r, http.StatusOK, pages.ReaderHitList(s.readerHits(rc, r.URL.Query().Get("q"))))
}

// readerSearchSheet is the search panel of the full page (`?sheet=search&q=`), for a browser without htmx too.
func (s *server) readerSearchSheet(r *http.Request, rc readerCtx, cur int) pages.ReaderSheetView {
	q := r.URL.Query().Get("q")
	ed := ""
	if rc.explicit {
		ed = rc.ed.ID
	}
	return pages.ReaderSheetView{Kind: "search", Title: readerSearchTitle, CloseLabel: readerCloseSheet, BookID: rc.book.ID,
		CloseHref: rc.href("ch", strconv.Itoa(cur)),
		Search: &pages.ReaderSearchView{Action: "/books/" + rc.book.ID + "/read", EditionID: ed, Q: q,
			Label: readerSearchField, Button: readerSearchGo, Hits: s.readerHits(rc, q)}}
}

// ---- Export ----

// rdrExport is one highlight or bookmark in an export.
type rdrExport struct {
	Kind         string `json:"kind"` // highlight or bookmark
	Chapter      int    `json:"chapter"`
	ChapterTitle string `json:"chapterTitle"`
	Quote        string `json:"quote"`
	Note         string `json:"note"`
	Colour       string `json:"colour,omitempty"`
	Created      string `json:"created"`
	CreatedAt    int64  `json:"createdAt"`
	sid          string
	off          int
}

func (s *server) getReaderMarksExport(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	hls, err := s.progress.Highlights(ctx, u.ID, rc.book.ID)
	if err != nil {
		s.serverError(w, r, "export highlights", err)
		return
	}
	bms, err := s.progress.TextBookmarks(ctx, u.ID, rc.book.ID)
	if err != nil {
		s.serverError(w, r, "export text bookmarks", err)
		return
	}
	title := func(edition string, ch int) string {
		if edition == rc.ed.ID && ch >= 1 && ch <= len(rc.doc.Chapters) {
			return rc.doc.Chapters[ch-1].Title
		}
		return ""
	}
	day := func(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02") }
	var list []rdrExport
	for _, h := range hls {
		list = append(list, rdrExport{Kind: "highlight", Chapter: h.Chapter, ChapterTitle: title(h.EditionID, h.Chapter), Quote: h.Quote,
			Note: h.Note, Colour: h.Colour, Created: day(h.CreatedAt), CreatedAt: h.CreatedAt, sid: h.StartSID, off: h.StartOff})
	}
	for _, b := range bms {
		c, _, _, _ := ebook.ParseSID(b.SID)
		list = append(list, rdrExport{Kind: "bookmark", Chapter: c, ChapterTitle: title(b.EditionID, c), Quote: rdrSentenceText(rc, b.EditionID, b.SID),
			Note: b.Note, Created: day(b.CreatedAt), CreatedAt: b.CreatedAt, sid: b.SID})
	}
	slices.SortStableFunc(list, func(a, b rdrExport) int {
		return cmp.Or(cmp.Compare(a.Chapter, b.Chapter), ebook.CompareSID(a.sid, b.sid), cmp.Compare(a.off, b.off), cmp.Compare(a.CreatedAt, b.CreatedAt))
	})
	ext := r.URL.Path[strings.LastIndexByte(r.URL.Path, '.')+1:]
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Disposition", `attachment; filename="`+bookmarkSlug(rc.book.Title)+`-highlights.`+ext+`"`)
	var body string
	switch ext {
	case "json":
		h.Set("Content-Type", "application/json")
		out := struct {
			Book  string      `json:"book"`
			Marks []rdrExport `json:"marks"`
		}{rc.book.Title, list}
		if out.Marks == nil {
			out.Marks = []rdrExport{}
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		body = string(b) + "\n"
	case "md":
		h.Set("Content-Type", "text/markdown; charset=utf-8")
		body = rdrExportText(rc.book.Title, list, true)
	default:
		h.Set("Content-Type", "text/plain; charset=utf-8")
		body = rdrExportText(rc.book.Title, list, false)
	}
	_, _ = w.Write([]byte(body))
}

// rdrSentenceText is the words of a sentence of the edition on screen, "" when the edition is another or the id is gone.
func rdrSentenceText(rc readerCtx, edition, sid string) string {
	if rc.doc == nil || edition != rc.ed.ID {
		return ""
	}
	c, ok := rdrSIDExists(rc.doc, sid)
	if !ok {
		return ""
	}
	bid := rdrBlockOf(sid)
	_, _, k, _ := ebook.ParseSID(sid)
	for _, b := range rc.doc.Chapters[c-1].Blocks {
		if b.ID == bid {
			units := utf16.Encode([]rune(b.Text))
			sent := b.Sentences[k]
			return string(utf16.Decode(units[min(sent[0], len(units)):min(sent[1], len(units))]))
		}
	}
	return ""
}

// rdrExportText writes the marks as Markdown or plain text: the title, then each chapter with its quotes and notes.
func rdrExportText(title string, list []rdrExport, md bool) string {
	var b strings.Builder
	if md {
		b.WriteString("# " + title + "\n")
	} else {
		b.WriteString(title + "\n" + strings.Repeat("=", len([]rune(title))) + "\n")
	}
	lastCh, lastTitle := -1, ""
	for _, e := range list {
		if e.Chapter != lastCh || e.ChapterTitle != lastTitle {
			lastCh, lastTitle = e.Chapter, e.ChapterTitle
			name := e.ChapterTitle
			if name == "" && e.Chapter > 0 {
				name = fmt.Sprintf(readerChapter, e.Chapter)
			}
			if name != "" {
				if md {
					b.WriteString("\n## " + name + "\n")
				} else {
					b.WriteString("\n" + name + "\n")
				}
			}
		}
		b.WriteString("\n")
		if e.Quote != "" {
			for _, line := range strings.Split(strings.TrimSpace(e.Quote), "\n") {
				if md {
					b.WriteString("> " + line + "\n")
				} else {
					b.WriteString("  " + line + "\n")
				}
			}
		}
		if e.Note != "" {
			if e.Quote != "" && md {
				b.WriteString("\n")
			}
			b.WriteString(e.Note + "\n")
		}
		kind := strings.ToUpper(e.Kind[:1]) + e.Kind[1:]
		if e.Colour != "" {
			kind += ", " + e.Colour
		}
		if md {
			b.WriteString("\n*" + kind + ", " + e.Created + "*\n")
		} else {
			b.WriteString("(" + kind + ", " + e.Created + ")\n")
		}
	}
	return b.String()
}
