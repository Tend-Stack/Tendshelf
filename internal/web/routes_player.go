package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

func (s *server) registerPlayer(mux *http.ServeMux) {
	mux.HandleFunc("GET /hx/player", s.getCurrentPlayer)
	mux.HandleFunc("GET /hx/player/{bookID}", s.getPlayer)
	mux.HandleFunc("POST /hx/player/next", s.postPlayerNext)
	mux.HandleFunc("GET /hx/player/panel/{bookID}", s.getNowPanel)
	mux.HandleFunc("GET /hx/player/panel/{bookID}/about", s.getNowPanelAbout)
	mux.HandleFunc("GET /hx/player/panel/{bookID}/bookmarks", s.getNowPanelBookmarks)
	mux.HandleFunc("GET /hx/player/panel/{bookID}/count", s.getNowPanelCount)
	mux.HandleFunc("GET /hx/player/panel/{bookID}/chapter/{index}", s.getNowPanelChapter)
}

// getPlayer answers GET /hx/player/{bookID}: the data-only PlayerData fragment for a book the
// person may listen to.
func (s *server) getPlayer(w http.ResponseWriter, r *http.Request) {
	b, ok := s.playableBook(w, r, r.PathValue("bookID"))
	if !ok {
		return
	}
	s.renderPlayer(w, r, b)
}

func (s *server) renderPlayer(w http.ResponseWriter, r *http.Request, b library.Book) {
	v, err := s.playerView(r.Context(), currentUser(r), s.scope(r), b)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "build player", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.PlayerData(v))
}

// getCurrentPlayer answers GET /hx/player: the person's newest unfinished visible book, or 204 when
// there is none (htmx and fetch both leave the page alone).
func (s *server) getCurrentPlayer(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ls, err := s.lib.ContinueListening(r.Context(), s.scope(r), u.ID, 1)
	if err != nil {
		s.serverError(w, r, "continue listening", err)
		return
	}
	if len(ls) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	b, err := s.lib.Book(r.Context(), ls[0].Book.ID)
	if errors.Is(err, library.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.serverError(w, r, "load book", err)
		return
	}
	if !s.can.Can(r.Context(), u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) ||
		b.RefusedReason != "" || b.MissingSince != 0 || b.Kind == library.BookText {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.renderPlayer(w, r, b)
}

// postPlayerNext answers POST /hx/player/next: autoplay. It drops the book that just ended (form
// field ended, optional) from Up next, pops the first book the person may play and answers with its
// PlayerData fragment, or 204 when Up next has nothing to play. A book that cannot be built is
// skipped, at most QueueMax times.
func (s *server) postPlayerNext(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, apiQueueBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, queueErrEnded, http.StatusBadRequest)
		return
	}
	ended := r.PostForm.Get("ended")
	if r.PostForm.Has("ended") && (ended == "" || len(ended) > endedMaxLen) {
		http.Error(w, queueErrEnded, http.StatusBadRequest)
		return
	}
	u, sc := currentUser(r), s.scope(r)
	var v components.PlayerDataView
	found, err := s.popQueue(r.Context(), u, sc, ended, func(b library.Book) (bool, error) {
		var err error
		v, err = s.playerView(r.Context(), u, sc, b)
		if errors.Is(err, library.ErrNotFound) {
			return false, nil
		}
		return err == nil, err
	})
	switch {
	case err != nil:
		s.serverError(w, r, "player next", err)
	case !found:
		w.WriteHeader(http.StatusNoContent)
	default:
		s.fragment(w, r, http.StatusOK, components.PlayerData(v))
	}
}

// The Now playing panel (NP1): fragments for the desktop side panel. Each one loads the book with
// ViewBook, so a hidden or unknown book is a 404; the bookmark parts also need ListenBook.

// nowPanelBook loads the book and its detail for a panel fragment; listen reports ListenBook.
func (s *server) nowPanelBook(w http.ResponseWriter, r *http.Request) (d library.BookDetail, listen, ok bool) {
	id := r.PathValue("bookID")
	book, ok := s.loadBook(w, r, id, share.ViewBook)
	if !ok {
		return d, false, false
	}
	d, err := s.lib.BookDetail(r.Context(), s.scope(r), id)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return d, false, false
	}
	if err != nil {
		s.serverError(w, r, "now panel", err)
		return d, false, false
	}
	listen = s.can.Can(r.Context(), currentUser(r), share.ListenBook, share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID})
	return d, listen, true
}

