package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Preparing read-along on the Text tab (M5 R3 P5): start the alignment of a paired ebook with its audiobook,
// watch it, cancel it, and settle a held one. Every route needs share.EditBook on the book; a file that is
// not paired to that book answers 404 like a missing one. The work is the align.book job; the card on the
// tab polls /hx/jobs/{id}.

func (s *server) registerEbookAlign(mux *http.ServeMux) {
	mux.HandleFunc("POST /books/{id}/text/{fileID}/align", s.postTextAlign)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/align/cancel", s.postTextAlignCancel)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/align/resume", s.postTextAlignResume)
	mux.HandleFunc("POST /books/{id}/text/{fileID}/align/discard", s.postTextAlignDiscard)
}

// alignLangNames are the languages with a pinned model.
var alignLangNames = map[string]string{"en": runtimesEnglish, "es": runtimesSpanish}

// pairLabel is the pairing button's label: the deck's "Pair and prepare read-along" when a checked runtime
// exists for the edition's language, else "Pair with this audiobook".
func (s *server) pairLabel(ctx context.Context, book library.Book, ed ebook.Edition) string {
	if s.align != nil {
		if ok, err := s.align.Available(ctx, ed, book.ID); err == nil && ok {
			return ebookPairAndPrepare
		}
	}
	return ebookPairGo
}

// prepareAfterPair starts the alignment of a file that was just paired, when the button said it would. A
// refusal here (no runtime any more, audio missing) leaves the file paired and is only logged: the Text tab
// shows "Prepare read-along" next.
func (s *server) prepareAfterPair(r *http.Request, book library.Book, fileID string) {
	if s.align == nil {
		return
	}
	ctx := r.Context()
	ed, ok, err := s.ebooks.Store().CurrentEdition(ctx, fileID)
	if err != nil || !ok {
		return
	}
	if avail, err := s.align.Available(ctx, ed, book.ID); err != nil || !avail {
		return
	}
	if _, err := s.align.Start(ctx, ed.ID, book.ID, currentUser(r).ID); err != nil {
		slog.Warn("align: could not start after pairing", "book", book.ID, "err", err)
	}
}

// alignTarget loads the book and the paired file of an alignment route: 404 unless alignment is on, the
// person may edit the book, and the file is a ready edition on that book.
func (s *server) alignTarget(w http.ResponseWriter, r *http.Request) (library.Book, ebook.File, ebook.Edition, bool) {
	book, ok := s.ebookEditBook(w, r)
	if !ok {
		return library.Book{}, ebook.File{}, ebook.Edition{}, false
	}
	if s.align == nil {
		s.notFound(w, r)
		return library.Book{}, ebook.File{}, ebook.Edition{}, false
	}
	f, found, err := s.ebooks.Store().File(r.Context(), r.PathValue("fileID"))
	if err != nil {
		s.serverError(w, r, "ebook row", err)
		return library.Book{}, ebook.File{}, ebook.Edition{}, false
	}
	if !found || f.BookID != book.ID || f.State != "ready" {
		s.notFound(w, r)
		return library.Book{}, ebook.File{}, ebook.Edition{}, false
	}
	ed, found, err := s.ebooks.Store().CurrentEdition(r.Context(), f.ID)
	if err != nil {
		s.serverError(w, r, "ebook edition", err)
		return library.Book{}, ebook.File{}, ebook.Edition{}, false
	}
	if !found {
		s.notFound(w, r)
		return library.Book{}, ebook.File{}, ebook.Edition{}, false
	}
	return book, f, ed, true
}

// alignFail answers a refused alignment action.
func (s *server) alignFail(w http.ResponseWriter, r *http.Request, book library.Book, ed ebook.Edition, err error) {
	switch {
	case errors.Is(err, align.ErrEditionNotReady), errors.Is(err, align.ErrNotFound):
		s.notFound(w, r)
	case errors.Is(err, align.ErrRuntimeMissing):
		s.ebookTabRefuse(w, r, book, http.StatusConflict, s.noRuntimeLine(r.Context(), book, ed))
	case errors.Is(err, align.ErrAudioUnavailable):
		s.ebookTabRefuse(w, r, book, http.StatusConflict, alignNoAudio)
	case errors.Is(err, align.ErrNotHeld):
		s.ebookTabRefuse(w, r, book, http.StatusConflict, alignFinished)
	default:
		s.serverError(w, r, "read-along", err)
	}
}

