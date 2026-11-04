package web

import (
	"context"
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

// Pairing on the Text tab (M5 R2 P2): pair a found ebook with this book, say "Not the same book",
// choose the book's edition and your own. Pairing changes rows only; no ebook file is written.
// Pair, unpair, not-same and default need share.EditBook, choose needs share.ReadText. Anything
// refused answers 404 like a missing book.

func (s *server) registerEbookPairing(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/text/{fileID}/pair", s.postTextPair)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/unpair", s.postTextUnpair)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/not-same", s.postTextNotSame)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/default", s.postTextDefault)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/choose", s.postTextChoose)
}

// textCtx is what the Text tab knows about the book beyond its files: the audiobook as a cover and
// a key to compare with, and which file is whose edition.
type textCtx struct {
	userID, csrf  string
	edit, canRead bool
	audio         *components.CardView
	key           ebook.BookKey
	chTitles      []string
	// bookEd and yourEd are file ids; multi is true when the book has two or more ready files.
	bookEd, yourEd string
	multi          bool
}

// textContext gathers the audiobook's side of the comparison (editors) and the editions in play.
func (s *server) textContext(r *http.Request, book library.Book, edit bool, files []ebook.File) (*textCtx, error) {
	ctx := r.Context()
	u := currentUser(r)
	t := share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}
	tc := &textCtx{userID: u.ID, csrf: s.csrf.TokenFrom(ctx), edit: edit, canRead: s.can.Can(ctx, u, share.ReadText, t)}
	ready := 0
	for _, f := range files {
		if f.State == "ready" {
			ready++
		}
	}
	tc.multi = ready >= 2
	if tc.multi {
		if f, _, ok, err := s.ebooks.Store().EditionFor(ctx, "", book.ID); err != nil {
			return nil, err
		} else if ok {
			tc.bookEd = f.ID
		}
		if f, _, ok, err := s.ebooks.Store().EditionFor(ctx, u.ID, book.ID); err != nil {
			return nil, err
		} else if ok {
			tc.yourEd = f.ID
		}
	}
	if !edit {
		return tc, nil
	}
	d, err := s.lib.BookDetail(ctx, s.scope(r), book.ID)
	if err != nil {
		return nil, err
	}
	cv := cardView(d.Card)
	tc.audio = &cv
	tc.key = ebook.BookKey{ID: book.ID, Title: book.Title, ISBN: book.ISBN, ASIN: book.ASIN}
	for _, a := range d.Card.Authors {
		tc.key.Authors = append(tc.key.Authors, a.Name)
	}
	for _, c := range d.Chapters {
		tc.chTitles = append(tc.chTitles, c.Title)
	}
	return tc, nil
}

// action builds one button of the tab.
func (tc *textCtx) action(book library.Book, f ebook.File, verb, label, kind string) *pages.TextActionView {
	return &pages.TextActionView{
		Action: ebookTextHref(book.ID) + "/" + f.ID + "/" + verb, Label: label, Kind: kind, CSRF: tc.csrf,
		Aria: label + ": " + ebookName(f),
	}
}

// card is the pairing card of an ebook against this book.
func (tc *textCtx) card(f ebook.File, ed ebook.Edition, rows []ebook.ChapterRow) components.PairingCardView {
	meta := ebook.EditionMeta(ed)
	title := ed.Title
	if title == "" {
		title = ebookName(f)
	}
	v := components.PairingCardView{ID: f.ID, Audio: tc.audio, Title: title, Meta: ebookPairLine(meta.Authors, ed)}
	if f.Origin == "library" {
		v.FoundIn, v.Path = ebookFoundInLead, ebookShortPath(f.Path)
	}
	for _, c := range ebook.Checks(meta, tc.key, rows, tc.chTitles) {
		v.Checks = append(v.Checks, components.PairingCheck{OK: c.OK, Text: ebookCheckText(c, title, meta.Authors)})
	}
	return v
}

// ebookPairLine is "{authors} · {FORMAT} · {n} pages", or chapters when the edition has no page count.
func ebookPairLine(authors []string, ed ebook.Edition) string {
	size := countText(ed.ChapterCount, "1 chapter", "%d chapters")
	if ed.PageCount > 0 {
		size = countText(ed.PageCount, "1 page", ebookPagesOf)
	}
	format := strings.ToUpper(ed.Format)
	if len(authors) == 0 {
		return fmt.Sprintf(ebookPairMetaNoA, format, size)
	}
	return fmt.Sprintf(ebookPairMeta, strings.Join(authors, ", "), format, size)
}

// ebookCheckText is the sentence of one check.
func ebookCheckText(c ebook.Check, title string, authors []string) string {
	switch c.Kind {
	case "same_title":
		if c.Author == "" {
			return ebookSameTitleNA
		}
		return fmt.Sprintf(ebookSameTitle, c.Author)
	case "same_isbn":
		return fmt.Sprintf(ebookSameISBN, c.ISBN)
	case "chapters_same":
		return fmt.Sprintf(ebookChaptersSame, c.N)
	case "chapters_count":
		return fmt.Sprintf(ebookChaptersNum, c.N)
	case "chapters_differ":
		return fmt.Sprintf(ebookChaptersDiff, c.N, c.M)
	}
	if len(authors) == 0 {
		return fmt.Sprintf(ebookDiffTitleNA, title)
	}
	return fmt.Sprintf(ebookDiffTitle, title, strings.Join(authors, ", "))
}