// nowPanelListenBook is nowPanelBook for the parts that need ListenBook: without it, a 404.
func (s *server) nowPanelListenBook(w http.ResponseWriter, r *http.Request) (library.BookDetail, bool) {
	d, listen, ok := s.nowPanelBook(w, r)
	if !ok {
		return d, false
	}
	if !listen {
		s.notFound(w, r)
		return d, false
	}
	return d, true
}

// getNowPanel answers GET /hx/player/panel/{bookID}: the whole panel with About open. ?chapter=N
// is ignored: the chapter that plays now is drawn by the full player's own top.
func (s *server) getNowPanel(w http.ResponseWriter, r *http.Request) {
	d, listen, ok := s.nowPanelBook(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	marks, queued := 0, 0
	if listen {
		var err error
		if marks, _, err = s.progress.BookmarkCounts(ctx, u.ID, d.Book.ID); err != nil {
			s.serverError(w, r, "now panel bookmarks", err)
			return
		}
		if queued, err = s.lib.QueueCount(ctx, s.scope(r), u.ID); err != nil {
			s.serverError(w, r, "now panel queue", err)
			return
		}
	}
	rails, err := s.bookRailData(r, d)
	if err != nil {
		s.serverError(w, r, "now panel rails", err)
		return
	}
	about, err := s.nowAboutView(r, d, rails)
	if err != nil {
		s.serverError(w, r, "now panel about", err)
		return
	}
	v := nowPanelView(d, listen, marks, queued)
	v.About = &about
	if s.canManageServerUsers(r) {
		v.ShareHref = "/books/" + d.Book.ID + "/share"
	}
	if s.canShelve(r) {
		v.Menu.ShelfHref, v.Menu.ShelfLabel = shelfSheetHref(d.Book.ID), shelfAddLabel
	}
	s.fragment(w, r, http.StatusOK, components.NowPanel(v))
}

// getNowPanelAbout answers GET /hx/player/panel/{bookID}/about: the About tab alone.
func (s *server) getNowPanelAbout(w http.ResponseWriter, r *http.Request) {
	d, _, ok := s.nowPanelBook(w, r)
	if !ok {
		return
	}
	rails, err := s.bookRailData(r, d)
	if err != nil {
		s.serverError(w, r, "now panel rails", err)
		return
	}
	about, err := s.nowAboutView(r, d, rails)
	if err != nil {
		s.serverError(w, r, "now panel about", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.NowPanelAbout(about))
}

// getNowPanelBookmarks answers GET /hx/player/panel/{bookID}/bookmarks: the newest bookmarks of the
// signed-in person, with the tab's count out of band.
func (s *server) getNowPanelBookmarks(w http.ResponseWriter, r *http.Request) {
	d, ok := s.nowPanelListenBook(w, r)
	if !ok {
		return
	}
	ctx, u := r.Context(), currentUser(r)
	list, err := s.progress.Bookmarks(ctx, u.ID, d.Book.ID, false)
	if err != nil {
		s.serverError(w, r, "now panel bookmarks", err)
		return
	}
	total, _, err := s.progress.BookmarkCounts(ctx, u.ID, d.Book.ID)
	if err != nil {
		s.serverError(w, r, "now panel bookmarks", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.NowPanelBookmarks(nowBookmarksView(d, list, total, time.Now())))
}

// getNowPanelCount answers GET /hx/player/panel/{bookID}/count: the number on the Bookmarks tab.
func (s *server) getNowPanelCount(w http.ResponseWriter, r *http.Request) {
	d, ok := s.nowPanelListenBook(w, r)
	if !ok {
		return
	}
	total, _, err := s.progress.BookmarkCounts(r.Context(), currentUser(r).ID, d.Book.ID)
	if err != nil {
		s.serverError(w, r, "now panel count", err)
		return
	}
	s.fragment(w, r, http.StatusOK, components.NowCount(strconv.Itoa(total)))
}

// getNowPanelChapter answers GET /hx/player/panel/{bookID}/chapter/{index}: the chapter line for the
// 0-based chapter. An index that is not a whole number, or past the last chapter, is a 404.
func (s *server) getNowPanelChapter(w http.ResponseWriter, r *http.Request) {
	d, _, ok := s.nowPanelBook(w, r)
	if !ok {
		return
	}
	idx, err := strconv.Atoi(strings.TrimSpace(r.PathValue("index")))
	if err != nil || idx < 0 || idx >= len(d.Chapters) {
		s.notFound(w, r)
		return
	}
	lead, rest := nowChapterLine(d.Chapters, idx)
	s.fragment(w, r, http.StatusOK, components.NowChapter(lead, rest))
}
