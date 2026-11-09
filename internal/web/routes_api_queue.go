package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The JSON API's Up next (queue): the person's own ordered list of books to play after the current
// one. Every read goes through the scope SQL (library.Queue), so a book the person cannot open never
// shows; adding needs ListenBook on the book.

const (
	apiQueueBody = 8 << 10 // the body of a queue write: a book id and a word
	endedMaxLen  = 64      // the longest "ended" book id the autoplay endpoints accept
)

func (s *server) registerAPIQueue(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/queue", s.getAPIQueue)
	mux.HandleFunc("POST /api/v1/queue", s.postAPIQueue)
	mux.HandleFunc("GET /api/v1/queue/next", s.getAPIQueueNext)
	mux.HandleFunc("POST /api/v1/queue/next", s.postAPIQueueNext)
	mux.HandleFunc("POST /api/v1/queue/{bookID}/move", s.postAPIQueueMove)
	mux.HandleFunc("DELETE /api/v1/queue/{bookID}", s.deleteAPIQueue)
}

type apiQueueItem struct {
	Book    apiBookCard `json:"book"`
	AddedAt int64       `json:"addedAt"`
}

// writeAPIQueue answers 200 with the person's visible queue. next is always null: the list is
// short and whole, so there is nothing to page.
func (s *server) writeAPIQueue(w http.ResponseWriter, r *http.Request) {
	list, err := s.lib.Queue(r.Context(), s.scope(r), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, "api queue", err)
		return
	}
	items := make([]apiQueueItem, 0, len(list))
	for _, e := range list {
		items = append(items, apiQueueItem{Book: apiCardOf(e.Book), AddedAt: e.AddedAt})
	}
	api.Write(w, http.StatusOK, map[string]any{"items": items, "next": nil, "total": len(items)})
}

func (s *server) getAPIQueue(w http.ResponseWriter, r *http.Request) {
	s.writeAPIQueue(w, r)
}

func (s *server) postAPIQueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BookID string `json:"bookId"`
		Where  string `json:"where"`
	}
	// The book id lives in the body, so the body is read first, then access to the book is checked
	// before anything about the book is said.
	if !apiJSONBody(w, r, apiQueueBody, &body) {
		return
	}
	if body.BookID == "" {
		apiBadField(w, "bookId", queueErrBookID)
		return
	}
	front := false
	switch body.Where {
	case "", "end":
	case "next":
		front = true
	default:
		apiBadField(w, "where", queueErrWhere)
		return
	}
	b, ok := s.loadBook(w, r, body.BookID, share.ListenBook)
	if !ok {
		return
	}
	if !s.queueable(r.Context(), b) {
		api.ErrorField(w, http.StatusConflict, api.CodeConflict, "bookId", queueErrUnplayable)
		return
	}
	_, err := s.lib.QueueAdd(r.Context(), currentUser(r).ID, b.ID, front)
	switch {
	case errors.Is(err, library.ErrQueueFull):
		api.ErrorField(w, http.StatusConflict, api.CodeConflict, "bookId", queueErrFull)
	case errors.Is(err, library.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "api queue add", err)
	default:
		s.writeAPIQueue(w, r)
	}
}

func (s *server) postAPIQueueMove(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.loadBook(w, r, r.PathValue("bookID"), share.ViewBook); !ok {
		return
	}
	var body struct {
		Dir string `json:"dir"`
	}
	if !apiJSONBody(w, r, apiQueueBody, &body) {
		return
	}
	delta := 0
	switch body.Dir {
	case "up":
		delta = -1
	case "down":
		delta = 1
	default:
		apiBadField(w, "dir", queueErrDir)
		return
	}
	err := s.lib.QueueMove(r.Context(), s.scope(r), currentUser(r).ID, r.PathValue("bookID"), delta)
	switch {
	case errors.Is(err, library.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "api queue move", err)
	default:
		s.writeAPIQueue(w, r)
	}
}

func (s *server) deleteAPIQueue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("bookID")
	if _, ok := s.loadBook(w, r, id, share.ViewBook); !ok {
		return
	}
	err := s.lib.QueueRemove(r.Context(), currentUser(r).ID, id)
	switch {
	case errors.Is(err, library.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "api queue remove", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) postAPIQueueNext(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Ended string `json:"ended"`
		// Series opts in to the book after Ended in its series when the queue is empty (decision 0026).
		// Without it an empty queue stays a 204, as it was for older apps.
		Series bool `json:"series"`
	}
	// An empty body is fine here: "ended" is optional. Anything else must be JSON.
	if r.ContentLength != 0 {
		if !apiJSONBody(w, r, apiQueueBody, &body) {
			return
		}
	}
	if len(body.Ended) > endedMaxLen {
		apiBadField(w, "ended", queueErrEnded)
		return
	}
	var card apiBookCard
	found, err := s.popQueue(r.Context(), currentUser(r), s.scope(r), body.Ended, func(b library.Book) (bool, error) {
		d, err := s.lib.BookDetail(r.Context(), s.scope(r), b.ID)
		if errors.Is(err, library.ErrNotFound) {
			return false, nil
		}
		card = apiCardOf(d.Card)
		return err == nil, err
	})
	switch {
	case err != nil:
		s.serverError(w, r, "api queue next", err)
	case found:
		api.Write(w, http.StatusOK, map[string]any{"book": card, "source": "queue"})
	default:
		s.writeSeriesNext(w, r, body.Ended, body.Series)
	}
}

