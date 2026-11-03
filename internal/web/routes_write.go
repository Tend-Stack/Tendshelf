package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Writing details into the audio files (M4-9): the confirm sheet, the job's progress, and the
// "Looks good" and "Undo" choices. Every route needs share.EditBook on the book and a Writes
// service; a refusal, an unknown book and a job of another book all answer 404. Nothing here
// touches a library file: the sheet only reads (Writes.Preflight uses the database, Lstat and
// Statfs), and the changes are made by internal/mediajobs.

func (s *server) registerWrite(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/write", s.postWrite)
	mux.HandleFunc("POST /books/{id}/write/confirm", s.postWriteConfirm)
	mux.HandleFunc("POST /books/{id}/write/undo", s.postWriteUndo)
}

// writeBook loads the book of a write route: 404 unless writing is set up and the person may edit it.
func (s *server) writeBook(w http.ResponseWriter, r *http.Request, id string) (library.Book, bool) {
	if s.writes == nil {
		s.notFound(w, r)
		return library.Book{}, false
	}
	return s.loadBook(w, r, id, share.EditBook)
}

// answerBook ends an unsafe request that came from a sheet or the Details tab. A plain form is
// redirected to loc. An htmx request must not be (HX-Redirect loads the whole window and stops
// the audio that plays in the layout), so it gets the Details page as GET loc would render it and
// swaps out of it what it needs.
func (s *server) answerBook(w http.ResponseWriter, r *http.Request, bookID, loc string) {
	if r.Header.Get("HX-Request") != "true" {
		s.redirect(w, r, loc)
		return
	}
	u, err := url.Parse(loc)
	if err != nil {
		s.serverError(w, r, "answer book", err)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.RawQuery = u.RawQuery
	s.renderBook(w, r2, bookID, "details", http.StatusOK, nil)
}

// writeSheetHref is the write sheet's address, with the write job when there is one.
func writeSheetHref(bookID, jobID string) string {
	h := detailsTabHref(bookID) + "?sheet=write"
	if jobID != "" {
		h += "&job=" + url.QueryEscape(jobID)
	}
	return h
}

// writeRefusedKey carries a refusal that Preflight does not list (the book is busy) from a POST to
// the sheet it re-renders.
type writeRefusedKey struct{}

// writeJobFor loads a write job of this book. ok is false for any other job.
func (s *server) writeJobFor(r *http.Request, jobID, bookID string) (jobs.Job, bool, error) {
	j, err := s.jobs.Get(r.Context(), jobID)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.Job{}, false, nil
	}
	if err != nil {
		return jobs.Job{}, false, err
	}
	var p mediajobs.WritePayload
	if j.Kind != mediajobs.KindWriteDetails || json.Unmarshal(j.Payload, &p) != nil || p.BookID != bookID {
		return jobs.Job{}, false, nil
	}
	return j, true, nil
}

// writePending is the "Waiting for you" panel of a book whose files were written and not yet
// confirmed. ok is false when there is nothing to choose.
func (s *server) writePending(r *http.Request, bookID string) (pages.WritePendingView, bool, error) {
	rows, err := s.lib.BookFileWrites(r.Context(), bookID, library.WriteSwapped)
	if err != nil || len(rows) == 0 {
		return pages.WritePendingView{}, false, err
	}
	base := "/books/" + bookID + "/write"
	return pages.WritePendingView{
		CSRF: s.csrf.TokenFrom(r.Context()), Head: writePendingHead, Body: writePendingBody,
		ConfirmAction: base + "/confirm", ConfirmLabel: writeConfirmBtn,
		UndoAction: base + "/undo", UndoLabel: writeUndoBtn,
	}, true, nil
}

// writeSheetBase fills the words and addresses every state of the sheet shares.
func (s *server) writeSheetBase(r *http.Request, bookID, jobID string) pages.WriteSheetView {
	return pages.WriteSheetView{
		CSRF: s.csrf.TokenFrom(r.Context()), CloseHref: detailsTabHref(bookID), SheetHref: writeSheetHref(bookID, jobID),
		Title: writeSheetTitle, CloseLabel: writeSheetClose, Intro: writeIntro,
		FilesLabel: writeFilesLabel, DetailsLabel: writeDetailsLabel, RefusedLabel: writeRefusedLabel,
		Note: writeNote, Action: "/books/" + bookID + "/write", Button: writeButton, CheckAgain: writeCheckAgain,
		Styles: s.sheetStyles(pages.DetailsStyles...),
	}
}

