package web

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Profile puts the person's books first (decision 0021): the hero, "Also in progress" and "Finished
// lately". Every list is a scoped library read of the signed-in person's own places.

// NEW copy (decision 0021, voice BRAND.md). "See all" is a literal, as on Home.
const (
	// The headings "Also in progress", "Finished lately", "Account" and "No shelves yet" are literals in
	// profile.templ, as "Your server" is.
	profileEmptyTitle = "Nothing in progress"
	profileEmptyBody  = "Start a book and it will wait for you here."
	rowResume         = "Resume %s"
	rowContinueRead   = "Continue reading %s"
)

const (
	// profileBooks is how many books under way Profile asks for: the hero and eight more.
	profileBooks = 9
	// profileShowAll is the count above which "See all" appears (the phone shows the hero and five rows).
	profileShowAll   = 6
	profileFinishedN = 6
	profileShelvesN  = 3
)

// profileBooksView builds the hero, the other books under way and the lately finished. The hero is the
// first book that can be drawn; each other book gets one resume control at most: the format used
// last, or the other format when the person lost the right to that one.
func (s *server) profileBooksView(r *http.Request, speed float64) (pages.ProfileBooksView, error) {
	ctx := r.Context()
	u := currentUser(r)
	var v pages.ProfileBooksView
	ls, err := s.lib.ContinueBooks(ctx, s.scope(r), u.ID, profileBooks)
	if err != nil {
		return v, err
	}
	if len(ls) > profileShowAll {
		v.SeeAllURL = "/libraries?show=" + library.ShowUnfinished
	}
	for len(ls) > 0 && v.Hero == nil {
		hero, _, err := s.heroFor(ctx, r, ls[0])
		if err != nil {
			return v, err
		}
		v.Hero = hero
		ls = ls[1:]
	}
	if v.Hero == nil {
		v.Empty = &pages.ListeningEmpty{Title: profileEmptyTitle, Body: profileEmptyBody, Action: listenEmptyAction, Href: "/libraries"}
	}
	if len(ls) > 0 {
		if v.Also, err = s.profileTiles(r, ls, speed); err != nil {
			return v, err
		}
	}
	fin, err := s.lib.FinishedLately(ctx, s.scope(r), u.ID, profileFinishedN)
	if err != nil {
		return v, err
	}
	cards := make([]library.BookCard, len(fin))
	for i, f := range fin {
		cards[i] = f.Book
		v.Finished = append(v.Finished, cardView(f.Book))
	}
	if err := s.withCardProgress(ctx, u.ID, cards, v.Finished); err != nil {
		return v, err
	}
	if len(fin) > 0 {
		v.FinishedURL = "/libraries?show=" + library.ShowFinished
	}
	return v, nil
}

// profileTiles draws the books under way after the hero. A book that vanished is skipped.
func (s *server) profileTiles(r *http.Request, ls []library.Listening, speed float64) ([]pages.ProfileBook, error) {
	ctx := r.Context()
	u := currentUser(r)
	ids := make([]string, len(ls))
	for i, l := range ls {
		ids[i] = l.Book.ID
	}
	prog, err := s.progress.ForBooks(ctx, u.ID, ids)
	if err != nil {
		return nil, err
	}
	reads, err := s.progress.ReadingForBooks(ctx, u.ID, ids)
	if err != nil {
		return nil, err
	}
	var out []pages.ProfileBook
	var cards []library.BookCard
	for _, l := range ls {
		book, err := s.lib.Book(ctx, l.Book.ID)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var p *progress.Progress
		if x, ok := prog[l.Book.ID]; ok {
			p = &x
		}
		var rp *progress.ReadingPosition
		if x, ok := reads[l.Book.ID]; ok {
			rp = &x
		}
		link, err := s.playLinkFor(ctx, u, book, p)
		if err != nil {
			return nil, err
		}
		mayRead, err := s.mayReadBook(ctx, u, book)
		if err != nil {
			return nil, err
		}
		t := pages.ProfileBook{Card: cardView(l.Book)}
		switch {
		case l.Format == library.BookText && mayRead, l.Format != library.BookText && link == nil && mayRead:
			t.Read = readButton(book, rp, false)
			t.Read.Round, t.Read.AriaLabel = true, fmt.Sprintf(rowContinueRead, l.Book.Title)
		case link != nil:
			t.Play = link.button(fmt.Sprintf(rowResume, l.Book.Title), true)
		}
		t.Card.Left = continueLeft(l, speed)
		out = append(out, t)
		cards = append(cards, l.Book)
	}
	views := make([]components.CardView, len(out))
	for i := range out {
		views[i] = out[i].Card
	}
	if err := s.withCardProgress(ctx, u.ID, cards, views); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Card = views[i]
	}
	return out, nil
}

// continueLeft is the line under a book in progress: the chapter and percent of a text, the time left of
// an audiobook, nothing when the length is not known. Profile's tiles and Home's row share it.
func continueLeft(l library.Listening, speed float64) string {
	switch {
	case l.Format == library.BookText:
		return fmt.Sprintf(readWhere, l.ChapterIdx, l.PercentBP/100)
	case l.DurationMs > 0:
		return fmt.Sprintf(bookWhereShortBook, bookLeftText(l.PositionMs, l.DurationMs, speed))
	}
	return ""
}

// profileShelvesView is the first shelves of the person, drawn with the Shelves page's own row.
func (s *server) profileShelvesView(r *http.Request) ([]pages.ShelfRowView, error) {
	list, err := s.lib.Shelves(r.Context(), s.scope(r), currentUser(r).ID)
	if err != nil {
		return nil, err
	}
	var rows []pages.ShelfRowView
	for _, sh := range list[:min(len(list), profileShelvesN)] {
		rows = append(rows, shelfRow(sh))
	}
	return rows, nil
}
