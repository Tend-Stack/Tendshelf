package web

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// /admin/ebooks (M5 R2 P3, reshaped by B3): ebooks are books now, so this page is about the few that need a
// person. "Maybe the same book" lists text books that may be an audiobook the engine would not (or could
// not safely) join; "Could not be opened" lists files that have no book yet. Every route needs
// share.ManageLibraries; joining two books also needs share.EditBook on both. Joining changes rows only.

const (
	inboxMaxRows  = 100
	inboxCloseMax = 5
	inboxFindMax  = 20
)

func (s *server) registerEbookInbox(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/ebooks", s.getEbookInbox)
	mux.HandleFunc("GET /admin/ebooks/{fileID}", s.getEbookPick)
	mux.HandleFunc("POST /admin/ebooks/{fileID}/pair", s.postEbookInboxPair)
	mux.HandleFunc("POST /admin/ebooks/{fileID}/not-same", s.postEbookInboxNotSame)
	mux.HandleFunc("POST /admin/ebooks/{fileID}/keep", s.postEbookInboxKeep)
	mux.HandleFunc("GET /admin/ebooks/{fileID}/file", s.getEbookInboxFile)
}

// inboxAllowed answers 404 unless ebooks are on and the person may manage libraries.
func (s *server) inboxAllowed(w http.ResponseWriter, r *http.Request) bool {
	if !s.allowLibraries(w, r) {
		return false
	}
	if s.ebooks == nil {
		s.notFound(w, r)
		return false
	}
	return true
}

func inboxPickHref(fileID string) string { return "/admin/ebooks/" + fileID }

// inboxFile loads the file of the path: one with no book, or one on a text book. Anything else (unknown, on an
// audiobook, not ready when it must be) is a 404.
func (s *server) inboxFile(w http.ResponseWriter, r *http.Request, needReady bool) (ebook.File, bool) {
	f, found, err := s.ebooks.Store().File(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return f, false
	}
	if !found || (needReady && f.State != "ready") {
		s.notFound(w, r)
		return f, false
	}
	if f.BookID != "" {
		if _, onText, err := s.ebooks.Store().TextBookOf(r.Context(), f.ID); err != nil {
			s.serverError(w, r, "ebook book", err)
			return f, false
		} else if !onText {
			s.notFound(w, r)
			return f, false
		}
	}
	return f, true
}

// inboxReason is why a book was offered.
func inboxReason(r ebook.Reason) string {
	switch r {
	case ebook.ReasonISBN, ebook.ReasonASIN:
		return inboxSameISBN
	case ebook.ReasonTitle:
		return inboxSameTitle
	case ebook.ReasonFolder:
		return inboxSameFolder
	case ebook.ReasonAuthor:
		return inboxSameAuthor
	}
	return inboxSimilar
}

// inboxMatch turns a book card into a "Choose" row for one ebook.
func (s *server) inboxMatch(r *http.Request, f ebook.File, name string, c library.BookCard, reason string) pages.InboxMatch {
	m := pages.InboxMatch{
		Title: c.Title, Byline: bylineOf(c.Authors), Reason: reason,
		Action: inboxPickHref(f.ID) + "/pair", CSRF: s.csrf.TokenFrom(r.Context()), BookID: c.ID,
		ChooseLabel: inboxMake, Aria: fmt.Sprintf(inboxMakeAria, name, c.Title),
	}
	// A person's join skips the engine's guard; say first when grants on the ebook go along with it.
	if f.BookID != "" {
		if ok, err := s.ebooks.GuardAllows(r.Context(), f.BookID, c.ID); err == nil && !ok {
			m.Note = inboxGuard
		}
	}
	return m
}

// inboxName and inboxMeta are the title and the "authors · FORMAT · n pages" line of an unpaired ebook.
func inboxName(f ebook.File, ed *ebook.Edition) string {
	if ed != nil && ed.Title != "" {
		return ed.Title
	}
	return ebookName(f)
}

func (s *server) getEbookInbox(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	s.renderInbox(w, r, http.StatusOK, "")
}

