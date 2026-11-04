package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The Ebooks to pair inbox (M5 R2 P3): ebooks that no audiobook has claimed. Every route needs
// share.ManageLibraries; pairing to a book also needs share.EditBook on that book. An ebook here is
// never a book: it has no row in any list, search or count. Pairing changes rows only.

const (
	inboxMaxRows  = 100
	inboxCloseMax = 5
	inboxFindMax  = 20
)

func (s *server) registerEbookInbox(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/ebooks", s.getEbookInbox)
	mux.HandleFunc("GET /admin/ebooks/{fileID}", s.getEbookPick)
	mux.HandleFunc("POST /admin/ebooks/{fileID}/pair", s.postEbookInboxPair)
	mux.HandleFunc("POST /admin/ebooks/{fileID}/keep", s.postEbookInboxKeep)
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

// inboxFile loads the unpaired file of the path. Anything else (unknown, paired, not ready) is a 404.
func (s *server) inboxFile(w http.ResponseWriter, r *http.Request, needReady bool) (ebook.File, bool) {
	f, found, err := s.ebooks.Store().File(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return f, false
	}
	if !found || f.BookID != "" || (needReady && f.State != "ready") {
		s.notFound(w, r)
		return f, false
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
	case ebook.ReasonAuthor:
		return inboxSameAuthor
	}
	return inboxSimilar
}

// inboxMatch turns a book card into a "Choose" row for one ebook.
func (s *server) inboxMatch(r *http.Request, f ebook.File, name string, c library.BookCard, reason string) pages.InboxMatch {
	return pages.InboxMatch{
		Title: c.Title, Byline: bylineOf(c.Authors), Reason: reason,
		Action: inboxPickHref(f.ID) + "/pair", CSRF: s.csrf.TokenFrom(r.Context()), BookID: c.ID,
		ChooseLabel: inboxChoose, Aria: fmt.Sprintf(inboxChooseAria, c.Title, name),
	}
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
	rows, total, err := s.ebooks.Store().Unpaired(ctx, inboxMaxRows)
	if err != nil {
		s.serverError(w, r, "unpaired ebooks", err)
		return
	}
	var ids []string
	for _, f := range rows {
		if f.State == "ready" && f.Edition != nil && f.Hold != "kept" {
			ids = append(ids, f.ID)
		}
	}
	cands, err := s.ebooks.CandidatesFor(ctx, ids, s.can.BookFilter(currentUser(r), "b"), 1)
	if err != nil {
		s.serverError(w, r, "ebook candidates", err)
		return
	}
	var bookIDs []string
	for _, cs := range cands {
		for _, c := range cs {
			bookIDs = append(bookIDs, c.BookID)
		}
	}
	cards, err := s.lib.CardsByID(ctx, s.scope(r), bookIDs)
	if err != nil {
		s.serverError(w, r, "ebook candidate books", err)
		return
	}
	byID := make(map[string]library.BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	v := pages.AdminEbooksView{Error: msg, Title: inboxTitle, Lead: inboxLead, Empty: inboxEmpty, KeptTitle: inboxKeptTitle, KeptLead: inboxKeptLead}
	for _, f := range rows {
		row := inboxRow(f)
		if f.State == "ready" && f.Edition != nil {
			row.PickHref, row.PickLabel = inboxPickHref(f.ID), inboxChooseBook
			for _, c := range cands[f.ID] {
				if card, ok := byID[c.BookID]; ok {
					m := s.inboxMatch(r, f.File, row.Name, card, inboxReason(c.Reason))
					row.Match = &m
				}
			}
		}
		if f.Hold == "kept" {
			v.Kept = append(v.Kept, row)
		} else {
			v.Waiting = append(v.Waiting, row)
		}
	}
	if total > len(rows) {
		v.More = fmt.Sprintf(inboxMore, total-len(rows))
	}
	styles := append(append([]string{}, adminStyles...), pages.TextStyles...)
	s.prefsPage(w, r, status, inboxTitle, "admin-libraries", "libraries", false, pages.AdminEbooks(v), nil, styles...)
}

// inboxRow is the name, meta line and (for a file that cannot be paired yet) the sentence of one row.
func inboxRow(f ebook.UnpairedFile) pages.InboxRow {
	row := pages.InboxRow{ID: f.ID, Name: inboxName(f.File, f.Edition)}
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
		CloseTitle: inboxClose, KeepAction: inboxPickHref(f.ID) + "/keep", KeepLabel: inboxKeep,
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
		found, err := s.lib.CardsByID(ctx, s.scope(r), res.IDs)
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

// postEbookInboxPair puts an unpaired ebook on the chosen book, if the person may edit that book.
func (s *server) postEbookInboxPair(w http.ResponseWriter, r *http.Request) {
	if !s.inboxAllowed(w, r) {
		return
	}
	f, ok := s.inboxFile(w, r, false)
	if !ok {
		return
	}
	bookID := strings.TrimSpace(r.PostFormValue("book"))
	if bookID == "" {
		s.inboxFail(w, r, nil, http.StatusUnprocessableEntity, inboxNoneChosen)
		return
	}
	book, ok := s.loadBook(w, r, bookID, share.EditBook)
	if !ok {
		return
	}
	if err := s.ebooks.Pair(r.Context(), f.ID, book.ID, currentUser(r).ID); err != nil {
		s.inboxFail(w, r, err, 0, "")
		return
	}
	s.answerPage(w, r, "/admin/ebooks")
}

// postEbookInboxKeep keeps an ebook on its own: it stays in the inbox, under "Kept on their own".
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