// decorate adds what pairing and editions put on a ready file's row.
func (tc *textCtx) decorate(book library.Book, f ebook.File, fv *pages.TextFileView, ed ebook.Edition, rows []ebook.ChapterRow) {
	if tc.multi {
		if f.ID == tc.bookEd {
			fv.Chips = append(fv.Chips, ebookBookEdition)
		}
		if f.ID == tc.yourEd {
			fv.Chips = append(fv.Chips, ebookYourEdition)
		}
		if tc.canRead && f.ID != tc.yourEd {
			fv.Choose = tc.action(book, f, "choose", ebookReadThis, "secondary")
		}
		if tc.edit && f.ID != tc.bookEd {
			fv.MakeDefault = tc.action(book, f, "default", ebookMakeDefault, "secondary")
		}
	}
	if !tc.edit || f.BookID != book.ID {
		return
	}
	fv.NotSame = tc.action(book, f, "unpair", ebookNotSame, "quiet")
	switch f.PairedBy {
	case "folder", "isbn", "title":
		card := tc.card(f, ed, rows)
		fv.Card, fv.Source, fv.SourceIsPath = &card, "", false
	}
}

// suggestions are the unpaired ebooks that might be this book's text, for editors.
func (s *server) textSuggestions(ctx context.Context, book library.Book, tc *textCtx) ([]pages.TextSuggestionView, error) {
	found, err := s.ebooks.SuggestionsForBook(ctx, book.ID, 3)
	if err != nil {
		return nil, err
	}
	var out []pages.TextSuggestionView
	for _, f := range found {
		ed, ok, err := s.ebooks.Store().CurrentEdition(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		rows, err := s.ebooks.Store().Chapters(ctx, ed.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, pages.TextSuggestionView{
			Card:    tc.card(f, ed, rows),
			Pair:    *tc.action(book, f, "pair", s.pairLabel(ctx, book, ed), "primary"),
			NotSame: *tc.action(book, f, "not-same", ebookNotSame, "quiet"),
		})
	}
	return out, nil
}

// ebookPairFail answers a refused pairing action: an error toast for htmx, the tab with the
// sentence in its alert otherwise. Anything unknown is a 500.
func (s *server) ebookPairFail(w http.ResponseWriter, r *http.Request, book library.Book, err error) {
	var status int
	var msg string
	switch {
	case errors.Is(err, ebook.ErrFileNotFound), errors.Is(err, ebook.ErrBookNotFound):
		s.notFound(w, r)
		return
	case errors.Is(err, ebook.ErrNotReady):
		status, msg = http.StatusConflict, ebookNotReadyYet
	case errors.Is(err, ebook.ErrAlreadyOnBook):
		status, msg = http.StatusConflict, ebookAlreadyHere
	default:
		s.serverError(w, r, "ebook pairing", err)
		return
	}
	s.ebookTabRefuse(w, r, book, status, msg)
}

// ebookTabRefuse answers a refused Text tab action: an error toast for htmx, the tab with the sentence in
// its alert otherwise.
func (s *server) ebookTabRefuse(w http.ResponseWriter, r *http.Request, book library.Book, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		s.fragment(w, r, status, components.ErrorToast(msg))
		return
	}
	r2 := r.Clone(context.WithValue(r.Context(), ebookUploadErrKey{}, msg))
	r2.Method = http.MethodGet
	s.renderBook(w, r2, book.ID, "text", status, nil)
}

// ebookPairDone ends a pairing action: back to the tab on success.
func (s *server) ebookPairDone(w http.ResponseWriter, r *http.Request, book library.Book, err error) {
	if err != nil {
		s.ebookPairFail(w, r, book, err)
		return
	}
	s.answerPage(w, r, ebookTextHref(book.ID))
}

// postTextPair puts an unpaired ebook on this book. A file that is on another book moves only when
// the person may edit that book too.
func (s *server) postTextPair(w http.ResponseWriter, r *http.Request) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return
	}
	f, found, err := s.ebooks.Store().File(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return
	}
	if !found {
		s.notFound(w, r)
		return
	}
	if f.BookID != "" && f.BookID != book.ID {
		if _, ok := s.loadBook(w, r, f.BookID, share.EditBook); !ok {
			return
		}
	}
	err = s.ebooks.Pair(r.Context(), f.ID, book.ID, currentUser(r).ID)
	if err == nil {
		s.prepareAfterPair(r, book, f.ID)
	}
	s.ebookPairDone(w, r, book, err)
}

// postTextUnpair is "Not the same book" on a paired file: it leaves the book and is not offered again.
func (s *server) postTextUnpair(w http.ResponseWriter, r *http.Request) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return
	}
	s.ebookPairDone(w, r, book, s.ebooks.Unpair(r.Context(), r.PathValue("fileID"), book.ID, currentUser(r).ID))
}

// postTextNotSame is "Not the same book" on a suggestion.
func (s *server) postTextNotSame(w http.ResponseWriter, r *http.Request) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return
	}
	s.ebookPairDone(w, r, book, s.ebooks.NotSame(r.Context(), r.PathValue("fileID"), book.ID, currentUser(r).ID))
}

// postTextDefault makes a file the book's edition.
func (s *server) postTextDefault(w http.ResponseWriter, r *http.Request) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return
	}
	s.ebookPairDone(w, r, book, s.ebooks.SetBookDefault(r.Context(), book.ID, r.PathValue("fileID"), currentUser(r).ID))
}

// postTextChoose picks the edition the signed-in person reads. Anyone who may read the text can.
func (s *server) postTextChoose(w http.ResponseWriter, r *http.Request) {
	if s.ebooks == nil {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.ReadText)
	if !ok {
		return
	}
	s.ebookPairDone(w, r, book, s.ebooks.Choose(r.Context(), currentUser(r).ID, book.ID, r.PathValue("fileID")))
}
