package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/a-h/templ"
)

// The Text tab (M5 R1): a book tab that lists the ebooks paired with the book, with their chapters and
// first lines, and takes an ebook upload. Reading needs share.ReadText, adding and clearing need
// share.EditBook (owner and admin). Anything refused answers 404 like a missing book.

func (s *server) registerEbooks(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/text", s.postEbookUpload)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/dismiss", s.postEbookDismiss)
	s.registerEbookPairing(mux)
}

// ebookUploadErrKey carries the refusal of an upload to the page render that shows it.
type ebookUploadErrKey struct{}

func ebookTextHref(bookID string) string { return bookTabHref(bookID, "text") }

// textAccess says what the person may do with the Text tab of a book: edit (add and clear files) or
// read (ready editions only, and only when the book has one). Both are false when ebooks are off.
func (s *server) textAccess(r *http.Request, book library.Book) (edit, read bool, err error) {
	if s.ebooks == nil {
		return false, false, nil
	}
	u := currentUser(r)
	t := share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}
	if s.can.Can(r.Context(), u, share.EditBook, t) {
		return true, false, nil
	}
	if !s.can.Can(r.Context(), u, share.ReadText, t) {
		return false, false, nil
	}
	read, err = s.ebooks.Store().ReadyForBook(r.Context(), book.ID)
	return false, read, err
}

// textView builds the Text tab. all lists every chapter instead of the first few; upErr is the plain
// sentence of an upload that was just refused.
func (s *server) textView(r *http.Request, book library.Book, edit, all bool, upErr string) (pages.TextView, error) {
	ctx := r.Context()
	files, err := s.ebooks.Store().FilesForBook(ctx, book.ID)
	if err != nil {
		return pages.TextView{}, err
	}
	v := pages.TextView{Heading: ebookTabLabel}
	csrf := s.csrf.TokenFrom(ctx)
	if edit {
		v.Heading, v.Lead = ebookAddTitle, ebookAddLead
		v.Upload = &pages.TextUploadView{
			Heading: ebookOrAddLabel, Label: ebookChooseLabel, Hint: ebookHint, Go: ebookAddGo,
			Action: ebookTextHref(book.ID), CSRF: csrf, Error: upErr,
		}
	}
	tc, err := s.textContext(r, book, edit, files)
	if err != nil {
		return pages.TextView{}, err
	}
	if edit {
		if v.Suggestions, err = s.textSuggestions(ctx, book, tc); err != nil {
			return pages.TextView{}, err
		}
		if len(v.Suggestions) > 0 {
			v.FoundLabel = ebookFoundLabel
		}
	}
	for _, f := range files {
		if !edit && f.State != "ready" {
			continue
		}
		fv, err := s.textFileView(ctx, book, f, edit, all, csrf, tc)
		if err != nil {
			return pages.TextView{}, err
		}
		if f.Origin == "library" && edit {
			v.FoundLabel = ebookFoundLabel
		}
		v.Files = append(v.Files, fv)
	}
	return v, nil
}

