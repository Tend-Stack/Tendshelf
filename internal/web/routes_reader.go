package web

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// The read-along reader (M5 R4 P2): the page, one chapter at a time as a fragment, the chapter's timing file, and
// the person's highlights. Every route answers 404 unless share.Can(user, ReadText, book); a share-link visitor
// has no session user, so share.Can refuses them. Highlights are the signed-in person's own.

func (s *server) registerReader(mux *http.ServeMux) {
	mux.HandleFunc("GET /books/{id}/read", s.getReader)
	mux.HandleFunc("GET /hx/books/{id}/read/chapters/{idx}", s.getReaderChapter)
	mux.HandleFunc("GET /books/{id}/read/timing/{idx}", s.getReaderTiming)
	mux.HandleFunc("POST /hx/books/{id}/highlights", s.postReaderHighlight)
	mux.HandleFunc("PATCH /hx/highlights/{id}", s.patchReaderHighlight)
	mux.HandleFunc("DELETE /hx/highlights/{id}", s.deleteReaderHighlight)
}

// readerDocCache keeps the last few parsed documents. Documents are content addressed, so a hit is always right.
type readerDocCache struct {
	mu   sync.Mutex
	keys []string
	docs map[string]*ebook.Doc
}

const readerDocsKept = 4

func (c *readerDocCache) get(dir, sha string) (*ebook.Doc, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d, ok := c.docs[sha]; ok {
		return d, nil
	}
	d, err := ebook.ReadDoc(dir, sha)
	if err != nil {
		return nil, err
	}
	if c.docs == nil {
		c.docs = map[string]*ebook.Doc{}
	}
	if len(c.keys) >= readerDocsKept {
		delete(c.docs, c.keys[0])
		c.keys = c.keys[1:]
	}
	c.docs[sha] = d
	c.keys = append(c.keys, sha)
	return d, nil
}

// readerCtx is what every reader route works on: the book, the edition shown, its document and its alignment.
type readerCtx struct {
	book     library.Book
	file     ebook.File
	ed       ebook.Edition
	doc      *ebook.Doc
	al       align.Alignment
	hasAl    bool
	rows     map[int]align.ChapterRow
	listen   bool
	explicit bool // the address named the edition
}

func (rc readerCtx) alongOK() bool { return rc.hasAl && rc.listen }

// href is a reader address with the edition (when the address named it) and the given query pairs.
func (rc readerCtx) href(kv ...string) string {
	q := url.Values{}
	if rc.explicit {
		q.Set("edition", rc.ed.ID)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u := "/books/" + rc.book.ID + "/read"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// edq is the edition query every fragment and timing address carries.
func (rc readerCtx) edq() string { return "edition=" + url.QueryEscape(rc.ed.ID) }

func (s *server) readerAlignDir() string { return filepath.Join(s.dataDir, "align") }

// readerCtx is the gate: the book is visible and readable, the edition is one of its ready ones, and the
// document opens. Anything else has answered 404.
func (s *server) readerCtx(w http.ResponseWriter, r *http.Request) (readerCtx, bool) {
	if s.ebooks == nil {
		s.notFound(w, r)
		return readerCtx{}, false
	}
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.ReadText)
	if !ok {
		return readerCtx{}, false
	}
	rc, found, err := s.readerLoad(r.Context(), currentUser(r), book, r.FormValue("edition"))
	if err != nil {
		s.serverError(w, r, "reader", err)
		return rc, false
	}
	if !found {
		s.notFound(w, r)
		return rc, false
	}
	return rc, true
}

// readerLoad opens the edition of a book the person may already read: the named one (it must be the current
// edition of a ready file on this book) or the one they read by default. found is false when there is none.
func (s *server) readerLoad(ctx context.Context, u users.User, book library.Book, editionID string) (rc readerCtx, found bool, err error) {
	rc.book = book
	st := s.ebooks.Store()
	if editionID != "" {
		ed, ok, err := st.Edition(ctx, editionID)
		if err != nil {
			return rc, false, err
		}
		var f ebook.File
		if ok {
			if f, ok, err = st.File(ctx, ed.EbookFileID); err != nil {
				return rc, false, err
			}
		}
		if !ok || f.BookID != book.ID || f.State != "ready" {
			return rc, false, nil
		}
		cur, ok, err := st.CurrentEdition(ctx, f.ID)
		if err != nil || !ok || cur.ID != ed.ID {
			return rc, false, err
		}
		rc.file, rc.ed, rc.explicit = f, ed, true
	} else {
		f, ed, ok, err := st.EditionFor(ctx, u.ID, book.ID)
		if err != nil || !ok || ed.ID == "" {
			return rc, false, err
		}
		rc.file, rc.ed = f, ed
	}
	doc, err := s.readerDocs.get(filepath.Join(s.dataDir, "text"), rc.ed.DocSHA256)
	if errors.Is(err, fs.ErrNotExist) {
		return rc, false, nil
	}
	if err != nil {
		return rc, false, err
	}
	rc.doc = doc
	if s.align != nil {
		al, has, err := s.align.Store().Latest(ctx, rc.ed.ID)
		if err != nil {
			return rc, false, err
		}
		if has {
			rows, err := s.align.Store().Chapters(ctx, al.ID)
			if err != nil {
				return rc, false, err
			}
			rc.al, rc.hasAl, rc.rows = al, true, make(map[int]align.ChapterRow, len(rows))
			for _, row := range rows {
				rc.rows[row.Idx] = row
			}
		}
	}
	rc.listen = s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID})
	return rc, true, nil
}

