package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The cover picker (M4-8): the "Change cover" sheet, choosing one of a book's covers, and adding a
// picture of one's own. Everything needs share.EditBook on the book; a refusal, an unknown book and
// a cover that is not the book's all answer 404. The upload is a plain multipart form.

const (
	// coverUploadMax is the largest picture accepted; the form around it may add 64 KiB.
	coverUploadMax  = 5 << 20
	coverUploadSlop = 64 << 10
)

func (s *server) registerCoverPick(mux *http.ServeMux) {
	mux.HandleFunc("GET /covers/{bookID}/choices/{coverID}", s.getCoverChoice)
	mux.HandleFunc("POST /books/{id}/cover", s.postCoverChoose)
	mux.HandleFunc("POST /books/{id}/cover/upload", s.postCoverUpload)
}

// coverBook loads the book of a cover route: 404 unless covers are set up and the person may edit it.
func (s *server) coverBook(w http.ResponseWriter, r *http.Request, id string) (library.Book, bool) {
	if s.covers == nil {
		s.notFound(w, r)
		return library.Book{}, false
	}
	return s.loadBook(w, r, id, share.EditBook)
}

// coverSheetHref is the cover sheet's address, with the adoption job when there is one.
func coverSheetHref(bookID, jobID string) string {
	h := detailsTabHref(bookID) + "?sheet=cover"
	if jobID != "" {
		h += "&job=" + url.QueryEscape(jobID)
	}
	return h
}

func coverChoiceSrc(bookID, coverID string, w int) string {
	return "/covers/" + bookID + "/choices/" + coverID + "?w=" + strconv.Itoa(w)
}

func coverSourceLabel(source string) string {
	switch source {
	case library.CoverEmbedded:
		return coverFromFile
	case library.CoverFolder:
		return coverFromFolder
	case library.CoverUpload:
		return coverFromUpload
	case library.CoverOnline:
		return coverFromOnline
	}
	return ""
}

// adoptJobFor loads a cover adoption job of this book. ok is false for any other job.
func (s *server) adoptJobFor(r *http.Request, jobID, bookID string) (jobs.Job, bool, error) {
	j, err := s.jobs.Get(r.Context(), jobID)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, false, nil
	}
	if err != nil {
		return jobs.Job{}, false, err
	}
	var p mediajobs.AdoptPayload
	if j.Kind != mediajobs.KindCoverAdopt || json.Unmarshal(j.Payload, &p) != nil || p.BookID != bookID {
		return jobs.Job{}, false, nil
	}
	return j, true, nil
}

// coverSheetView builds the cover sheet for a book that is already known to be editable. ok is false
// when jobID names a job that is not this book's adoption.
func (s *server) coverSheetView(r *http.Request, book library.Book, jobID string) (pages.CoverSheetView, bool, error) {
	id := book.ID
	v := pages.CoverSheetView{
		CSRF: s.csrf.TokenFrom(r.Context()), CloseHref: detailsTabHref(id), SheetHref: coverSheetHref(id, jobID),
		Title: coverSheetTitle, CloseLabel: coverSheetClose, Intro: coverPickIntro, NowLabel: coverNow, NoCover: coverNoCover,
		ChoicesLabel: coverChoicesName, CurrentTag: coverCurrentTag, Use: coverUse,
		ChooseAction: "/books/" + id + "/cover", UploadAction: "/books/" + id + "/cover/upload",
		UploadTitle: coverUploadTitle, UploadLabel: coverUploadLabel, UploadHint: coverUploadHint, UploadGo: coverUploadGo,
		CheckAgain: coverCheckAgain, Styles: s.sheetStyles(pages.DetailsStyles...),
	}
	choices, err := s.covers.Choices(r.Context(), id)
	if err != nil {
		return v, false, err
	}
	for _, c := range choices {
		cur := c.ID == book.CoverID
		v.Choices = append(v.Choices, pages.CoverChoice{
			ID: c.ID, Src: coverChoiceSrc(id, c.ID, 160), Label: coverSourceLabel(c.Source), Current: cur})
		if cur {
			v.NowSrc = coverChoiceSrc(id, c.ID, 320)
		}
	}
	if jobID == "" {
		return v, true, nil
	}
	j, ok, err := s.adoptJobFor(r, jobID, id)
	if err != nil || !ok {
		return v, false, err
	}
	v.PollHref = "/hx/jobs/" + url.PathEscape(j.ID)
	switch j.State {
	case jobs.StateDone:
		v.State, v.Message = "done", coverDone
	case jobs.StateFailed, jobs.StateCancelled:
		v.State, v.Message = "failed", j.ErrorText
		if v.Message == "" {
			v.Message = coverFailed
		}
	default:
		v.State, v.Message = "wait", coverWaiting
	}
	return v, true, nil
}