// textFileView is one file of the book in the words of its state.
func (s *server) textFileView(ctx context.Context, book library.Book, f ebook.File, edit, all bool, csrf string, tc *textCtx) (pages.TextFileView, error) {
	fv := pages.TextFileView{ID: f.ID, State: f.State, Name: ebookName(f)}
	if edit {
		fv.Source = ebookFromThis
		if f.Origin == "library" {
			fv.Source, fv.SourceIsPath = fmt.Sprintf(ebookFoundIn, ebookShortPath(f.Path)), true
		}
	}
	if edit && f.Origin == "upload" && (f.State == "refused" || f.State == "failed") {
		fv.ClearAction = ebookTextHref(book.ID) + "/" + f.ID + "/dismiss"
		fv.ClearLabel, fv.ClearAria, fv.CSRF = ebookClear, fmt.Sprintf(ebookClearLabel, fv.Name), csrf
	}
	switch f.State {
	case "ready":
		ed, ok, err := s.ebooks.Store().CurrentEdition(ctx, f.ID)
		if err != nil {
			return fv, err
		}
		if !ok {
			// Ready but with no edition for the present bytes: the next read will bring it.
			fv.State, fv.Reading = "pending", fmt.Sprintf(ebookReading, fv.Name)
			return fv, nil
		}
		rows, err := s.ebooks.Store().Chapters(ctx, ed.ID)
		if err != nil {
			return fv, err
		}
		fv.Summary = fmt.Sprintf(ebookSummary, strings.ToUpper(ed.Format),
			countText(ed.ChapterCount, "1 chapter", "%d chapters"), ebookWords(ed.WordCount))
		if ed.Layout == "page" {
			fv.Note = ebookPDFLayout
		}
		if edit && f.MissingSince > 0 {
			fv.Note = ebookGone
		}
		if tc != nil {
			tc.decorate(book, f, &fv, ed, rows)
		}
		shown := rows
		if !all && len(shown) > bookChaptersHead {
			shown = shown[:bookChaptersHead]
			fv.ShowAll = fmt.Sprintf(ebookShowAll, len(rows))
			fv.ShowAllHref = ebookTextHref(book.ID) + "?all=1"
		}
		n := 0
		for _, c := range rows[:len(shown)] {
			cv := pages.TextChapterView{Title: c.Title, FirstLine: c.FirstLine}
			if c.Front {
				cv.Front = ebookFront
			} else {
				n++
				cv.Num = fmt.Sprint(n)
			}
			fv.Chapters = append(fv.Chapters, cv)
		}
	case "pending":
		fv.Reading = fmt.Sprintf(ebookReading, fv.Name)
		fv.PollHref = ebookPollHref(f.JobID)
		if fv.PollHref == "" {
			fv.CheckLabel, fv.CheckHref = ebookReadCheck, ebookTextHref(book.ID)
		}
	case "refused":
		if f.RefusedReason == ebook.ReasonDRM {
			fv.Protected = &pages.TextProtectedView{
				Title: ebookDRMTitle, Body: fmt.Sprintf(ebookDRMBody, fv.Name), Advice: ebookDRMAdvice,
				Another: ebookDRMAnother, AnotherHref: ebookTextHref(book.ID) + "#ebook-add", WhyTitle: ebookDRMWhy,
				WhyBody: ebookDRMWhyBody, StillPlays: fmt.Sprintf(ebookStillPlays, book.Title),
			}
		}
		fv.Message = ebookRefusalText(f)
	default:
		fv.Message = ebookReadFailed
		if f.ErrorCode == "missing" {
			fv.Message = ebookGone
		}
	}
	return fv, nil
}

// ebookWords is the word count with thousands separators.
func ebookWords(n int) string {
	if n == 1 {
		return "1 word"
	}
	return thousands(n) + " words"
}

func ebookPollHref(jobID string) string {
	if jobID == "" {
		return ""
	}
	return "/hx/jobs/" + url.PathEscape(jobID)
}

// ebookRefusalText is the plain sentence for a refusal reason.
func ebookRefusalText(f ebook.File) string {
	switch f.RefusedReason {
	case ebook.ReasonTooLarge:
		return ebookTooLarge
	case ebook.ReasonDamaged:
		return ebookDamaged
	case ebook.ReasonNoText:
		if f.Ext == "pdf" {
			return ebookPDFImages
		}
		return ebookNoText
	default:
		return ebookUnsupported
	}
}

// ebookName is the file's name for the page: the name the owner uploaded, or the library file's name.
func ebookName(f ebook.File) string {
	if f.OriginalName != "" {
		return f.OriginalName
	}
	return filepath.Base(f.Path)
}

// ebookShortPath is the folder and file name of a library file ("Inbox/salt-and-ember.epub"): enough
// to recognise it, and no more of the server's folders.
func ebookShortPath(p string) string {
	return filepath.Base(filepath.Dir(p)) + "/" + filepath.Base(p)
}

// ebookEditBook loads the book of an Text edit route: 404 unless ebooks are on and the person may edit it.
func (s *server) ebookEditBook(w http.ResponseWriter, r *http.Request) (library.Book, bool) {
	if s.ebooks == nil {
		s.notFound(w, r)
		return library.Book{}, false
	}
	return s.loadBook(w, r, r.PathValue("id"), share.EditBook)
}