// rdrChap is one chapter ready to draw.
type rdrChap struct {
	view   pages.ReaderChapterView
	blocks map[string]pages.ReaderBlockView
	timing rdrTiming
}

// readerChapter builds chapter idx (1-based) for the person: its blocks with their sentences, highlights and flags.
func (s *server) readerChapter(ctx context.Context, u users.User, rc readerCtx, idx int, mode string) (rdrChap, error) {
	ch := rc.doc.Chapters[idx-1]
	out := rdrChap{blocks: map[string]pages.ReaderBlockView{}}
	row := rc.rows[idx]
	aligned := rc.hasAl && row.State == align.ChapterAligned && row.ChapterKey != "" && row.StartMS != nil && row.EndMS != nil
	if aligned {
		t, err := align.ReadTiming(s.readerAlignDir(), row.ChapterKey)
		if err != nil {
			slog.Error("reader timing", "key", row.ChapterKey, "err", err)
			aligned = false
		} else {
			out.timing = rdrTimingOf(t)
		}
	}
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
		switch {
		case b.SID != "":
			if c, _, _, ok := ebook.ParseSID(b.SID); ok && c == idx && b.EditionID == rc.ed.ID {
				bms[b.SID] = true
			}
		case aligned:
			if sid := rdrSIDAt(out.timing.t, b.PositionMs); sid != "" {
				bms[sid] = true
			}
		}
	}
	// Characters before this chapter, for the page numbers.
	before, total := 0, 0
	for _, c := range rc.doc.Chapters {
		for _, b := range c.Blocks {
			n := utf8.RuneCountInString(b.Text)
			if c.Idx < idx {
				before += n
			}
			total += n
		}
	}
	v := pages.ReaderChapterView{Idx: idx, Start: -1, End: -1, Title: ch.Title}
	if aligned {
		v.Start, v.End = *row.StartMS, *row.EndMS
		v.TimingHref = fmt.Sprintf("/books/%s/read/timing/%d?%s", rc.book.ID, idx, rc.edq())
	}
	cum, dropDone := before, false
	for _, b := range ch.Blocks {
		pg := 0
		if pc := rc.doc.PageCount; pc > 0 && total > 0 {
			pg = min(pc, cum*pc/total+1)
		}
		cum += utf8.RuneCountInString(b.Text)
		if b.Kind == ebook.KindImg && b.Text == "" {
			continue
		}
		bv := pages.ReaderBlockView{Kind: b.Kind, ID: "b-" + b.ID, Pg: pg}
		if b.Kind != ebook.KindHR {
			opts := rdrBlockOpts{Timing: out.timing, Aligned: aligned, Hls: hls, Bookmarks: bms}
			if b.Kind == ebook.KindP && !dropDone {
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
	if idx < len(rc.doc.Chapters) {
		v.NextFrag = fmt.Sprintf("/hx/books/%s/read/chapters/%d?%s&mode=%s", rc.book.ID, idx+1, rc.edq(), url.QueryEscape(mode))
		v.NextHref = rc.href("ch", strconv.Itoa(idx+1), "mode", mode)
		v.NextLabel = readerNextChapter
	}
	out.view = v
	return out, nil
}

// rdrSIDAt is the sentence of the timing file being spoken at ms, or "".
func rdrSIDAt(t *align.Timing, ms int64) string {
	if t == nil {
		return ""
	}
	for i := range t.SID {
		if t.Gap[i] == nil && t.SS[i] >= 0 && t.SS[i] <= ms && ms < t.SE[i] {
			return t.SID[i]
		}
	}
	return ""
}

// readerMode reads ?mode=; ok is false for a value that is not a mode.
func readerMode(rc readerCtx, q url.Values) (string, bool) {
	switch m := q.Get("mode"); m {
	case "":
		if rc.alongOK() {
			return "readalong", true
		}
		return "read", true
	case "readalong":
		if !rc.alongOK() {
			return "read", true
		}
		return m, true
	case "read":
		return m, true
	}
	return "", false
}

func (s *server) getReader(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	ctx, u, q := r.Context(), currentUser(r), r.URL.Query()
	mode, ok := readerMode(rc, q)
	if !ok {
		s.notFound(w, r)
		return
	}
	n := len(rc.doc.Chapters)
	ch := 0
	if q.Has("ch") {
		c, err := strconv.Atoi(q.Get("ch"))
		if err != nil || c < 1 || c > n {
			s.notFound(w, r)
			return
		}
		ch = c
	}
	if at := q.Get("at"); at != "" {
		c, ok := rdrSIDExists(rc.doc, at)
		if !ok {
			s.notFound(w, r)
			return
		}
		if ch == 0 {
			ch = c
		}
	}
	sheet := q.Get("sheet")
	if sheet != "" && sheet != "contents" && sheet != "marks" && sheet != "reading" {
		s.notFound(w, r)
		return
	}
	prog, err := s.playProgress(ctx, u.ID, rc.book.ID)
	if err != nil {
		s.serverError(w, r, "reader progress", err)
		return
	}
	if ch == 0 && prog != nil {
		for i := 1; i <= n && ch == 0; i++ {
			row := rc.rows[i]
			if row.State == align.ChapterAligned && row.StartMS != nil && row.EndMS != nil && prog.PositionMs >= *row.StartMS && prog.PositionMs < *row.EndMS {
				ch = i
			}
		}
	}
	if ch == 0 {
		ch = 1
		for i, c := range rc.doc.Chapters {
			if !c.Front {
				ch = i + 1
				break
			}
		}
	}
	chap, err := s.readerChapter(ctx, u, rc, ch, mode)
	if err != nil {
		s.serverError(w, r, "reader chapter", err)
		return
	}
	v, err := s.readerView(r, rc, mode, chap, prog)
	if err != nil {
		s.serverError(w, r, "reader view", err)
		return
	}
	var sh templ.Component
	switch sheet {
	case "":
	case "reading":
		sv, err := s.readingSheet(r, rc, mode, ch)
		if err != nil {
			s.serverError(w, r, "reading settings sheet", err)
			return
		}
		sh = pages.ReadingSheet(sv)
	default:
		sv, err := s.readerSheet(r, rc, sheet, ch)
		if err != nil {
			s.serverError(w, r, "reader sheet", err)
			return
		}
		sh = pages.ReaderSheet(sv)
	}
	s.pageSheetStyled(w, r, v.Title, pages.Reader(v), sh)
}

// pageSheetStyled renders the reader inside the layout with its stylesheet.
func (s *server) pageSheetStyled(w http.ResponseWriter, r *http.Request, title string, body, sheet templ.Component) {
	s.pageStyled(w, r, http.StatusOK, title, "", body, sheet, pages.ReaderStyles...)
}

// rdrSIDExists reports whether the sentence id names a sentence of the document, and its chapter.
func rdrSIDExists(d *ebook.Doc, sid string) (int, bool) {
	c, _, k, ok := ebook.ParseSID(sid)
	if !ok || c < 1 || c > len(d.Chapters) {
		return 0, false
	}
	bid := rdrBlockOf(sid)
	for _, b := range d.Chapters[c-1].Blocks {
		if b.ID == bid {
			return c, k < len(b.Sentences)
		}
	}
	return 0, false
}

// readerView assembles the page around a chapter.
func (s *server) readerView(r *http.Request, rc readerCtx, mode string, chap rdrChap, prog *progress.Progress) (pages.ReaderView, error) {
	ctx, u := r.Context(), currentUser(r)
	id := rc.book.ID
	v := pages.ReaderView{
		Title: rc.book.Title, BookID: id, EditionID: rc.ed.ID, Mode: mode,
		CloseHref: "/books/" + id, CloseLabel: readerClose, ModesLabel: readerModesLabel,
		Chapter: chap.view, Module: s.assetOr("js/readalong.js"), MarksModule: s.assetOr("js/reader-marks.js"),
		MsgLeft: "{n} min left in chapter", MsgPage: "p. {n} of {total}",
		Voice: readerBackVoice, ExitFocus: readerExitFocus, FocusHint: readerFocusHint,
		FocusBack: readerBackSentence, FocusFwd: readerFwdSentence,
	}
	if rc.listen {
		v.Modes = append(v.Modes, pages.ReaderModeLink{Label: readerModeListen, Href: "/books/" + id})
	}
	if rc.alongOK() {
		v.Modes = append(v.Modes, pages.ReaderModeLink{Label: readerModeAlong, Href: rc.href("mode", "readalong"), Current: mode == "readalong"})
	}
	v.Modes = append(v.Modes, pages.ReaderModeLink{Label: readerModeRead, Href: rc.href("mode", "read"), Current: mode == "read"})
	var spans []string
	for i := range rc.doc.Chapters {
		row := rc.rows[i+1]
		if row.State == align.ChapterAligned && row.StartMS != nil && row.EndMS != nil {
			spans = append(spans, fmt.Sprintf("%d,%d", *row.StartMS, *row.EndMS))
		} else {
			spans = append(spans, "-1,-1")
		}
	}
	v.Chapters = strings.Join(spans, ";")
	if rc.hasAl && rc.al.State == align.StatePartial {
		last := 0
		for idx, row := range rc.rows {
			if row.State == align.ChapterAligned && idx > last {
				last = idx
			}
		}
		if last > 0 {
			v.Banner = fmt.Sprintf(readerPartial, last)
		}
	}
	speed := "1"
	if set, err := s.users.LoadSettings(ctx, u.ID); err == nil {
		speed = strconv.FormatFloat(set.Speed, 'f', -1, 64)
	}
	rs, err := s.users.LoadReader(ctx, u.ID)
	if err != nil {
		return v, err
	}
	v.Look = readerLook(rs)
	v.SettingsLabel, v.SettingsHref = readerSettings, rc.href("mode", mode, "sheet", "reading")
	v.SettingsModule, v.SettingsAction, v.SettingsCSRF = s.assetOr("js/reader-settings.js"), readerSettingsRoute, s.csrf.TokenFrom(ctx)
	v.Dock = pages.ReaderDock{
		SpeedText: speed + "×", SpeedLabel: fmt.Sprintf(readerSpeedLabel, speed),
		BackLabel: readerBackSentence, FwdLabel: readerFwdSentence,
		ContentsLabel: readerContents, ContentsHref: rc.href("mode", mode, "sheet", "contents"),
		PageLine: chap.view.Page,
	}
	var link *playLink
	if rc.listen {
		var err error
		if link, err = s.playLinkFor(ctx, u, rc.book, prog); err != nil {
			return v, err
		}
	}
	if link != nil {
		v.Dock.Play = link.button(readerPlayLabel, true)
		if prog != nil && prog.PositionMs > 0 {
			rl := *link
			rl.Label = fmt.Sprintf(readerContinueFrom, clockText(prog.PositionMs))
			v.Dock.ReadPlay = rl.button("", false)
		} else {
			v.Dock.ReadPlay = link.button("", false)
		}
		if mode == "readalong" {
			v.EyesFree = &pages.ReaderEyesFree{Label: readerEyesFree, Src: s.assets.URL("js/eyes-free.js"), CSS: s.assets.URL("css/pages-eyes-free.css")}
		}
	}
	hls, err := s.progress.Highlights(ctx, u.ID, rc.book.ID)
	if err != nil {
		return v, err
	}
	v.Marks = pages.ReaderMarksBar{
		Label: readerSelLabel, Highlight: readerHighlight, Note: readerNote, Listen: readerListenHere, Copy: readerCopy,
		Action: "/hx/books/" + id + "/highlights", CSRF: s.csrf.TokenFrom(ctx), Listenable: rc.alongOK(),
		Count: fmt.Sprintf(readerMarksCount, len(hls)),
	}
	return v, nil
}

// readerSheet builds the contents or the marks panel.
func (s *server) readerSheet(r *http.Request, rc readerCtx, kind string, cur int) (pages.ReaderSheetView, error) {
	ctx, u, q := r.Context(), currentUser(r), r.URL.Query()
	mode, _ := readerMode(rc, q)
	sv := pages.ReaderSheetView{Kind: kind, CloseLabel: readerCloseSheet, BookID: rc.book.ID,
		CloseHref: rc.href("mode", mode, "ch", strconv.Itoa(cur))}
	if kind == "contents" {
		sv.Title = readerTocTitle
		n := 0
		for i, c := range rc.doc.Chapters {
			it := pages.ReaderContentsItem{Title: c.Title, Current: i+1 == cur, Href: rc.href("mode", mode, "ch", strconv.Itoa(i+1))}
			if c.Front {
				it.Note = readerFrontOnly
			} else {
				n++
				it.Num = strconv.Itoa(n)
			}
			sv.Chapters = append(sv.Chapters, it)
		}
		return sv, nil
	}
	sv.Title, sv.Lead, sv.Empty = readerMarksTitle, readerMarksLead, readerNoMarks
	hls, err := s.progress.Highlights(ctx, u.ID, rc.book.ID)
	if err != nil {
		return sv, err
	}
	bms, err := s.progress.Bookmarks(ctx, u.ID, rc.book.ID, false)
	if err != nil {
		return sv, err
	}
	csrf := s.csrf.TokenFrom(ctx)
	var hlItems, noteItems, bmItems []pages.ReaderMarkItem
	for _, h := range hls {
		it := s.readerMarkItem(rc, h, csrf)
		hlItems = append(hlItems, it)
		if h.Note != "" {
			noteItems = append(noteItems, it)
		}
	}
	for _, b := range bms {
		it := pages.ReaderMarkItem{ID: b.ID, BookID: rc.book.ID, Kind: readerKindBm, Note: b.Note, HasTime: true, StartMs: b.PositionMs,
			PlayLabel: fmt.Sprintf(readerPlayAt, clockText(b.PositionMs))}
		if c, _, _, ok := ebook.ParseSID(b.SID); ok && b.EditionID == rc.ed.ID {
			it.OpenHref = rc.href("ch", strconv.Itoa(c), "at", b.SID) + "#s-" + b.SID
			it.OpenLabel = fmt.Sprintf(readerOpenPage, c)
		}
		bmItems = append(bmItems, it)
	}
	show := q.Get("show")
	items := append(append([]pages.ReaderMarkItem{}, hlItems...), bmItems...)
	switch show {
	case "highlights":
		items = hlItems
	case "notes":
		items = noteItems
	case "bookmarks":
		items = bmItems
	default:
		show = "all"
	}
	filter := func(key, label string, n int) pages.ReaderFilter {
		return pages.ReaderFilter{Label: label, Count: n, Current: show == key,
			Href: rc.href("mode", mode, "ch", strconv.Itoa(cur), "sheet", "marks", "show", key)}
	}
	sv.Filters = []pages.ReaderFilter{
		filter("all", readerShowAll, len(hlItems)+len(bmItems)), filter("highlights", readerShowHl, len(hlItems)),
		filter("notes", readerShowNotes, len(noteItems)), filter("bookmarks", readerShowBm, len(bmItems)),
	}
	sv.Items = items
	return sv, nil
}

// readerMarkItem is the marks row of a highlight.
func (s *server) readerMarkItem(rc readerCtx, h progress.Highlight, csrf string) pages.ReaderMarkItem {
	it := pages.ReaderMarkItem{ID: h.ID, BookID: h.BookID, Kind: readerKindHl, Quote: h.Quote, Note: h.Note, CSRF: csrf,
		NoteHref: "/hx/highlights/" + h.ID, DeleteHref: "/hx/highlights/" + h.ID,
		SaveLabel: readerSaveNote, DeleteLabel: readerDelete, NoteField: readerNoteField}
	if h.Note != "" {
		it.Kind = readerKindNote
	}
	if h.StartMs != nil && h.EndMs != nil {
		it.HasTime, it.StartMs, it.EndMs = true, *h.StartMs, *h.EndMs
		it.PlayLabel = fmt.Sprintf(readerPlayAt, clockText(*h.StartMs))
	}
	if h.EditionID == rc.ed.ID {
		it.OpenHref = rc.href("ch", strconv.Itoa(h.Chapter), "at", h.StartSID) + "#s-" + h.StartSID
		it.OpenLabel = fmt.Sprintf(readerOpenPage, h.Chapter)
	}
	return it
}

func (s *server) getReaderChapter(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	mode, ok := readerMode(rc, r.URL.Query())
	idx, err := strconv.Atoi(r.PathValue("idx"))
	if !ok || err != nil || idx < 1 || idx > len(rc.doc.Chapters) {
		s.notFound(w, r)
		return
	}
	chap, err := s.readerChapter(r.Context(), currentUser(r), rc, idx, mode)
	if err != nil {
		s.serverError(w, r, "reader chapter fragment", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.ReaderChapter(chap.view))
}

// maxTimingPlain bounds what the plain (no gzip) answer inflates.
const maxTimingPlain = 64 << 20

func (s *server) getReaderTiming(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok {
		return
	}
	idx, err := strconv.Atoi(r.PathValue("idx"))
	row, has := rc.rows[idx]
	if err != nil || !has || row.State != align.ChapterAligned || row.ChapterKey == "" {
		s.notFound(w, r)
		return
	}
	f, err := align.OpenTiming(s.readerAlignDir(), row.ChapterKey)
	if errors.Is(err, fs.ErrNotExist) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "reader timing", err)
		return
	}
	defer f.Close()
	etag := `"` + row.ChapterKey + `"`
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("ETag", etag)
	h.Set("Cache-Control", "private, no-cache")
	h.Set("Vary", "Accept-Encoding")
	if static.MatchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if static.AcceptsGzip(r.Header.Get("Accept-Encoding")) {
		h.Set("Content-Encoding", "gzip")
		_, _ = io.Copy(w, f)
		return
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		s.serverError(w, r, "reader timing gzip", err)
		return
	}
	defer zr.Close()
	_, _ = io.Copy(w, io.LimitReader(zr, maxTimingPlain))
}

// readerHx refuses a request that did not come from htmx: the highlight routes have no plain form.
func (s *server) readerHx(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("HX-Request") == "true" {
		return true
	}
	http.Error(w, "This works from the reader page.", http.StatusBadRequest)
	return false
}

func (s *server) readerToast(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.fragment(w, r, status, components.ErrorToast(msg))
}

func (s *server) postReaderHighlight(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.readerCtx(w, r)
	if !ok || !s.readerHx(w, r) {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	startSID, endSID := r.PostFormValue("start_sid"), r.PostFormValue("end_sid")
	so, err1 := strconv.Atoi(r.PostFormValue("start_off"))
	eo, err2 := strconv.Atoi(r.PostFormValue("end_off"))
	sc, _, _, ok1 := ebook.ParseSID(startSID)
	ec, _, _, ok2 := ebook.ParseSID(endSID)
	if err1 != nil || err2 != nil || !ok1 || !ok2 || sc < 1 || sc > len(rc.doc.Chapters) {
		s.readerToast(w, r, http.StatusBadRequest, readerHlBad)
		return
	}
	if sc != ec {
		s.readerToast(w, r, http.StatusBadRequest, readerHlOneChapter)
		return
	}
	chDoc := rc.doc.Chapters[sc-1]
	q, ok := rdrQuoteOf(chDoc, startSID, so, endSID, eo)
	if !ok {
		s.readerToast(w, r, http.StatusBadRequest, readerHlBad)
		return
	}
	h := progress.Highlight{UserID: u.ID, BookID: rc.book.ID, EditionID: rc.ed.ID, StartSID: startSID, EndSID: endSID,
		StartOff: so, EndOff: eo, Chapter: sc, Quote: q.Quote, Prefix: q.Prefix, Suffix: q.Suffix,
		Colour: r.PostFormValue("colour"), Note: r.PostFormValue("note"), ClientID: r.PostFormValue("client_id")}
	if row := rc.rows[sc]; rc.hasAl && row.State == align.ChapterAligned && row.ChapterKey != "" {
		if t, err := align.ReadTiming(s.readerAlignDir(), row.ChapterKey); err == nil {
			h.AlignmentID = rc.al.ID
			h.StartMs, h.EndMs = rdrTimes(t, startSID, so, endSID, eo)
		}
	}
	saved, _, err := s.progress.AddHighlight(ctx, h)
	var invalid *progress.InvalidError
	switch {
	case errors.Is(err, progress.ErrLimit):
		s.readerToast(w, r, http.StatusConflict, readerHlLimit)
		return
	case errors.Is(err, progress.ErrClientIDTaken):
		s.readerToast(w, r, http.StatusConflict, readerHlBad)
		return
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
		return
	case errors.As(err, &invalid) && invalid.Field == "note":
		s.readerToast(w, r, http.StatusBadRequest, readerNoteLong)
		return
	case errors.As(err, &invalid):
		slog.Warn("highlight refused", "field", invalid.Field)
		s.readerToast(w, r, http.StatusBadRequest, readerHlBad)
		return
	case err != nil:
		s.serverError(w, r, "add highlight", err)
		return
	}
	parts, err := s.readerOOB(r, rc, saved)
	if err != nil {
		s.serverError(w, r, "highlight blocks", err)
		return
	}
	parts = append(parts, components.ToastOOB("ok", readerHighlighted))
	s.fragment(w, r, http.StatusOK, templ.Join(parts...))
}

// readerOOB re-draws the blocks a highlight touches (when its edition is the one on screen) and the count.
func (s *server) readerOOB(r *http.Request, rc readerCtx, h progress.Highlight) ([]templ.Component, error) {
	ctx, u := r.Context(), currentUser(r)
	var parts []templ.Component
	if rc.doc != nil && h.EditionID == rc.ed.ID && h.Chapter >= 1 && h.Chapter <= len(rc.doc.Chapters) {
		chap, err := s.readerChapter(ctx, u, rc, h.Chapter, "read")
		if err != nil {
			return nil, err
		}
		sb, eb := rdrBlockOf(h.StartSID), rdrBlockOf(h.EndSID)
		in := false
		for _, b := range rc.doc.Chapters[h.Chapter-1].Blocks {
			if b.ID == sb {
				in = true
			}
			if bv, ok := chap.blocks[b.ID]; ok && in {
				parts = append(parts, pages.ReaderBlock(bv, true))
			}
			if b.ID == eb {
				break
			}
		}
	}
	all, err := s.progress.Highlights(ctx, u.ID, rc.book.ID)
	if err != nil {
		return nil, err
	}
	return append(parts, pages.ReaderMarksCount(fmt.Sprintf(readerMarksCount, len(all)))), nil
}

// ownHighlight loads the person's own highlight named by the path and checks they may still read its book. The
// reader context is the highlight's edition when that is still readable; rc.doc is nil when it is not (the
// change is then saved without drawing text). Anything else has answered 404.
func (s *server) ownHighlight(w http.ResponseWriter, r *http.Request) (progress.Highlight, readerCtx, bool) {
	var rc readerCtx
	u := currentUser(r)
	h, err := s.progress.Highlight(r.Context(), u.ID, r.PathValue("id"))
	if errors.Is(err, progress.ErrNotFound) {
		s.notFound(w, r)
		return h, rc, false
	}
	if err != nil {
		s.serverError(w, r, "load highlight", err)
		return h, rc, false
	}
	book, ok := s.loadBook(w, r, h.BookID, share.ReadText)
	if !ok || s.ebooks == nil {
		if ok {
			s.notFound(w, r)
		}
		return h, rc, false
	}
	rc, found, err := s.readerLoad(r.Context(), u, book, h.EditionID)
	if err != nil {
		s.serverError(w, r, "highlight reader", err)
		return h, rc, false
	}
	if !found {
		rc = readerCtx{book: book}
	}
	return h, rc, true
}

func (s *server) patchReaderHighlight(w http.ResponseWriter, r *http.Request) {
	if !s.readerHx(w, r) {
		return
	}
	h, rc, ok := s.ownHighlight(w, r)
	if !ok {
		return
	}
	var note, colour *string
	if r.PostForm == nil {
		_ = r.ParseForm()
	}
	if r.Form.Has("note") {
		v := r.Form.Get("note")
		note = &v
	}
	if r.Form.Has("colour") {
		v := r.Form.Get("colour")
		colour = &v
	}
	saved, err := s.progress.SetHighlight(r.Context(), currentUser(r).ID, h.ID, note, colour)
	var invalid *progress.InvalidError
	switch {
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
		return
	case errors.As(err, &invalid) && invalid.Field == "note":
		s.readerToast(w, r, http.StatusBadRequest, readerNoteLong)
		return
	case errors.As(err, &invalid):
		s.readerToast(w, r, http.StatusBadRequest, readerHlBad)
		return
	case err != nil:
		s.serverError(w, r, "change highlight", err)
		return
	}
	parts, err := s.readerOOB(r, rc, saved)
	if err != nil {
		s.serverError(w, r, "highlight blocks", err)
		return
	}
	item := s.readerMarkItem(rc, saved, s.csrf.TokenFrom(r.Context()))
	item.OOB = true
	parts = append(parts, pages.ReaderMarkRow(item), components.ToastOOB("ok", readerNoteSaved))
	s.fragment(w, r, http.StatusOK, templ.Join(parts...))
}

func (s *server) deleteReaderHighlight(w http.ResponseWriter, r *http.Request) {
	if !s.readerHx(w, r) {
		return
	}
	h, rc, ok := s.ownHighlight(w, r)
	if !ok {
		return
	}
	err := s.progress.DeleteHighlight(r.Context(), currentUser(r).ID, h.ID)
	if errors.Is(err, progress.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "delete highlight", err)
		return
	}
	parts, err := s.readerOOB(r, rc, h)
	if err != nil {
		s.serverError(w, r, "highlight blocks", err)
		return
	}
	parts = append(parts, pages.ReaderMarkGone(h.ID), components.ToastOOB("ok", readerHlRemoved))
	s.fragment(w, r, http.StatusOK, templ.Join(parts...))
}

// assetOr is the hashed address of a static file, or "" while the file is not shipped yet (P3 adds the reader's scripts).
func (s *server) assetOr(name string) string {
	if !s.assets.Has(name) {
		return ""
	}
	return s.assets.URL(name)
}
