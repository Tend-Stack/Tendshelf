package web

import (
	"errors"
	"net/http"
	"net/url"
	"slices"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	"github.com/a-h/templ"
)

// The book page and the pages of authors, narrators and series (P1b-6), and search. Everything is
// read with the person's scope: a book, person or series they cannot see answers 404 exactly like
// an id that does not exist, and counts and groups only cover what they may open.

func (s *server) registerBook(mux *http.ServeMux) {
	mux.HandleFunc("GET /books/{id}", s.getBook)
	mux.HandleFunc("GET /books/{id}/{tab}", s.getBook)
	mux.HandleFunc("GET /hx/books/{id}/play", s.getBookPlay)
	mux.HandleFunc("GET /authors/{id}", s.getPerson(library.RoleAuthor))
	mux.HandleFunc("GET /narrators/{id}", s.getPerson(library.RoleNarrator))
	mux.HandleFunc("GET /series/{id}", s.getSeries)
	mux.HandleFunc("GET /search", s.getSearch)
}

func (s *server) getBook(w http.ResponseWriter, r *http.Request) {
	tab := r.PathValue("tab")
	if tab == "" {
		tab = "about"
	}
	s.renderBook(w, r, r.PathValue("id"), tab, http.StatusOK, nil)
}

// renderBook is the book page. shelf is non-nil when a shelf form was refused: the page then
// answers with that status and the "Add to shelf" sheet open, showing the refusal.
func (s *server) renderBook(w http.ResponseWriter, r *http.Request, id, tab string, status int, shelf *shelfForm) {
	ctx := r.Context()
	if !bookTabValid(tab) {
		s.notFound(w, r)
		return
	}
	book, ok := s.loadBook(w, r, id, share.ViewBook)
	if !ok {
		return
	}
	sc := s.scope(r)
	d, err := s.lib.BookDetail(ctx, sc, id)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "book detail", err)
		return
	}
	v := bookHeader(d, tab)
	target := share.Target{Kind: share.TargetBook, ID: d.Book.ID, LibraryID: d.Book.LibraryID}
	listen := s.can.Can(ctx, currentUser(r), share.ListenBook, target)
	if !listen {
		v.Tabs = slices.DeleteFunc(v.Tabs, func(t pages.BookTab) bool { return t.ID == "bookmarks" })
	}
	files, prog, canPlay, err := s.bookPlayData(r, book)
	if err != nil {
		s.serverError(w, r, "book play", err)
		return
	}
	if canPlay {
		v.Play = s.bookPlayView(r, book, files, prog)
	}
	shelves := s.canShelve(r)
	var sheet templ.Component
	styles := pages.BookStyles
	if shelves {
		v.ShelfHref, v.ShelfLabel = shelfSheetHref(id), shelfAddLabel
		styles = append(append([]string(nil), styles...), pages.ShelfStyles[2])
		if shelf != nil || r.URL.Query().Get("sheet") == "shelves" {
			if sheet, err = s.shelfSheet(r, id, shelf); err != nil {
				s.serverError(w, r, "shelf sheet", err)
				return
			}
		}
	}
	if listen {
		// The Bookmarks tab swaps only #book-panel, so its stylesheet is linked whatever tab opens.
		styles = append(append([]string(nil), styles...), pages.BookmarkStyles...)
	}
	switch tab {
	case "about":
		var series, narrated []library.BookCard
		var seqs map[string]string
		if len(d.Series) > 0 {
			sp, err := s.lib.SeriesInfo(ctx, sc, d.Series[0].ID)
			if err != nil && !errors.Is(err, library.ErrNotFound) {
				s.serverError(w, r, "book series", err)
				return
			}
			series = sp.Books
			if len(series) > 0 {
				if seqs, err = s.lib.SeriesSequences(ctx, sc, d.Series[0].ID); err != nil {
					s.serverError(w, r, "book series places", err)
					return
				}
			}
		}
		if len(d.Card.Narrators) > 0 {
			page, err := s.lib.ListBooks(ctx, sc, library.ListQuery{NarratorID: d.Card.Narrators[0].ID, Limit: bookRailBooks + 1})
			if err != nil {
				s.serverError(w, r, "book narrator rail", err)
				return
			}
			narrated = page.Books
		}
		bookAbout(&v, d, series, seqs, narrated)
	case "chapters":
		var play *chapterPlay
		if canPlay && len(files) > 0 {
			play = &chapterPlay{book: book, files: files, p: prog}
		}
		bookChapters(&v, d, r.URL.Query().Get("all") == "1", play)
	case "bookmarks":
		if !listen {
			s.notFound(w, r)
			return
		}
		var bsheet templ.Component
		v.Bookmarks, bsheet, err = s.bookmarksPanel(r, d)
		if errors.Is(err, errBookmarkNotFound) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, "bookmarks", err)
			return
		}
		if bsheet != nil {
			sheet = bsheet
		}
	case "details":
		v.Rows = bookDetailsView(d, s.canManage(r))
	}
	s.pageStyled(w, r, status, d.Book.Title, "library", pages.Book(v), sheet, styles...)
}

