package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The book page and the pages of authors, narrators and series (P1b-6), and search. Everything is
// read with the person's scope: a book, person or series they cannot see answers 404 exactly like
// an id that does not exist, and counts and groups only cover what they may open.

func (s *server) registerBook(mux *http.ServeMux) {
	mux.HandleFunc("GET /books/{id}", s.getBook)
	mux.HandleFunc("GET /books/{id}/{tab}", s.getBook)
	mux.HandleFunc("GET /authors/{id}", s.getPerson(library.RoleAuthor))
	mux.HandleFunc("GET /narrators/{id}", s.getPerson(library.RoleNarrator))
	mux.HandleFunc("GET /series/{id}", s.getSeries)
	mux.HandleFunc("GET /search", s.getSearch)
}

func (s *server) getBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, tab := r.PathValue("id"), r.PathValue("tab")
	if tab == "" {
		tab = "about"
	}
	if !bookTabValid(tab) {
		s.notFound(w, r)
		return
	}
	if _, ok := s.loadBook(w, r, id, share.ViewBook); !ok {
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
		bookChapters(&v, d, r.URL.Query().Get("all") == "1")
	case "details":
		v.Rows = bookDetailsView(d, s.canManage(r))
	}
	s.pageStyled(w, r, http.StatusOK, d.Book.Title, "library", pages.Book(v), nil, pages.BookStyles...)
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