// getCoverAdoptJob is GET /hx/jobs/{id} for a cover adoption: the sheet's body again, waiting or
// finished. The job names its book; the person must be allowed to edit it.
func (s *server) getCoverAdoptJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	var p mediajobs.AdoptPayload
	if json.Unmarshal(j.Payload, &p) != nil || p.BookID == "" {
		s.notFound(w, r)
		return
	}
	book, ok := s.coverBook(w, r, p.BookID)
	if !ok {
		return
	}
	v, ok, err := s.coverSheetView(r, book, j.ID)
	if err != nil {
		s.serverError(w, r, "cover sheet", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.CoverBody(v))
}

// getCoverChoice serves the thumbnail of one of the book's own covers to a person who may edit the
// book. A cover of another book is a 404.
func (s *server) getCoverChoice(w http.ResponseWriter, r *http.Request) {
	book, ok := s.coverBook(w, r, r.PathValue("bookID"))
	if !ok {
		return
	}
	width := 0
	switch r.URL.Query().Get("w") {
	case "":
	case "160":
		width = 160
	case "320":
		width = 320
	default:
		s.plainError(w, r, http.StatusBadRequest, "Bad width.")
		return
	}
	c, err := s.lib.Cover(r.Context(), r.PathValue("coverID"))
	if errors.Is(err, library.ErrNotFound) || (err == nil && c.BookID != book.ID) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "load cover", err)
		return
	}
	s.serveCoverFile(w, r, c.ID, width, "private, no-cache")
}

// postCoverChoose makes one of the book's covers its current cover.
func (s *server) postCoverChoose(w http.ResponseWriter, r *http.Request) {
	book, ok := s.coverBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	coverID := r.PostFormValue("cover_id")
	err := s.covers.Choose(r.Context(), book.ID, coverID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "choose cover", err)
		return
	}
	s.audit(r.Context(), currentUser(r).ID, "book.cover.choose", book.ID, map[string]string{"cover_id": coverID})
	s.answerBook(w, r, book.ID, detailsTabHref(book.ID))
}

// postCoverUpload keeps an uploaded picture and queues the job that stores it. The CSRF check has
// already read the form (up to csrfUploadLimit), so the part is read from r.MultipartForm.
func (s *server) postCoverUpload(w http.ResponseWriter, r *http.Request) {
	book, ok := s.coverBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if r.MultipartForm == nil {
		r.Body = http.MaxBytesReader(w, r.Body, coverUploadMax+coverUploadSlop)
		err := r.ParseMultipartForm(coverUploadMax + coverUploadSlop)
		if errors.Is(err, http.ErrNotMultipart) {
			s.detailsRefuse(w, r, http.StatusUnprocessableEntity, coverNoFile)
			return
		}
		if err != nil {
			s.detailsRefuse(w, r, http.StatusRequestEntityTooLarge, coverTooLarge)
			return
		}
	}
	files := r.MultipartForm.File["picture"]
	if len(files) == 0 {
		s.detailsRefuse(w, r, http.StatusUnprocessableEntity, coverNoFile)
		return
	}
	if files[0].Size > coverUploadMax {
		s.detailsRefuse(w, r, http.StatusRequestEntityTooLarge, coverTooLarge)
		return
	}
	f, err := files[0].Open()
	if err != nil {
		s.serverError(w, r, "open upload", err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, coverUploadMax+1))
	if err != nil {
		s.serverError(w, r, "read upload", err)
		return
	}
	name, err := s.covers.Stage(data)
	var me *media.Error
	switch {
	case errors.As(err, &me) && me.Code == mediajobs.CodeTooLarge:
		s.detailsRefuse(w, r, http.StatusRequestEntityTooLarge, coverTooLarge)
		return
	case errors.As(err, &me):
		s.detailsRefuse(w, r, http.StatusUnprocessableEntity, coverNotPicture)
		return
	case err != nil:
		s.serverError(w, r, "stage cover", err)
		return
	}
	user := currentUser(r).ID
	payload := mediajobs.AdoptPayload{BookID: book.ID, Source: library.CoverUpload, Staged: name}
	jobID, err := s.jobs.Enqueue(r.Context(), mediajobs.KindCoverAdopt, payload, jobs.EnqueueOptions{CreatedBy: user})
	if err != nil {
		s.serverError(w, r, "adopt cover", err)
		return
	}
	s.audit(r.Context(), user, "book.cover.upload", book.ID, map[string]string{"job": jobID})
	s.answerBook(w, r, book.ID, coverSheetHref(book.ID, jobID))
}