// noRuntimeLine is the deck's "Read-along for Spanish needs word timing for Spanish. An admin can set it up."
func (s *server) noRuntimeLine(ctx context.Context, book library.Book, ed ebook.Edition) string {
	if name, ok := alignLangNames[s.align.Language(ctx, ed, book.ID)]; ok {
		return fmt.Sprintf(alignNoRuntime, name, name)
	}
	return alignNoRuntimeAny
}

// postTextAlign starts preparing read-along for a paired ebook.
func (s *server) postTextAlign(w http.ResponseWriter, r *http.Request) {
	book, _, ed, ok := s.alignTarget(w, r)
	if !ok {
		return
	}
	_, err := s.align.Start(r.Context(), ed.ID, book.ID, currentUser(r).ID)
	if err != nil {
		s.alignFail(w, r, book, ed, err)
		return
	}
	s.answerPage(w, r, ebookTextHref(book.ID))
}

// postTextAlignCancel stops a running alignment.
func (s *server) postTextAlignCancel(w http.ResponseWriter, r *http.Request) {
	s.alignEnd(w, r, false)
}

// postTextAlignDiscard throws away a held alignment.
func (s *server) postTextAlignDiscard(w http.ResponseWriter, r *http.Request) {
	s.alignEnd(w, r, true)
}

// alignEnd cancels or discards the edition's active alignment. One that already ended is "already finished".
func (s *server) alignEnd(w http.ResponseWriter, r *http.Request, discard bool) {
	book, _, ed, ok := s.alignTarget(w, r)
	if !ok {
		return
	}
	a, found, err := s.align.Store().Active(r.Context(), ed.ID)
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	if !found {
		s.alignFail(w, r, book, ed, align.ErrNotHeld)
		return
	}
	if discard {
		err = s.align.Discard(r.Context(), a.ID)
	} else {
		err = s.align.Cancel(r.Context(), a.ID)
	}
	if err != nil {
		s.alignFail(w, r, book, ed, err)
		return
	}
	s.answerPage(w, r, ebookTextHref(book.ID))
}

// postTextAlignResume lets a held alignment go on.
func (s *server) postTextAlignResume(w http.ResponseWriter, r *http.Request) {
	book, _, ed, ok := s.alignTarget(w, r)
	if !ok {
		return
	}
	a, found, err := s.align.Store().Active(r.Context(), ed.ID)
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	if !found {
		s.alignFail(w, r, book, ed, align.ErrNotHeld)
		return
	}
	if err := s.align.Resume(r.Context(), a.ID); err != nil {
		s.alignFail(w, r, book, ed, err)
		return
	}
	s.answerPage(w, r, ebookTextHref(book.ID))
}

// alignView builds the read-along card of a paired edition, or nil when alignment is off. rows are the
// edition's chapters (their titles name the chapter being matched).
func (s *server) alignView(ctx context.Context, book library.Book, f ebook.File, ed ebook.Edition, rows []ebook.ChapterRow, tc *textCtx) (*pages.TextAlignView, error) {
	if s.align == nil {
		return nil, nil
	}
	st, has, err := s.align.Status(ctx, ed.ID)
	if err != nil {
		return nil, err
	}
	v := &pages.TextAlignView{ID: "align-" + f.ID, State: "idle"}
	prepare := func(label string) {
		v.Actions = append(v.Actions, *tc.action(book, f, "align", label, "primary"))
	}
	if !has {
		prepare(alignPrepare)
		return v, nil
	}
	switch st.State {
	case align.StateQueued, align.StateEmitting, align.StateAligning:
		v.State, v.Title = "active", alignTitle
		if st.Job != nil {
			v.PollHref = "/hx/jobs/" + url.PathEscape(st.Job.ID)
		} else if st.JobID != "" {
			v.PollHref = "/hx/jobs/" + url.PathEscape(st.JobID)
		}
		alignProgress(v, st, rows)
		v.Notes = append(v.Notes, alignRunsHere)
		v.Actions = append(v.Actions, *tc.action(book, f, "align/cancel", alignCancel, "secondary"))
	case align.StateHeld:
		v.State, v.Title = "held", alignHeldTitle
		switch st.ErrorCode {
		case align.HoldRuntimeChanged:
			v.Notes = append(v.Notes, alignHeldRuntime)
		case align.HoldAudioChanged:
			v.Notes = append(v.Notes, alignHeldAudio)
		default:
			v.Notes = append(v.Notes, alignHeldWorker)
		}
		v.Actions = append(v.Actions, *tc.action(book, f, "align/resume", alignResume, "primary"),
			*tc.action(book, f, "align/discard", alignDiscard, "quiet"))
	case align.StateDone, align.StatePartial:
		v.State = "done"
		v.Title = fmt.Sprintf(alignDone, book.Title)
		if st.State == align.StatePartial {
			v.State = "partial"
			v.Title = fmt.Sprintf(alignPartial, int(st.Coverage*100+0.5))
			for _, l := range st.Labels {
				switch l {
				case align.LabelAbridged:
					v.Notes = append(v.Notes, alignAbridged)
				case align.LabelDifferentEdition:
					v.Notes = append(v.Notes, alignDifferent)
				}
			}
		} else {
			for _, l := range st.Labels {
				if l == align.LabelDifferentEdition {
					v.Notes = append(v.Notes, alignDifferent)
				}
			}
		}
		if st.Stale {
			v.Notes = append(v.Notes, alignStale)
			prepare(alignPrepareAgain)
		}
	case align.StateFailed:
		v.State, v.Title = "failed", alignTitle
		switch st.ErrorCode {
		case align.CodeNoMatch:
			v.Notes = append(v.Notes, alignNoMatch, alignNoMatchHint)
		case "runtime_missing":
			v.Notes = append(v.Notes, s.noRuntimeLine(ctx, book, ed))
		case "audio_unavailable":
			v.Notes = append(v.Notes, alignNoAudio)
		default:
			v.Notes = append(v.Notes, alignFailedOther)
		}
		prepare(alignTryAgain)
	default: // cancelled
		prepare(alignPrepare)
	}
	return v, nil
}