// writeJobView is the sheet for a write job: waiting with its progress, done with the two choices,
// or failed in plain words. ok is false when the job is not this book's.
func (s *server) writeJobView(r *http.Request, bookID, jobID string) (pages.WriteSheetView, bool, error) {
	v := s.writeSheetBase(r, bookID, jobID)
	j, ok, err := s.writeJobFor(r, jobID, bookID)
	if err != nil || !ok {
		return v, false, err
	}
	v.PollHref = "/hx/jobs/" + url.PathEscape(j.ID)
	switch j.State {
	case jobs.StateDone:
		v.State, v.Message = "done", writeJobDone
		pending, has, err := s.writePending(r, bookID)
		if err != nil {
			return v, false, err
		}
		v.Pending = pending
		if !has {
			v.Message = writeSettled
		}
	case jobs.StateFailed, jobs.StateCancelled:
		v.State, v.Message = "failed", j.ErrorText
		if v.Message == "" {
			v.Message = writeFailed
		}
	default:
		v.State, v.Message = "wait", writeWaiting
		var p struct{ File, Of int }
		if json.Unmarshal(j.Progress, &p) == nil && p.File > 0 && p.Of > 0 {
			v.Message = fmt.Sprintf(writeProgress, p.File, p.Of)
		}
	}
	return v, true, nil
}

// writeSheetView builds the sheet for a book that is already known to be editable: the preview
// with its confirm form, or the job named by ?job=. ok is false when jobID is not this book's.
func (s *server) writeSheetView(r *http.Request, d library.BookDetail, jobID string) (pages.WriteSheetView, bool, error) {
	if jobID != "" {
		return s.writeJobView(r, d.Book.ID, jobID)
	}
	ctx := r.Context()
	v := s.writeSheetBase(r, d.Book.ID, "")
	files, err := s.lib.AudioFiles(ctx, d.Book.ID)
	if err != nil {
		return v, false, err
	}
	for _, f := range files {
		v.Files = append(v.Files, filepath.Base(f.Path))
	}
	v.Details = writeDetails(d, len(files))
	refs, err := s.writes.Preflight(ctx, d.Book.ID)
	if err != nil {
		return v, false, err
	}
	for _, ref := range refs {
		row := pages.WriteRefusalRow{Text: ref.Text}
		if ref.AudioFileID != "" {
			row.File = filepath.Base(ref.Path)
		}
		v.Refusals = append(v.Refusals, row)
	}
	if text, ok := ctx.Value(writeRefusedKey{}).(string); ok && text != "" {
		v.Refusals = append(v.Refusals, pages.WriteRefusalRow{Text: text})
	}
	return v, true, nil
}

// writeDetails lists the details that will go into the files, the way mediajobs maps them. The
// chapters are written into single-file books only.
func writeDetails(d library.BookDetail, files int) []pages.WriteDetail {
	var out []pages.WriteDetail
	add := func(label, value string) {
		if value != "" {
			out = append(out, pages.WriteDetail{Label: label, Value: value})
		}
	}
	names := func(ps []library.PersonRef) string {
		var n []string
		for _, p := range ps {
			n = append(n, p.Name)
		}
		return strings.Join(n, ", ")
	}
	add(writeLabelTitle, d.Book.Title)
	add(writeLabelAuthors, names(d.Card.Authors))
	add(writeLabelNarrators, names(d.Card.Narrators))
	if len(d.Series) > 0 {
		series := d.Series[0].Name
		if d.Series[0].Sequence != "" {
			series += " #" + d.Series[0].Sequence
		}
		add(writeLabelSeries, series)
	}
	if d.Book.PublishedYear > 0 {
		add(writeLabelYear, strconv.Itoa(d.Book.PublishedYear))
	}
	add(writeLabelGenres, strings.Join(d.Genres, ", "))
	if files == 1 && len(d.Chapters) > 0 {
		add(writeLabelChapters, strconv.Itoa(len(d.Chapters)))
	}
	return out
}

