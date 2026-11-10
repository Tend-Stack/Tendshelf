package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
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

// apiQueueItem is one queued item. Kind is "book" or "episode"; exactly one of Book and Episode is set. The
// book fields are what they were before episodes; kind is additive.
type apiQueueItem struct {
	Kind    string         `json:"kind"`
	Book    *apiBookCard   `json:"book,omitempty"`
	Episode *apiPodEpisode `json:"episode,omitempty"`
	AddedAt int64          `json:"addedAt"`
}

// apiQueueKinds reads ?kinds=book,episode. Without it only books are listed, popped and counted, as before
// episodes existed (an older app cannot read an item with no book); episode needs podcasts on and the right to use
// them, else it is quietly left out. A list naming no known kind is a 400 (ok false).
func (s *server) apiQueueKinds(w http.ResponseWriter, r *http.Request) (library.QueueKinds, bool) {
	raw, asked := r.URL.Query()["kinds"]
	if !asked {
		return library.QueueBooks, true
	}
	var k library.QueueKinds
	known := false
	for _, v := range raw {
		for _, tok := range strings.Split(v, ",") {
			switch strings.TrimSpace(tok) {
			case library.QueueKindBook:
				k.Books, known = true, true
			case library.QueueKindEpisode:
				k.Episodes, known = true, true
			}
		}
	}
	if !known {
		apiBadField(w, "kinds", queueErrKinds)
		return k, false
	}
	if k.Episodes && !(s.podcastsOn(r) && s.can.Can(r.Context(), currentUser(r), share.UsePodcasts, podServer)) {
		k.Episodes = false
	}
	return k, true
}

// apiEpisodeItem checks that the person may queue or touch an episode, answering 404 like any unknown id.
func (s *server) apiEpisodeItem(w http.ResponseWriter, r *http.Request, id string) (podcast.Episode, bool) {
	if !s.podcastsOn(r) || !s.can.Can(r.Context(), currentUser(r), share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: id}) {
		s.notFound(w, r)
		return podcast.Episode{}, false
	}
	ep, err := s.podcasts.Episode(r.Context(), currentUser(r).ID, id)
	switch {
	case errors.Is(err, podcast.ErrNotFound):
		s.notFound(w, r)
		return ep, false
	case err != nil:
		s.serverError(w, r, "api queue episode", err)
		return ep, false
	}
	return ep, true
}

// writeAPIQueue answers 200 with the person's visible queue. next is always null: the list is
// short and whole, so there is nothing to page.
func (s *server) writeAPIQueue(w http.ResponseWriter, r *http.Request) {
	kinds, ok := s.apiQueueKinds(w, r)
	if !ok {
		return
	}
	list, err := s.lib.QueueIn(r.Context(), s.scope(r), currentUser(r).ID, kinds)
	if err != nil {
		s.serverError(w, r, "api queue", err)
		return
	}
	items := make([]apiQueueItem, 0, len(list))
	for _, e := range list {
		if e.Kind == library.QueueKindEpisode {
			items = append(items, apiQueueItem{Kind: e.Kind, Episode: &apiPodEpisode{ID: e.ID, ShowID: e.Episode.ShowID, Title: e.Episode.Title,
				Show: e.Episode.ShowTitle, ArtURL: apiPodArt(e.Episode.ShowID), PublishedAt: e.Episode.PublishedAt, DurationMs: e.Episode.DurationMs}, AddedAt: e.AddedAt})
			continue
		}
		card := apiCardOf(e.Book)
		items = append(items, apiQueueItem{Kind: e.Kind, Book: &card, AddedAt: e.AddedAt})
	}
	api.Write(w, http.StatusOK, map[string]any{"items": items, "next": nil, "total": len(items)})
}

func (s *server) getAPIQueue(w http.ResponseWriter, r *http.Request) {
	s.writeAPIQueue(w, r)
}