// alignProgress writes the stage line, the count and the chapter bar of a running alignment.
func alignProgress(v *pages.TextAlignView, st align.Status, rows []ebook.ChapterRow) {
	var p struct {
		Stage         string `json:"stage"`
		ChaptersDone  int    `json:"chaptersDone"`
		ChaptersTotal int    `json:"chaptersTotal"`
		LeftMs        *int64 `json:"leftMs"`
	}
	if st.Job != nil {
		_ = json.Unmarshal(st.Job.Progress, &p)
	}
	switch {
	case st.State == align.StateQueued && p.Stage == "":
		v.Line = alignWaiting
	case st.State == align.StateAligning && p.ChaptersDone < len(rows) && p.ChaptersDone >= 0 && rows[p.ChaptersDone].Title != "":
		v.Line = fmt.Sprintf(alignMatching, p.ChaptersDone+1, rows[p.ChaptersDone].Title)
	case st.State == align.StateAligning:
		v.Line = fmt.Sprintf(alignMatchingNoName, p.ChaptersDone+1)
	default:
		v.Line = alignListening
	}
	if p.ChaptersTotal > 0 {
		v.Count = fmt.Sprintf(alignChapterOf, min(p.ChaptersDone, p.ChaptersTotal), p.ChaptersTotal)
		for i := 0; i < p.ChaptersTotal; i++ {
			switch {
			case i < p.ChaptersDone:
				v.Segments = append(v.Segments, "done")
			case i == p.ChaptersDone:
				v.Segments = append(v.Segments, "now")
			default:
				v.Segments = append(v.Segments, "")
			}
		}
	}
	if p.LeftMs != nil {
		if mins := (*p.LeftMs + 59_999) / 60_000; mins >= 1 {
			v.Left = fmt.Sprintf(alignLeft, mins)
		} else {
			v.Left = alignLeftShort
		}
	}
}

// getAlignJob is the card of the alignment a job belongs to, for the Text tab's polling. The person must be
// able to edit the book; anyone else gets "no such job".
func (s *server) getAlignJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	if s.align == nil || s.ebooks == nil {
		s.notFound(w, r)
		return
	}
	a, ok, err := s.align.Store().ByJob(r.Context(), j.ID)
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, a.BookID, share.EditBook)
	if !ok {
		return
	}
	ed, ok, err := s.ebooks.Store().Edition(r.Context(), a.EditionID)
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	if !ok {
		s.notFound(w, r)
		return
	}
	f, ok, err := s.ebooks.Store().File(r.Context(), ed.EbookFileID)
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	if !ok || f.BookID != book.ID {
		s.notFound(w, r)
		return
	}
	rows, err := s.ebooks.Store().Chapters(r.Context(), ed.ID)
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	v, err := s.alignView(r.Context(), book, f, ed, rows, &textCtx{csrf: s.csrf.TokenFrom(r.Context())})
	if err != nil {
		s.serverError(w, r, "read-along", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.TextAlign(*v))
}