// writeTabView is the write controls of the Details tab for a book the person may edit.
func (s *server) writeTabView(r *http.Request, bookID string) (pages.WriteTabView, error) {
	var v pages.WriteTabView
	switch r.URL.Query().Get("written") {
	case "kept":
		v.Note = writeKept
	case "undone":
		v.Note = writeUndone
	}
	pending, has, err := s.writePending(r, bookID)
	if err != nil {
		return v, err
	}
	if has {
		v.Pending = pending
	} else {
		v.OpenHref, v.OpenLabel, v.OpenCaption = writeSheetHref(bookID, ""), writeOpenLink, writeOpenCaption
	}
	rows, err := s.lib.BookFileWrites(r.Context(), bookID)
	if err != nil || len(rows) == 0 {
		return v, err
	}
	// The notice stays while the book's latest write ended in a file that needs a look.
	latest := rows[len(rows)-1].JobID
	var folder string
	attention := false
	for _, row := range rows {
		if row.JobID == latest && row.State == library.WriteFailed && row.ErrorCode == mediajobs.WriteAttention {
			attention, folder = true, filepath.Dir(row.OriginalPath)
		}
	}
	if attention {
		v.Attention = &pages.WriteAttentionView{Text: writeAttention}
		if s.canManage(r) {
			v.Attention.Folder = fmt.Sprintf(writeAttentionDir, folder)
		}
	}
	return v, nil
}

// postWrite starts the write. A refusal answers 422 with the sheet showing it; success goes to the
// sheet that follows the job.
func (s *server) postWrite(w http.ResponseWriter, r *http.Request) {
	book, ok := s.writeBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	user := currentUser(r).ID
	jobID, err := s.writes.Start(r.Context(), book.ID, user)
	var ref *mediajobs.WriteRefusal
	if errors.As(err, &ref) {
		ctx := context.WithValue(r.Context(), writeRefusedKey{}, ref.Text)
		r2 := r.Clone(ctx)
		r2.URL.RawQuery = "sheet=write"
		s.renderBook(w, r2, book.ID, "details", http.StatusUnprocessableEntity, nil)
		return
	}
	if err != nil {
		s.serverError(w, r, "start write", err)
		return
	}
	s.audit(r.Context(), user, "book.write.start", book.ID, map[string]string{"job": jobID})
	s.answerBook(w, r, book.ID, writeSheetHref(book.ID, jobID))
}

// writeChoice runs Looks good or Undo and goes back to the Details tab with the result.
func (s *server) writeChoice(w http.ResponseWriter, r *http.Request, done string, run func(context.Context, string, string) (int, error)) {
	book, ok := s.writeBook(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	_, err := run(r.Context(), book.ID, currentUser(r).ID)
	var ref *mediajobs.WriteRefusal
	switch {
	case err == nil:
		s.answerBook(w, r, book.ID, detailsTabHref(book.ID)+"?written="+done)
	case errors.Is(err, mediajobs.ErrNeedsAttention):
		// The Details tab shows the notice for the file that changed.
		s.answerBook(w, r, book.ID, detailsTabHref(book.ID))
	case errors.As(err, &ref):
		s.detailsRefuse(w, r, http.StatusUnprocessableEntity, ref.Text)
	default:
		s.serverError(w, r, "write choice", err)
	}
}

// postWriteConfirm is "Looks good": the backups are removed.
func (s *server) postWriteConfirm(w http.ResponseWriter, r *http.Request) {
	s.writeChoice(w, r, "kept", s.writes.Confirm)
}

// postWriteUndo puts the original files back.
func (s *server) postWriteUndo(w http.ResponseWriter, r *http.Request) {
	s.writeChoice(w, r, "undone", s.writes.Undo)
}

// getWriteJob is GET /hx/jobs/{id} for a write: the sheet's body again, waiting or finished. The
// job names its book; the person must be allowed to edit it.
func (s *server) getWriteJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	var p mediajobs.WritePayload
	if json.Unmarshal(j.Payload, &p) != nil || p.BookID == "" {
		s.notFound(w, r)
		return
	}
	book, ok := s.writeBook(w, r, p.BookID)
	if !ok {
		return
	}
	v, ok, err := s.writeJobView(r, book.ID, j.ID)
	if err != nil {
		s.serverError(w, r, "write sheet", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.WriteBody(v))
}