// getPerson serves an author or a narrator page. A person with no visible listable book in this
// role is a 404, the same as an unknown id.
func (s *server) getPerson(role string) http.HandlerFunc {
	roleLabel, count := bookRoleAuthor, func(p library.Person) int { return p.AuthorOf }
	if role == library.RoleNarrator {
		roleLabel, count = bookRoleNarrator, func(p library.Person) int { return p.NarratorOf }
	}
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sc := s.scope(r)
		p, err := s.lib.Person(ctx, sc, r.PathValue("id"))
		if errors.Is(err, library.ErrNotFound) || (err == nil && count(p) == 0) {
			s.notFound(w, r)
			return
		}
		if err != nil {
			s.serverError(w, r, "person", err)
			return
		}
		q := library.ListQuery{Sort: library.SortTitle, Cursor: r.URL.Query().Get("cursor"), Limit: bookPersonPage}
		if role == library.RoleAuthor {
			q.AuthorID = p.ID
		} else {
			q.NarratorID = p.ID
		}
		page, err := s.lib.ListBooks(ctx, sc, q)
		if errors.Is(err, library.ErrInvalid) {
			s.errorPage(w, r, http.StatusBadRequest)
			return
		}
		if err != nil {
			s.serverError(w, r, "person books", err)
			return
		}
		ms, err := s.lib.PersonDuration(ctx, sc, p.ID, role)
		if err != nil {
			s.serverError(w, r, "person length", err)
			return
		}
		path := "/authors/"
		if role == library.RoleNarrator {
			path = "/narrators/"
		}
		path += p.ID
		v := pages.PersonView{
			ID: p.ID, Name: p.Name, Role: roleLabel, Description: p.Description,
			Count:    joinNonEmpty(" · ", countText(count(p), "1 book in your library", "%d books in your library"), hoursMinutesText(ms)),
			BackHref: "/libraries", BackLabel: bookBack, BooksTitle: bookBooksHeading,
		}
		for _, b := range page.Books {
			v.Cards = append(v.Cards, cardView(b))
		}
		if page.Next != "" {
			v.MoreHref, v.MoreLabel = path+"?cursor="+url.QueryEscape(page.Next), bookShowMore
		}
		if q.Cursor != "" {
			v.StartHref, v.StartLabel = path, bookBackToStart
		}
		s.pageStyled(w, r, http.StatusOK, p.Name, "library", pages.Person(v), nil, pages.BookStyles...)
	}
}

func (s *server) getSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := s.scope(r)
	sp, err := s.lib.SeriesInfo(ctx, sc, r.PathValue("id"))
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "series", err)
		return
	}
	seqs, err := s.lib.SeriesSequences(ctx, sc, sp.ID)
	if err != nil {
		s.serverError(w, r, "series places", err)
		return
	}
	var total int64
	v := pages.SeriesView{
		Overline: bookSeriesOverline + countText(len(sp.Books), "1 book", "%d books"), Name: sp.Name, Description: sp.Description,
		ByLabel: bookBy, ReadByLabel: bookReadBy, OrderTitle: bookSeriesOrder,
		BackHref: "/libraries", BackLabel: bookBack,
		Authors:   bookLinks(bookUniquePeople(sp.Books, false, 2), "authors"),
		Narrators: bookLinks(bookUniquePeople(sp.Books, true, 2), "narrators"),
	}
	for _, b := range sp.Books {
		total += b.DurationMs
		c := cardView(b)
		c.Byline = bookSeqLabel(seqs[b.ID])
		c.AriaLabel = joinNonEmpty(" ", c.Byline, b.Title)
		v.Rows = append(v.Rows, c)
	}
	if t := hoursMinutesText(total); t != "" {
		v.Total = t + bookInAll
	}
	s.pageStyled(w, r, http.StatusOK, sp.Name, "library", pages.Series(v), nil, pages.BookStyles...)
}
