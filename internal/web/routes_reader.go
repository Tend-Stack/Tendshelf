package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The reader: the page, one chapter at a time as a fragment, and the person's highlights. Every route answers 404 unless share.Can(user, ReadText, book); a share-link visitor
// has no session user, so share.Can refuses them. Highlights are the signed-in person's own.

func (s *server) registerReader(mux *http.ServeMux) {
	mux.HandleFunc("GET /books/{id}/read", s.getReader)
	mux.HandleFunc("GET /hx/books/{id}/read/chapters/{idx}", s.getReaderChapter)
	mux.HandleFunc("GET /books/{id}/read/images/{blockID}", s.getReaderImage)
	mux.HandleFunc("POST /hx/books/{id}/highlights", s.postReaderHighlight)
	mux.HandleFunc("PATCH /hx/highlights/{id}", s.patchReaderHighlight)
	mux.HandleFunc("DELETE /hx/highlights/{id}", s.deleteReaderHighlight)
	s.registerReaderMarks(mux)
	s.registerLookup(mux)
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

// readerCtx is what every reader route works on: the book, the edition shown and its document.
type readerCtx struct {
	book     library.Book
	file     ebook.File
	ed       ebook.Edition
	doc      *ebook.Doc
	explicit bool // the address named the edition
}

// href is a reader address with the edition (when the address named it) and the given query pairs.
func (rc readerCtx) href(kv ...string) string {
	ed := ""
	if rc.explicit {
		ed = rc.ed.ID
	}
	return readerHref(rc.book.ID, ed, kv...)
}

// readerHref is the reader address of a book: editionID (when not empty) and the key/value pairs become the
// query, and a pair with an empty value is left out.
func readerHref(bookID, editionID string, kv ...string) string {
	q := url.Values{}
	if editionID != "" {
		q.Set("edition", editionID)
	}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	u := "/books/" + bookID + "/read"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// edq is the edition query every fragment address carries.
func (rc readerCtx) edq() string { return "edition=" + url.QueryEscape(rc.ed.ID) }

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
	if errors.Is(err, ebook.ErrDocCorrupt) || errors.Is(err, ebook.ErrDocTooLarge) {
		s.readerTrouble(w, r, rc, err)
		return rc, false
	}
	if err != nil {
		s.serverError(w, r, "reader", err)
		return rc, false
	}
	if !found {
		if rc.ed.ID != "" && rc.doc == nil { // the edition is on record but its document will not load
			s.readerTrouble(w, r, rc, errReaderDoc)
			return rc, false
		}
		s.notFound(w, r)
		return rc, false
	}
	return rc, true
}

// errReaderDoc is the log cause of a book whose document file is missing.
var errReaderDoc = errors.New("reader: the document file is missing")

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
		if !ok || f.BookID != book.ID || (f.State != "ready" && f.State != "pending") {
			return rc, false, nil
		}
		// The edition named must be the file's current one, or, while the file is read again, its newest.
		cur, ok, err := st.CurrentEdition(ctx, f.ID)
		if err == nil && !ok {
			cur, ok, err = st.NewestEdition(ctx, f.ID)
		}
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
	return rc, true, nil
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

// readerWords is the copy the client script needs, as JSON on the reader root (reader.js has no words of its own).
func readerWords() string {
	b, _ := json.Marshal(map[string]string{"pageOf": readerPageOf, "ch": readerLeftChapter, "chAbout": readerLeftChapAbt,
		"bk": readerLeftBook, "bkAbout": readerLeftBookAbt, "further": readerAnotherDev, "go": readerGoThere, "stay": readerStayHere,
		"back": readerBack, "failed": readerPartFailed, "retry": readerTryAgain, "prev": readerPrevChapter, "needsNet": readerNeedsNet})
	return string(b)
}

// readerAloud is the read-aloud strip's words and address (decision 0032). The JSON holds the words reader-aloud.js shows by itself.
func (s *server) readerAloud() *pages.ReaderAloudView {
	copyJSON, _ := json.Marshal(map[string]string{"end": readerAloudEnd, "noPart": readerAloudNoPart, "failed": readerAloudFailed,
		"blocked": readerAloudBlocked, "play": readerAloudPlay, "pause": readerAloudPause, "default": readerAloudDefault, "other": readerAloudOther})
	var speeds []pages.ReaderAloudSpeed
	for _, v := range []string{"0.8", "1", "1.2", "1.5", "1.75", "2"} {
		speeds = append(speeds, pages.ReaderAloudSpeed{Value: v, Text: strings.Replace(msgSpeedValue, "{v}", v, 1)})
	}
	return &pages.ReaderAloudView{Src: s.assetOr("js/reader-aloud.js"), Label: readerAloud, Play: readerAloudPlay, Pause: readerAloudPause,
		Back: readerAloudBack, Fwd: readerAloudFwd, Stop: readerAloudStop, More: readerAloudMore, Voice: readerAloudVoice,
		Default: readerAloudDefault, Other: readerAloudOther, Speed: readerAloudSpeed, Follow: readerAloudFollow, Speeds: speeds, Copy: string(copyJSON)}
}

// readerView assembles the page around a chapter.
func (s *server) readerView(r *http.Request, rc readerCtx, chap rdrChap, pl rsPlace) (pages.ReaderView, error) {
	ctx, u := r.Context(), currentUser(r)
	id := rc.book.ID
	v := pages.ReaderView{
		Title: rc.book.Title, BookID: id, EditionID: rc.ed.ID,
		CloseHref: "/books/" + id, CloseLabel: readerClose, ShowBarLabel: readerShowBar,
		Chapter: chap.view, MarksModule: s.assetOr("js/reader-marks.js"), ReaderModule: s.assetOr("js/reader.js"), PagesModule: s.assetOr("js/reader-pages.js"),
		SkimModule: s.assetOr("js/reader-skim.js"), Offline: &pages.ReaderOfflineView{Module: s.assetOr("js/reader-offline.js")},
		Aloud: s.readerAloud(),
	}
	rs, err := s.users.LoadReader(ctx, u.ID)
	if err != nil {
		return v, err
	}
	v.SearchLabel, v.SearchHref = readerSearch, rc.href("sheet", "search")
	v.BookmarkLabel, v.BookmarkURL = readerBookmarkPage, "/books/"+id+"/bookmarks"
	v.Look = readerLook(rs)
	v.SettingsLabel, v.SettingsHref = readerSettings, rc.href("sheet", "reading")
	v.SettingsModule = s.assetOr("js/reader-settings.js")
	v.ComfortSrc = s.assetOr("js/reader-comfort.js")
	if rs.KeepAwake || rs.Warm != "off" || rs.AutoHide {
		v.ComfortModule = v.ComfortSrc // only a person who turned a comfort setting on loads it
	}
	v.Footer = pages.ReaderFooter{PageLine: chap.view.Page, ContentsLabel: readerContents, ContentsHref: rc.href("sheet", "contents"),
		Ruler: rsRuler(rc.doc, chap)}
	v.MarksLabel, v.MarksHref = readerMarksTitle, rc.href("sheet", "marks")
	cpm, learned := s.progress.ReadingSpeed(ctx, u.ID)
	chChars := 0
	for _, b := range rc.doc.Chapters[chap.view.Idx-1].Blocks {
		chChars += rsUnits(b.Text)
	}
	v.Root = pages.ReaderRoot{
		At: pl.At, Off: strconv.Itoa(pl.Off), Cpm: cpm, Learned: learned, TotalChars: chap.total,
		Before: chap.view.Before, ChapterChars: chChars, Save: "/reading/" + id,
		Flow: rs.Flow, Lang: rc.doc.Meta.Language,
		Pages: rc.doc.PageCount, Chapters: len(rc.doc.Chapters), Words: readerWords(),
		Skim: rsSkim(rc.doc), NotesSrc: s.assetOr("js/reader-notes.js"), LookupSrc: s.assetOr("js/reader-lookup.js"),
	}
	// The speaker: only with the right to listen and an audiobook to play. It starts at the audiobook's own
	// saved place (never the reading place) and writes neither position itself.
	p, err := s.playProgress(ctx, u.ID, rc.book.ID)
	if err != nil {
		return v, err
	}
	link, err := s.playLinkFor(ctx, u, rc.book, p)
	if err != nil {
		return v, err
	}
	if link != nil {
		btn := link.button(readerSpeaker, true)
		btn.Icon = "output" // the set's speaker with sound waves
		v.Speaker = btn
	}
	hls, err := s.progress.Highlights(ctx, u.ID, rc.book.ID)
	if err != nil {
		return v, err
	}
	v.Marks = pages.ReaderMarksBar{
		Label: readerSelLabel, Highlight: readerHighlight, Note: readerNote, Copy: readerCopy,
		Lookup: lookupTitle, WordAction: "/hx/books/" + id + "/words",
		Action: "/hx/books/" + id + "/highlights", CSRF: s.csrf.TokenFrom(ctx),
		Count: fmt.Sprintf(readerMarksCount, len(hls)), Swatches: readerSwatches(), Save: readerSave, Cancel: readerCancel,
	}
	return v, nil
}

// readerSheet builds the contents or the marks panel.
func (s *server) readerSheet(r *http.Request, rc readerCtx, kind string, cur int) (pages.ReaderSheetView, error) {
	ctx, u, q := r.Context(), currentUser(r), r.URL.Query()
	sv := pages.ReaderSheetView{Kind: kind, CloseLabel: readerCloseSheet, BookID: rc.book.ID,
		CloseHref: rc.href("ch", strconv.Itoa(cur))}
	if kind == "search" {
		return s.readerSearchSheet(r, rc, cur), nil
	}
	if kind == "contents" {
		sv.Title = readerTocTitle
		n := 0
		for i, c := range rc.doc.Chapters {
			it := pages.ReaderContentsItem{Title: c.Title, Current: i+1 == cur, Href: rc.href("ch", strconv.Itoa(i+1))}
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
	bms, err := s.progress.TextBookmarks(ctx, u.ID, rc.book.ID)
	if err != nil {
		return sv, err
	}
	md, txt := "/books/"+rc.book.ID+"/read/marks.md", "/books/"+rc.book.ID+"/read/marks.txt"
	if rc.explicit {
		md, txt = md+"?edition="+url.QueryEscape(rc.ed.ID), txt+"?edition="+url.QueryEscape(rc.ed.ID)
	}
	sv.Exports = []pages.ReaderExport{{Label: readerExport + ": " + readerExportMd, Href: md}, {Label: readerExport + ": " + readerExportTxt, Href: txt}}
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
		it := pages.ReaderMarkItem{ID: b.ID, BookID: rc.book.ID, Kind: readerKindBm, Note: b.Note, Quote: rdrSentenceText(rc, b.EditionID, b.SID),
			CSRF: csrf, DeleteHref: "/bookmarks/" + b.ID + "/delete", PostDelete: true, DeleteLabel: readerDelete}
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
			Href: rc.href("ch", strconv.Itoa(cur), "sheet", "marks", "show", key)}
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
	if h.EditionID == rc.ed.ID {
		it.OpenHref = rc.href("ch", strconv.Itoa(h.Chapter), "at", h.StartSID) + "#s-" + h.StartSID
		it.OpenLabel = fmt.Sprintf(readerOpenPage, h.Chapter)
	}
	return it
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
		chap, err := s.readerChapter(ctx, u, rc, h.Chapter, 0)
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