func (s *server) renderInbox(w http.ResponseWriter, r *http.Request, status int, msg string) {
	ctx := r.Context()
	suggested, err := s.ebooks.Suggestions(ctx, s.can.BookFilter(currentUser(r), "b"), inboxMaxRows)
	if err != nil {
		s.serverError(w, r, "ebook suggestions", err)
		return
	}
	rows, total, err := s.ebooks.Store().Unpaired(ctx, inboxMaxRows)
	if err != nil {
		s.serverError(w, r, "ebooks without a book", err)
		return
	}
	v := pages.AdminEbooksView{Error: msg, Title: inboxTitle, Lead: inboxLead, Empty: inboxEmpty,
		MaybeTitle: inboxMaybeTitle, MaybeLead: inboxMaybeLead, CannotTitle: inboxCannotHead}
	csrf := s.csrf.TokenFrom(ctx)
	for _, sg := range suggested {
		d, err := s.lib.BookDetail(ctx, s.scope(r), sg.Match.BookID)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			s.serverError(w, r, "ebook suggestion book", err)
			return
		}
		chapters, err := s.ebooks.Store().Chapters(ctx, sg.Edition.ID)
		if err != nil {
			s.serverError(w, r, "ebook chapters", err)
			return
		}
		v.Maybe = append(v.Maybe, s.inboxSuggestion(sg, d, chapters, csrf))
	}
	for _, f := range rows {
		row := inboxRow(f)
		if f.State == "ready" && f.Edition != nil {
			row.PickHref, row.PickLabel = inboxPickHref(f.ID), inboxChooseBook
		}
		v.Cannot = append(v.Cannot, row)
	}
	if total > len(rows) {
		v.More = fmt.Sprintf(inboxMore, total-len(rows))
	}
	styles := append(append([]string{}, adminStyles...), pages.TextStyles...)
	s.prefsPage(w, r, status, inboxTitle, "admin-libraries", "libraries", false, pages.AdminEbooks(v), nil, styles...)
}

// inboxSuggestion is the card of one text book against the audiobook it may be, with the three answers.
func (s *server) inboxSuggestion(sg ebook.Suggestion, d library.BookDetail, chapters []ebook.ChapterRow, csrf string) pages.InboxSuggestion {
	f, ed := sg.File, sg.Edition
	name := inboxName(f, &ed)
	meta := ebook.EditionMeta(ed)
	audio := cardView(d.Card)
	key := ebook.BookKey{ID: d.Card.ID, Title: d.Card.Title, ISBN: d.Book.ISBN, ASIN: d.Book.ASIN}
	for _, a := range d.Card.Authors {
		key.Authors = append(key.Authors, a.Name)
	}
	var chTitles []string
	for _, c := range d.Chapters {
		chTitles = append(chTitles, c.Title)
	}
	card := components.PairingCardView{ID: f.ID, Audio: &audio, Title: name, Meta: ebookPairLine(meta.Authors, ed)}
	if f.Origin == "library" {
		card.FoundIn, card.Path = ebookFoundInLead, ebookShortPath(f.Path)
	}
	for _, c := range ebook.Checks(meta, key, chapters, chTitles) {
		card.Checks = append(card.Checks, components.PairingCheck{OK: c.OK, Text: ebookCheckText(c, name, meta.Authors)})
	}
	if sg.Guard {
		card.Warning = inboxGuard
	}
	base := inboxPickHref(f.ID)
	return pages.InboxSuggestion{
		ID: f.ID, Card: card,
		PickHref: base, PickLabel: inboxChooseBook,
		Make:    pages.InboxAction{Action: base + "/pair", Label: inboxMake, Kind: "primary", CSRF: csrf, BookID: d.Card.ID, Aria: fmt.Sprintf(inboxMakeAria, name, d.Card.Title)},
		NotSame: pages.InboxAction{Action: base + "/not-same", Label: ebookNotSame, Kind: "quiet", CSRF: csrf, BookID: d.Card.ID, Aria: ebookNotSame + ": " + name},
		Keep:    pages.InboxAction{Action: base + "/keep", Label: inboxKeep, Kind: "quiet", CSRF: csrf, Aria: inboxKeep + ": " + name},
	}
}

// inboxDownloadable says whether the original bytes of a file may be downloaded here: a file that failed to
// read, or a library file that looks damaged. A copy-protected file is never sent.
func inboxDownloadable(f ebook.File) bool {
	return f.State == "failed" || (f.State == "refused" && f.RefusedReason == ebook.ReasonDamaged && f.Origin == "library")
}

// getEbookInboxFile sends the original bytes of a file listed under "Could not be opened", so its owner can try
// it elsewhere. It is read-only and needs share.ManageLibraries.
func (s *server) getEbookInboxFile(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	f, found, err := s.ebooks.Store().File(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return
	}
	if !found || !inboxDownloadable(f) {
		s.notFound(w, r)
		return
	}
	file, err := s.ebooks.OpenSource(f)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err != nil {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": ebookName(f)}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", fi.ModTime(), file)
}