// nextPlayable reports whether a book may be the next one for the person: they may listen to it, it is not
// refused or missing, and it is not a text edition. Autoplay, its series fallback and the peek share it.
func (s *server) nextPlayable(ctx context.Context, u users.User, b library.Book) bool {
	return s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) &&
		b.RefusedReason == "" && b.MissingSince == 0 && b.Kind != library.BookText
}

// getAPIQueueNext is the read-only peek behind the app's automatic download (decision 0035): the book that
// POST /api/v1/queue/next would give after ended, without taking it off the queue or writing anything. The
// series fallback is always on here; it still honours the person's own setting.
func (s *server) getAPIQueueNext(w http.ResponseWriter, r *http.Request) {
	ended := r.URL.Query().Get("ended")
	if len(ended) > endedMaxLen {
		apiBadField(w, "ended", queueErrEnded)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	ctx := r.Context()
	u := currentUser(r)
	entries, err := s.lib.Queue(ctx, s.scope(r), u.ID)
	if err != nil {
		s.serverError(w, r, "api queue peek", err)
		return
	}
	for _, e := range entries {
		if ended != "" && e.Book.ID == ended {
			continue
		}
		b, err := s.lib.Book(ctx, e.Book.ID)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			s.serverError(w, r, "api queue peek", err)
			return
		}
		if !s.nextPlayable(ctx, u, b) {
			continue
		}
		d, err := s.lib.BookDetail(ctx, s.scope(r), b.ID)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			s.serverError(w, r, "api queue peek", err)
			return
		}
		api.Write(w, http.StatusOK, map[string]any{"book": apiCardOf(d.Card), "source": "queue"})
		return
	}
	s.writeSeriesNext(w, r, ended, true)
}

// seriesNextBook is the one lookup behind "next in series", for the app (POST /api/v1/queue/next) and the
// web player (POST /hx/player/next): the book after ended in its first series, when the person's setting is
// on and the book is one they may listen to, Tendshelf can play, and have not finished. ok is false for
// anything else. The queue is never touched here, and a book the person cannot see never shows: the series
// is read through the scope SQL.
func (s *server) seriesNextBook(r *http.Request, ended string) (b library.Book, ok bool, err error) {
	if ended == "" {
		return b, false, nil
	}
	ctx := r.Context()
	u := currentUser(r)
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil || !set.NextInSeries {
		return b, false, err
	}
	next, _, err := s.nextInSeries(r, ended)
	if err != nil || next == nil {
		return b, false, err
	}
	b, err = s.lib.Book(ctx, next.ID)
	if errors.Is(err, library.ErrNotFound) {
		return b, false, nil
	}
	if err != nil {
		return b, false, err
	}
	if !s.nextPlayable(ctx, u, b) {
		return b, false, nil
	}
	p, err := s.progress.Get(ctx, u.ID, b.ID)
	if errors.Is(err, progress.ErrNotFound) {
		return b, true, nil
	}
	return b, err == nil && !p.Finished(), err
}

// writeSeriesNext answers the empty-queue case of POST /api/v1/queue/next when the app asked (series).
// Anything but a playable next book is a 204.
func (s *server) writeSeriesNext(w http.ResponseWriter, r *http.Request, ended string, series bool) {
	if !series {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b, ok, err := s.seriesNextBook(r, ended)
	if err != nil {
		s.serverError(w, r, "api series next", err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), b.ID)
	if errors.Is(err, library.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.serverError(w, r, "api series next", err)
		return
	}
	api.Write(w, http.StatusOK, map[string]any{"book": apiCardOf(d.Card), "source": "series"})
}

// queueable reports whether a book can be queued: not refused, not missing and with an audio file.
func (s *server) queueable(ctx context.Context, b library.Book) bool {
	if b.RefusedReason != "" || b.MissingSince != 0 || b.Kind == library.BookText {
		return false
	}
	files, err := s.lib.AudioFiles(ctx, b.ID)
	return err == nil && len(files) > 0
}

// popQueue takes the person's first playable queued book (dropping ended first, on the first pop
// only) and hands it to use. use reports false when it could not build the book, and the loop pops
// the next one. At most QueueMax books are tried; nothing left reports found false.
func (s *server) popQueue(ctx context.Context, u users.User, sc library.Scope, ended string, use func(library.Book) (bool, error)) (bool, error) {
	for range library.QueueMax {
		id, err := s.lib.QueuePop(ctx, sc, u.ID, ended)
		ended = ""
		if errors.Is(err, library.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		b, err := s.lib.Book(ctx, id)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !s.nextPlayable(ctx, u, b) {
			continue
		}
		if ok, err := use(b); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}
