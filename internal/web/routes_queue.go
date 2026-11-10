package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The Up next screens: the list in the Now playing panel (GET /hx/queue?in=panel), the full page
// (GET /queue) and the three changes (add, move, remove). The data and its rules are library.Queue*
// (the JSON API shares them). Every change is a form: htmx answers in place with a fresh fragment
// (never a redirect, which would reload the window and stop the audio); a plain post is redirected.

func (s *server) registerQueue(mux *http.ServeMux) {
	mux.HandleFunc("GET /hx/queue", s.getHXQueue)
	mux.HandleFunc("GET /queue", s.getQueue)
	mux.HandleFunc("POST /queue", s.postQueue)
	mux.HandleFunc("POST /queue/{bookID}/move", s.postQueueMove)
	mux.HandleFunc("POST /queue/{bookID}/delete", s.postQueueDelete)
}

const (
	queueInPanel = "panel"
	queueInPage  = "page"
	queueHref    = "/queue"
)

// queueIn normalises the "in" field: the panel, else the page.
func queueIn(v string) string {
	if v == queueInPanel {
		return queueInPanel
	}
	return queueInPage
}

// queueFocus says which button the keyboard lands on after a change.
type queueFocus struct {
	bookID, key string // a moved row (book or episode id): "up" or "down"
	removeAt    int    // after a remove: the index the removed row had, or -1
}

var queueNoFocus = queueFocus{removeAt: -1}

// queueList builds the Up next list. in is "panel" or "page"; focus says where the keyboard goes.
func (s *server) queueList(r *http.Request, in string, focus queueFocus) (components.UpNextView, error) {
	u := currentUser(r)
	list, err := s.lib.QueueIn(r.Context(), s.scope(r), u.ID, s.queueKinds(r))
	if err != nil {
		return components.UpNextView{}, err
	}
	v := components.UpNextView{
		ID: "up-next", In: in, CSRF: s.csrf.TokenFrom(r.Context()), Next: queueHref,
		EmptyText: queueEmpty, Count: fmt.Sprint(len(list)),
	}
	if in == queueInPanel {
		v.ID = "now-up-next"
	}
	for i, e := range list {
		card, title := cardView(e.Book), e.Book.Title
		if e.Kind == library.QueueKindEpisode {
			card, title = queueEpisodeCard(e.Episode), e.Episode.Title
		}
		row := components.UpNextRow{
			ID:          e.ID,
			Card:        card,
			UpLabel:     fmt.Sprintf(queueMoveUpFmt, title),
			DownLabel:   fmt.Sprintf(queueMoveDownFmt, title),
			RemoveLabel: fmt.Sprintf(queueRemoveFmt, title),
			First:       i == 0, Last: i == len(list)-1,
		}
		if e.ID == focus.bookID && focus.key != "" {
			row.Focus = focus.key
			// A row that reached an end has no arrow that way: the other arrow keeps the focus.
			if row.First && row.Focus == "up" {
				row.Focus = "down"
			}
			if row.Last && row.Focus == "down" {
				row.Focus = "up"
			}
		}
		if focus.removeAt >= 0 && i == min(focus.removeAt, len(list)-1) {
			row.Focus = "remove"
		}
		v.Rows = append(v.Rows, row)
	}
	v.FocusEmpty = focus.removeAt >= 0 && len(list) == 0
	return v, nil
}