// postEbookUpload adds an ebook to the book. htmx gets the refreshed files and form (the new file
// polls until it is read); a plain form is sent back to the tab. A refusal answers with its status and
// the sentence in the form.
func (s *server) postEbookUpload(w http.ResponseWriter, r *http.Request) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return
	}
	user := currentUser(r)
	if r.MultipartForm == nil {
		r.Body = http.MaxBytesReader(w, r.Body, 201<<20)
		if err := r.ParseMultipartForm(1 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			s.ebookRefuse(w, r, book, http.StatusRequestEntityTooLarge, "This file is larger than 200 MB.")
			return
		}
	}
	body, hdr, err := r.FormFile("file")
	if err != nil || hdr.Filename == "" {
		s.ebookRefuse(w, r, book, http.StatusUnprocessableEntity, ebookNoFile)
		return
	}
	defer body.Close()
	f, err := s.ebooks.AddUpload(r.Context(), book, user.ID, hdr.Filename, body)
	var ue *ebook.UploadError
	if errors.As(err, &ue) {
		s.ebookRefuse(w, r, book, ue.Status, ue.Message)
		return
	}
	if err != nil {
		s.serverError(w, r, "add ebook", err)
		return
	}
	s.audit(r.Context(), user.ID, "book.text.add", book.ID, map[string]string{"file": f.ID, "ext": f.Ext})
	if r.Header.Get("HX-Request") != "true" {
		s.redirect(w, r, ebookTextHref(book.ID))
		return
	}
	v, err := s.textView(r, book, true, false, "")
	if err != nil {
		s.serverError(w, r, "text tab", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.TextBody(v))
}

// ebookRefuse answers a refused upload in plain words: the form fragment for htmx, the whole tab otherwise.
func (s *server) ebookRefuse(w http.ResponseWriter, r *http.Request, book library.Book, status int, msg string) {
	if r.Header.Get("HX-Request") == "true" {
		v, err := s.textView(r, book, true, false, msg)
		if err != nil {
			s.serverError(w, r, "text tab", err)
			return
		}
		s.fragment(w, r, status, pages.TextBody(v))
		return
	}
	r2 := r.Clone(context.WithValue(r.Context(), ebookUploadErrKey{}, msg))
	r2.Method = http.MethodGet
	s.renderBook(w, r2, book.ID, "text", status, nil)
}

// postEbookDismiss clears a refused or failed upload of the book.
func (s *server) postEbookDismiss(w http.ResponseWriter, r *http.Request) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return
	}
	err := s.ebooks.Dismiss(r.Context(), book.ID, r.PathValue("fileID"))
	if errors.Is(err, ebook.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "clear ebook", err)
		return
	}
	s.audit(r.Context(), currentUser(r).ID, "book.text.clear", book.ID, map[string]string{"file": r.PathValue("fileID")})
	if r.Header.Get("HX-Request") != "true" {
		s.redirect(w, r, ebookTextHref(book.ID))
		return
	}
	s.fragment(w, r, http.StatusOK, templ.NopComponent)
}

// getIngestJob is GET /hx/jobs/{id} for an ebook read: the file's row as it is now. The file must
// belong to a book the person may edit; the same row shows the edition when the read is done.
func (s *server) getIngestJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	var p ebook.IngestPayload
	if s.ebooks == nil || json.Unmarshal(j.Payload, &p) != nil || p.FileID == "" {
		s.notFound(w, r)
		return
	}
	f, ok, err := s.ebooks.Store().File(r.Context(), p.FileID)
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return
	}
	if !ok || f.BookID == "" {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, f.BookID, share.EditBook)
	if !ok {
		return
	}
	files, err := s.ebooks.Store().FilesForBook(r.Context(), book.ID)
	if err != nil {
		s.serverError(w, r, "ebook rows", err)
		return
	}
	tc, err := s.textContext(r, book, true, files)
	if err != nil {
		s.serverError(w, r, "ebook rows", err)
		return
	}
	fv, err := s.textFileView(r.Context(), book, f, true, false, s.csrf.TokenFrom(r.Context()), tc)
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.EbookFileRow(fv))
}