// inboxRow is the name, meta line and (for a file that cannot be paired yet) the sentence of one row.
func inboxRow(f ebook.UnpairedFile) pages.InboxRow {
	row := pages.InboxRow{ID: f.ID, Name: inboxName(f.File, f.Edition)}
	if inboxDownloadable(f.File) {
		row.DownloadHref, row.DownloadLabel, row.DownloadAr = inboxPickHref(f.ID)+"/file", inboxDownload, fmt.Sprintf(inboxDownloadAr, ebookName(f.File))
	}
	switch f.State {
	case "ready":
		if f.Edition == nil {
			row.Note = fmt.Sprintf(ebookReading, row.Name)
			return row
		}
		row.Meta = ebookPairLine(ebook.EditionMeta(*f.Edition).Authors, *f.Edition)
		if f.MissingSince > 0 {
			row.Note = ebookGone
		}
	case "pending":
		row.Note = fmt.Sprintf(ebookReading, row.Name)
	case "refused":
		row.Note = ebookRefusalText(f.File)
		if f.RefusedReason == ebook.ReasonDRM {
			row.Note = ebookDRMTitle
		}
	default:
		row.Note = ebookReadFailed
		if f.ErrorCode == "missing" {
			row.Note = ebookGone
		}
	}
	return row
}

func (s *server) getEbookPick(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	s.renderPick(w, r, http.StatusOK, "")
}

// renderPick draws "Which audiobook is it?" for the file of the path, with msg (a refusal) in an alert.
func (s *server) renderPick(w http.ResponseWriter, r *http.Request, status int, msg string) {
	f, ok := s.inboxFile(w, r, true)
	if !ok {
		return
	}
	ctx := r.Context()
	ed, found, err := s.ebooks.Store().CurrentEdition(ctx, f.ID)
	if err != nil {
		s.serverError(w, r, "ebook edition", err)
		return
	}
	if !found {
		s.notFound(w, r)
		return
	}
	cands, err := s.ebooks.Candidates(ctx, f.ID, s.can.BookFilter(currentUser(r), "b"), inboxCloseMax)
	if err != nil {
		s.serverError(w, r, "ebook candidates", err)
		return
	}
	name := inboxName(f, &ed)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	v := pages.AdminEbookPickView{
		Eyebrow: inboxEyebrow, Title: inboxPickTitle, BackLabel: inboxBack, BackText: inboxTitle, Error: msg,
		SearchAction: inboxPickHref(f.ID), SearchLabel: inboxSearchLabel, SearchGo: inboxSearchGo, Q: q,
		CloseTitle: inboxClose, KeepAction: inboxPickHref(f.ID) + "/keep", KeepLabel: inboxKeep, KeepNote: inboxKeepNote,
		CSRF: s.csrf.TokenFrom(ctx),
	}
	card := components.PairingCardView{ID: f.ID, Title: name, Meta: ebookPairLine(ebook.EditionMeta(ed).Authors, ed)}
	if f.Origin == "library" {
		card.FoundIn, card.Path = ebookFoundInLead, ebookShortPath(f.Path)
	}
	strong := false
	var ids []string
	reasons := map[string]string{}
	for _, c := range cands {
		ids = append(ids, c.BookID)
		reasons[c.BookID] = inboxReason(c.Reason)
		if c.Reason == ebook.ReasonTitle || c.Reason == ebook.ReasonISBN || c.Reason == ebook.ReasonASIN {
			strong = true
		}
	}
	if !strong {
		libs, err := s.lib.VisibleLibraries(ctx, s.scope(r))
		if err != nil {
			s.serverError(w, r, "visible libraries", err)
			return
		}
		n := 0
		for _, l := range libs {
			n += l.Books
		}
		card.NoMatch, card.Warning = true, fmt.Sprintf(inboxNoMatch, n)
	}
	v.Card = card
	cards, err := s.lib.CardsByID(ctx, s.scope(r), ids)
	if err != nil {
		s.serverError(w, r, "ebook candidate books", err)
		return
	}
	for _, c := range cards {
		v.Close = append(v.Close, s.inboxMatch(r, f, name, c, reasons[c.ID]))
	}
	if q != "" {
		v.Searched = true
		v.ResultsTitle, v.NoResults = fmt.Sprintf(inboxResultsTitle, q), fmt.Sprintf(inboxNoResults, q)
		res, err := s.lib.Search(ctx, s.scope(r), q, inboxFindMax)
		if err != nil {
			s.serverError(w, r, "search audiobooks", err)
			return
		}
		audioIDs, err := s.ebooks.Store().OnlyAudio(ctx, res.IDs)
		if err != nil {
			s.serverError(w, r, "search audiobooks", err)
			return
		}
		found, err := s.lib.CardsByID(ctx, s.scope(r), audioIDs)
		if err != nil {
			s.serverError(w, r, "search cards", err)
			return
		}
		for _, c := range found {
			v.Results = append(v.Results, s.inboxMatch(r, f, name, c, ""))
		}
	}
	styles := append(append([]string{}, adminStyles...), pages.TextStyles...)
	s.prefsPage(w, r, status, inboxPickTitle, "admin-libraries", "libraries", false, pages.AdminEbookPick(v), nil, styles...)
}