func (s *server) getHXQueue(w http.ResponseWriter, r *http.Request) {
	v, err := s.queueList(r, queueIn(r.URL.Query().Get("in")), queueNoFocus)
	if err != nil {
		s.serverError(w, r, "queue list", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.UpNext(v))
}

func (s *server) getQueue(w http.ResponseWriter, r *http.Request) {
	v, err := s.queueList(r, queueInPage, queueNoFocus)
	if err != nil {
		s.serverError(w, r, "queue page", err)
		return
	}
	s.page(w, r, http.StatusOK, queueTitle, "", pages.QueuePage(queueTitle, v))
}

// queueBook loads a book the person may open for a queue change. When it cannot (unknown or hidden),
// it answers with answer(404) and reports false: for htmx that re-renders the same target fresh with
// an error toast, for a plain post the 404 page.
func (s *server) queueBook(w http.ResponseWriter, r *http.Request, id string, a share.Action, answer func(status int, msg string)) (library.Book, bool) {
	b, err := s.lib.Book(r.Context(), id)
	if err != nil && !errors.Is(err, library.ErrNotFound) {
		s.serverError(w, r, "queue book", err)
		return library.Book{}, false
	}
	if err != nil || !s.can.Can(r.Context(), currentUser(r), a, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) {
		if isHTMX(r) {
			answer(http.StatusNotFound, queueErrGone)
		} else {
			s.notFound(w, r)
		}
		return library.Book{}, false
	}
	return b, true
}

// queueKinds says which kinds of item this person's Up next shows: books always, episodes while podcasts are on
// and the person may use them.
func (s *server) queueKinds(r *http.Request) library.QueueKinds {
	if s.podcastsOn(r) && s.can.Can(r.Context(), currentUser(r), share.UsePodcasts, podServer) {
		return library.QueueAll
	}
	return library.QueueBooks
}

// queueEpisodeCard is the row of a queued episode: the show's picture, the title, and "{show} · {length}".
func queueEpisodeCard(e library.QueueEpisode) components.CardView {
	c := podArt(e.ShowID, e.ShowTitle, e.ArtSHA)
	c.ID, c.Title, c.Href = e.ID, e.Title, "/podcasts/episodes/"+url.PathEscape(e.ID)
	c.Byline, c.Duration = e.ShowTitle, durationText(e.DurationMs)
	return c
}

// queueEpisode loads an episode the person may open for a queue change (podcasts on, ListenEpisode), the way
// queueBook loads a book: the same answers, so an unknown, hidden or off episode looks like any other miss.
func (s *server) queueEpisode(w http.ResponseWriter, r *http.Request, id string, answer func(status int, msg string)) (podcast.Episode, bool) {
	miss := func() (podcast.Episode, bool) {
		if isHTMX(r) {
			answer(http.StatusNotFound, queueErrGone)
		} else {
			s.notFound(w, r)
		}
		return podcast.Episode{}, false
	}
	if !s.podcastsOn(r) || !s.can.Can(r.Context(), currentUser(r), share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: id}) {
		return miss()
	}
	ep, err := s.podcasts.Episode(r.Context(), currentUser(r).ID, id)
	if errors.Is(err, podcast.ErrNotFound) {
		return miss()
	}
	if err != nil {
		s.serverError(w, r, "queue episode", err)
		return podcast.Episode{}, false
	}
	return ep, true
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// queueMenuView is the dropdown's view for a book (closed; focus on the toggle after an answer).
func (s *server) queueMenuView(r *http.Request, bookID, next string) components.QueueMenuView {
	return components.QueueMenuView{
		BookID: bookID, CSRF: s.csrf.TokenFrom(r.Context()), Next: next, Toggle: queueToggle,
		PlayNext: queuePlayNext, AddEnd: queueAddEnd, Focus: true,
	}
}

// menuAnswer answers an add in place: the closed menu and a toast.
func (s *server) menuAnswer(w http.ResponseWriter, r *http.Request, status int, bookID, next, kind, msg string) {
	toast := templ.Component(components.ToastOOB(kind, msg))
	if kind == "err" {
		toast = components.ErrorToast(msg)
	}
	s.fragment(w, r, status, templ.Join(components.QueueMenu(s.queueMenuView(r, bookID, next)), toast))
}

func (s *server) postQueue(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	id := r.PostFormValue("book_id")
	next := "/books/" + url.PathEscape(id)
	if raw := r.PostFormValue("next"); raw != "" {
		next = auth.SafeNext(raw)
	}
	fail := func(status int, msg string) {
		if isHTMX(r) {
			s.menuAnswer(w, r, status, id, next, "err", msg)
			return
		}
		s.errorPage(w, r, status)
	}
	var front bool
	switch r.PostFormValue("where") {
	case "next":
		front = true
	case "end":
	default:
		fail(http.StatusBadRequest, queueErrWhere)
		return
	}
	if eid := r.PostFormValue("episode_id"); eid != "" {
		s.postQueueEpisode(w, r, eid, front)
		return
	}
	b, ok := s.queueBook(w, r, id, share.ListenBook, fail)
	if !ok {
		return
	}
	if b.Kind == library.BookText {
		fail(http.StatusNotFound, queueErrGone) // invariant 8: a text book is not a thing to play
		return
	}
	if !s.queueable(r.Context(), b) {
		fail(http.StatusConflict, queueErrUnplayable)
		return
	}
	added, err := s.lib.QueueAdd(r.Context(), currentUser(r).ID, b.ID, front)
	switch {
	case errors.Is(err, library.ErrQueueFull):
		fail(http.StatusConflict, queueErrFull)
		return
	case errors.Is(err, library.ErrNotFound):
		fail(http.StatusNotFound, queueErrGone)
		return
	case err != nil:
		s.serverError(w, r, "queue add", err)
		return
	}
	msg := queueAddedEnd
	switch {
	case front:
		msg = queueAddedFront
	case !added:
		msg = queueAlready
	}
	if isHTMX(r) {
		s.menuAnswer(w, r, http.StatusOK, b.ID, next, "ok", msg)
		return
	}
	s.redirect(w, r, next)
}

// queueChange runs a move or a remove (the form is parsed): it checks the book, runs do, and answers with the fresh list
// (htmx) or a redirect to the page (plain). focusOf makes the focus from the list before the change.
func (s *server) queueChange(w http.ResponseWriter, r *http.Request, do func(bookID string) error, focusOf func(before []library.QueueEntry, bookID string) queueFocus) {
	in, id := queueIn(r.PostFormValue("in")), r.PathValue("bookID")
	answer := func(status int, msg string, focus queueFocus) {
		v, err := s.queueList(r, in, focus)
		if err != nil {
			s.serverError(w, r, "queue list", err)
			return
		}
		body := templ.Component(components.UpNext(v))
		if msg != "" {
			body = templ.Join(body, components.ErrorToast(msg))
		}
		s.fragment(w, r, status, body)
	}
	miss := func(status int, msg string) { answer(status, msg, queueNoFocus) }
	if strings.HasPrefix(id, "pe_") {
		if _, ok := s.queueEpisode(w, r, id, miss); !ok {
			return
		}
	} else if _, ok := s.queueBook(w, r, id, share.ViewBook, miss); !ok {
		return
	}
	focus := queueNoFocus
	if isHTMX(r) {
		before, err := s.lib.QueueIn(r.Context(), s.scope(r), currentUser(r).ID, s.queueKinds(r))
		if err != nil {
			s.serverError(w, r, "queue list", err)
			return
		}
		focus = focusOf(before, id)
	}
	err := do(id)
	switch {
	case errors.Is(err, library.ErrNotFound):
		if isHTMX(r) {
			answer(http.StatusNotFound, queueErrGone, queueNoFocus)
		} else {
			s.notFound(w, r)
		}
	case err != nil:
		s.serverError(w, r, "queue change", err)
	case isHTMX(r):
		answer(http.StatusOK, "", focus)
	default:
		s.redirect(w, r, queueHref+"#item-"+url.PathEscape(id))
	}
}

func (s *server) postQueueMove(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	dir, delta := r.PostFormValue("dir"), 0
	switch dir {
	case "up":
		delta = -1
	case "down":
		delta = 1
	default:
		if isHTMX(r) {
			v, err := s.queueList(r, queueIn(r.PostFormValue("in")), queueNoFocus)
			if err != nil {
				s.serverError(w, r, "queue list", err)
				return
			}
			s.fragment(w, r, http.StatusBadRequest, templ.Join(components.UpNext(v), components.ErrorToast(queueErrDir)))
			return
		}
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	s.queueChange(w, r, func(id string) error {
		return s.lib.QueueMoveIn(r.Context(), s.scope(r), currentUser(r).ID, id, delta, s.queueKinds(r))
	}, func(_ []library.QueueEntry, id string) queueFocus {
		return queueFocus{bookID: id, key: dir, removeAt: -1}
	})
}

func (s *server) postQueueDelete(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	s.queueChange(w, r, func(id string) error {
		return s.lib.QueueRemove(r.Context(), currentUser(r).ID, id)
	}, func(before []library.QueueEntry, id string) queueFocus {
		for i, e := range before {
			if e.ID == id {
				return queueFocus{removeAt: i}
			}
		}
		return queueNoFocus
	})
}

// postQueueEpisode is POST /queue for an episode (form field episode_id): Play next or Add to queue from an
// episode row. The person must be able to listen to it; htmx answers with the closed row menu and a toast.
func (s *server) postQueueEpisode(w http.ResponseWriter, r *http.Request, id string, front bool) {
	next := "/podcasts/episodes/" + url.PathEscape(id)
	if raw := r.PostFormValue("next"); raw != "" {
		next = auth.SafeNext(raw)
	}
	var ep podcast.Episode
	answer := func(status int, kind, msg string) {
		if isHTMX(r) {
			toast := templ.Component(components.ToastOOB(kind, msg))
			if kind == "err" {
				toast = components.ErrorToast(msg)
			}
			s.fragment(w, r, status, templ.Join(components.EpisodeMenu(s.episodeMenuView(r, id, ep.Title, next)), toast))
			return
		}
		s.errorPage(w, r, status)
	}
	ep, ok := s.queueEpisode(w, r, id, func(status int, msg string) { answer(status, "err", msg) })
	if !ok {
		return
	}
	if podIsVideo(ep) {
		answer(http.StatusConflict, "err", podQueueUnplayable)
		return
	}
	added, err := s.lib.QueueAddEpisode(r.Context(), currentUser(r).ID, ep.ID, front)
	switch {
	case errors.Is(err, library.ErrQueueFull):
		answer(http.StatusConflict, "err", queueErrFull)
		return
	case errors.Is(err, library.ErrNotFound):
		answer(http.StatusNotFound, "err", queueErrGone)
		return
	case err != nil:
		s.serverError(w, r, "queue add episode", err)
		return
	}
	msg := queueAddedEnd
	switch {
	case front:
		msg = queueAddedFront
	case !added:
		msg = queueAlready
	}
	if isHTMX(r) {
		answer(http.StatusOK, "ok", msg)
		return
	}
	s.redirect(w, r, next)
}

// episodeMenuView is the row menu of an episode (closed): Play next and Add to queue. next is where a plain
// form returns to.
func (s *server) episodeMenuView(r *http.Request, id, title, next string) components.EpisodeMenuView {
	toggle := podMenuNoTitle
	if title != "" {
		toggle = fmt.Sprintf(podMenuFmt, title)
	}
	return components.EpisodeMenuView{
		ID: id, CSRF: s.csrf.TokenFrom(r.Context()), Next: next,
		Toggle: toggle, PlayNext: queuePlayNext, AddEnd: queueAddEnd,
	}
}