func (s *server) postAPIQueue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BookID    string `json:"bookId"`
		EpisodeID string `json:"episodeId"`
		Where     string `json:"where"`
	}
	// The book id lives in the body, so the body is read first, then access to the book is checked
	// before anything about the book is said.
	if !apiJSONBody(w, r, apiQueueBody, &body) {
		return
	}
	if (body.BookID == "") == (body.EpisodeID == "") {
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
	var err error
	if body.EpisodeID != "" {
		ep, ok := s.apiEpisodeItem(w, r, body.EpisodeID)
		if !ok {
			return
		}
		if podIsVideo(ep) {
			api.ErrorField(w, http.StatusConflict, api.CodeConflict, "episodeId", podQueueUnplayable)
			return
		}
		_, err = s.lib.QueueAddEpisode(r.Context(), currentUser(r).ID, ep.ID, front)
	} else {
		b, ok := s.loadBook(w, r, body.BookID, share.ListenBook)
		if !ok {
			return
		}
		if !s.queueable(r.Context(), b) {
			api.ErrorField(w, http.StatusConflict, api.CodeConflict, "bookId", queueErrUnplayable)
			return
		}
		_, err = s.lib.QueueAdd(r.Context(), currentUser(r).ID, b.ID, front)
	}
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

// apiQueueTouch checks the item a move or a remove names: a book the person may view, or (an id starting pe_) an
// episode they may listen to.
func (s *server) apiQueueTouch(w http.ResponseWriter, r *http.Request, id string) bool {
	if strings.HasPrefix(id, "pe_") {
		_, ok := s.apiEpisodeItem(w, r, id)
		return ok
	}
	_, ok := s.loadBook(w, r, id, share.ViewBook)
	return ok
}

func (s *server) postAPIQueueMove(w http.ResponseWriter, r *http.Request) {
	if !s.apiQueueTouch(w, r, r.PathValue("bookID")) {
		return
	}
	kinds, ok := s.apiQueueKinds(w, r)
	if !ok {
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
	err := s.lib.QueueMoveIn(r.Context(), s.scope(r), currentUser(r).ID, r.PathValue("bookID"), delta, kinds)
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
	if !s.apiQueueTouch(w, r, id) {
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
	kinds, ok := s.apiQueueKinds(w, r)
	if !ok {
		return
	}
	var card apiBookCard
	var episode *apiPodPlayback
	found, err := s.popQueue(r.Context(), currentUser(r), s.scope(r), kinds, body.Ended, func(b library.Book) (bool, error) {
		d, err := s.lib.BookDetail(r.Context(), s.scope(r), b.ID)
		if errors.Is(err, library.ErrNotFound) {
			return false, nil
		}
		card = apiCardOf(d.Card)
		return err == nil, err
	}, func(ep podcast.Episode) (bool, error) {
		doc := s.apiPodPlaybackOf(r, ep)
		episode = &doc
		return true, nil
	})
	switch {
	case err != nil:
		s.serverError(w, r, "api queue next", err)
	case found && episode != nil:
		api.Write(w, http.StatusOK, map[string]any{"kind": library.QueueKindEpisode, "episode": episode, "source": "queue"})
	case found:
		api.Write(w, http.StatusOK, map[string]any{"kind": library.QueueKindBook, "book": card, "source": "queue"})
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
	kinds, ok := s.apiQueueKinds(w, r)
	if !ok {
		return
	}
	entries, err := s.lib.QueueIn(ctx, s.scope(r), u.ID, kinds)
	if err != nil {
		s.serverError(w, r, "api queue peek", err)
		return
	}
	for _, e := range entries {
		if ended != "" && e.ID == ended {
			continue
		}
		if e.Kind == library.QueueKindEpisode {
			ep, ok := s.episodeFor(ctx, u, e.ID)
			if !ok {
				continue
			}
			api.Write(w, http.StatusOK, map[string]any{"kind": library.QueueKindEpisode, "episode": s.apiPodPlaybackOf(r, ep), "source": "queue"})
			return
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
		api.Write(w, http.StatusOK, map[string]any{"kind": library.QueueKindBook, "book": apiCardOf(d.Card), "source": "queue"})
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

// popQueue takes the person's first playable queued item of the asked kinds (dropping ended first, on the first
// pop only) and hands it to useBook or useEpisode. use reports false when it could not build the item, and the loop
// pops the next one. At most QueueMax items are tried; nothing left reports found false. useEpisode may be nil
// when kinds has no episodes.
func (s *server) popQueue(ctx context.Context, u users.User, sc library.Scope, kinds library.QueueKinds, ended string,
	useBook func(library.Book) (bool, error), useEpisode func(podcast.Episode) (bool, error)) (bool, error) {
	for range library.QueueMax {
		it, err := s.lib.QueuePopIn(ctx, sc, u.ID, ended, kinds)
		ended = ""
		if errors.Is(err, library.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if it.Kind == library.QueueKindEpisode {
			if useEpisode == nil {
				continue
			}
			ep, ok := s.episodeFor(ctx, u, it.ID)
			if !ok {
				continue
			}
			if ok, err := useEpisode(ep); err != nil || ok {
				return ok, err
			}
			continue
		}
		b, err := s.lib.Book(ctx, it.ID)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !s.nextPlayable(ctx, u, b) {
			continue
		}
		if ok, err := useBook(b); err != nil || ok {
			return ok, err
		}
	}
	return false, nil
}

// episodeFor loads an episode the person may play, or reports false (unknown, refused, video, podcasts off).
func (s *server) episodeFor(ctx context.Context, u users.User, id string) (podcast.Episode, bool) {
	if s.podcasts == nil || !s.podcasts.Enabled(ctx) || !s.can.Can(ctx, u, share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: id}) {
		return podcast.Episode{}, false
	}
	ep, err := s.podcasts.Episode(ctx, u.ID, id)
	if err != nil || podIsVideo(ep) {
		return podcast.Episode{}, false
	}
	return ep, true
}