// inboxFail answers a refused action: a toast for htmx, the pick page with the sentence otherwise.
func (s *server) inboxFail(w http.ResponseWriter, r *http.Request, err error, status int, msg string) {
	switch {
	case err == nil:
	case errors.Is(err, ebook.ErrFileNotFound), errors.Is(err, ebook.ErrBookNotFound):
		s.notFound(w, r)
		return
	case errors.Is(err, ebook.ErrNotReady):
		status, msg = http.StatusConflict, ebookNotReadyYet
	case errors.Is(err, ebook.ErrAlreadyOnBook):
		status, msg = http.StatusConflict, ebookAlreadyHere
	default:
		s.serverError(w, r, "ebook inbox", err)
		return
	}
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
		return
	}
	if errors.Is(err, ebook.ErrNotReady) {
		s.renderInbox(w, r, status, msg)
		return
	}
	s.renderPick(w, r, status, msg)
}

// inboxAudiobook loads the audiobook named in the form, if the person may edit it. A text book, an unknown book
// or one they may not edit is a 404.
func (s *server) inboxAudiobook(w http.ResponseWriter, r *http.Request) (library.Book, bool) {
	bookID := strings.TrimSpace(r.PostFormValue("book"))
	if bookID == "" {
		s.inboxFail(w, r, nil, http.StatusUnprocessableEntity, inboxNoneChosen)
		return library.Book{}, false
	}
	book, ok := s.loadBook(w, r, bookID, share.EditBook)
	if !ok {
		return book, false
	}
	if book.Kind == library.BookText {
		s.notFound(w, r)
		return book, false
	}
	return book, true
}

// postEbookInboxPair is "Make one book": the ebook (a text book, or a file with no book yet) joins the chosen
// audiobook. It needs share.EditBook on both books; the engine's guard does not apply to a person.
func (s *server) postEbookInboxPair(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	f, ok := s.inboxFile(w, r, false)
	if !ok {
		return
	}
	to, ok := s.inboxAudiobook(w, r)
	if !ok {
		return
	}
	var err error
	if f.BookID == "" {
		err = s.ebooks.Pair(r.Context(), f.ID, to.ID, currentUser(r).ID)
	} else {
		from, ok := s.loadBook(w, r, f.BookID, share.EditBook)
		if !ok {
			return
		}
		err = s.ebooks.MergeBook(r.Context(), from.ID, to.ID, currentUser(r).ID)
	}
	if err != nil {
		s.inboxFail(w, r, err, 0, "")
		return
	}
	s.answerPage(w, r, "/admin/ebooks")
}

// postEbookInboxNotSame is "Not the same book" on a suggestion: nothing moves, and it is not offered again.
func (s *server) postEbookInboxNotSame(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	f, ok := s.inboxFile(w, r, false)
	if !ok {
		return
	}
	to, ok := s.inboxAudiobook(w, r)
	if !ok {
		return
	}
	if err := s.ebooks.NotSame(r.Context(), f.ID, to.ID, currentUser(r).ID); err != nil {
		s.inboxFail(w, r, err, 0, "")
		return
	}
	s.answerPage(w, r, "/admin/ebooks")
}

// postEbookInboxKeep keeps an ebook on its own: it stays a book, and no suggestion comes back for it.
func (s *server) postEbookInboxKeep(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	f, ok := s.inboxFile(w, r, false)
	if !ok {
		return
	}
	if err := s.ebooks.Keep(r.Context(), f.ID, currentUser(r).ID); err != nil {
		s.inboxFail(w, r, err, 0, "")
		return
	}
	s.answerPage(w, r, "/admin/ebooks")
}
