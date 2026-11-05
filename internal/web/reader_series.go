package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// nextInSeries is the book after this one in its first series, among the books the person may see (SeriesInfo
// filters by the grants in SQL, so a hidden next book is skipped and its title never shows). It answers nil
// when the book has no series, is the last, or the series cannot be read.
func (s *server) nextInSeries(r *http.Request, bookID string) (*library.BookCard, library.BookSeriesLink, error) {
	ctx := r.Context()
	links, err := s.lib.BookSeries(ctx, bookID)
	if err != nil || len(links) == 0 {
		return nil, library.BookSeriesLink{}, err
	}
	sp, err := s.lib.SeriesInfo(ctx, s.scope(r), links[0].SeriesID)
	if err != nil {
		if errors.Is(err, library.ErrNotFound) {
			return nil, links[0], nil
		}
		return nil, links[0], err
	}
	at := slices.IndexFunc(sp.Books, func(c library.BookCard) bool { return c.ID == bookID })
	if at < 0 || at+1 >= len(sp.Books) {
		return nil, links[0], nil
	}
	return &sp.Books[at+1], links[0], nil
}

// readerNextBook gives the end-of-book block a quiet link to the next book of the series. The book page picks
// the format. A failure only leaves the link out: the end of a book never fails for it.
func (s *server) readerNextBook(r *http.Request, rc readerCtx, end *pages.ReaderEnd) {
	if end == nil {
		return
	}
	next, link, err := s.nextInSeries(r, rc.book.ID)
	if err != nil || next == nil {
		return
	}
	end.NextHref, end.NextLabel = "/books/"+next.ID, fmt.Sprintf(readerNextInSeries, link.Name, next.Title)
}
