package web

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The Home rows for a person who has listened: the hero, the series in progress and "Because you
// listened to". Every read goes through the scoped library reads, so a book the person cannot see
// never appears, and the rows are drawn into the page (Home is complete without JavaScript).

const (
	homeSeriesRows  = 3
	homeBecauseRows = 12
	homeSharedCards = 3
)

// homeHeroView builds the hero for the person's newest unfinished book, or nil when there is none.
// The play link is left out when the person may not play the book.
func (s *server) homeHeroView(ctx context.Context, r *http.Request) (*pages.HomeHeroView, string, error) {
	u := currentUser(r)
	ls, err := s.lib.ContinueListening(ctx, s.scope(r), u.ID, 1)
	if err != nil || len(ls) == 0 {
		return nil, "", err
	}
	l := ls[0]
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		return nil, "", err
	}
	chs, err := s.lib.Chapters(ctx, l.Book.ID)
	if err != nil {
		return nil, "", err
	}
	v := &pages.HomeHeroView{
		Cover: cardView(l.Book), Eyebrow: homeContinue, Title: l.Book.Title, Byline: bylineOf(l.Book.Authors),
		BookHref: "/books/" + l.Book.ID, ChaptersHref: "/books/" + l.Book.ID + "/chapters", ChaptersLabel: homeChapters,
	}
	if n := bylineOf(l.Book.Narrators); n != "" {
		v.Narrator = fmt.Sprintf(homeReadBy, n)
	}
	lr := listenRulerFor(chs, l.PositionMs, l.DurationMs, set.Speed)
	v.RulerLabel, v.RulerNow, v.Ticks, v.Where = lr.Label, lr.Now, lr.Ticks, lr.Where
	book, err := s.lib.Book(ctx, l.Book.ID)
	if errors.Is(err, library.ErrNotFound) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	p := &progress.Progress{PositionMs: l.PositionMs}
	if link, err := s.playLinkFor(ctx, u, book, p); err != nil {
		return nil, "", err
	} else if link != nil {
		v.Play = link.button("", false)
		v.Play.ShowsPlayer = true
	}
	return v, l.Book.ID, nil
}

// currentChapter is the index of the chapter that holds pos, or -1 without chapters.
func currentChapter(chs []library.Chapter, pos int64) int {
	cur := -1
	for i, c := range chs {
		if c.StartMs <= pos {
			cur = i
		}
	}
	if cur < 0 && len(chs) > 0 {
		cur = 0
	}
	return cur
}

// percentText is part/whole as a 0 to 100 percentage with one decimal ("62.4"), for data-ts-value.
func percentText(part, whole int64) string {
	if whole <= 0 {
		return "0"
	}
	v := math.Min(100, math.Max(0, float64(part)*100/float64(whole)))
	return strconv.FormatFloat(v, 'f', 1, 64)
}

// homeSeriesView builds "Your series in progress". The row of the hero's book carries no play link:
// the hero is the one place that resumes it.
func (s *server) homeSeriesView(ctx context.Context, r *http.Request, heroID string) (pages.HomeSeriesView, error) {
	u := currentUser(r)
	rows, err := s.lib.SeriesInProgress(ctx, s.scope(r), u.ID, homeSeriesRows)
	if err != nil || len(rows) == 0 {
		return pages.HomeSeriesView{}, err
	}
	ids := make([]string, len(rows))
	for i, sn := range rows {
		ids[i] = sn.Next.ID
	}
	prog, err := s.progress.ForBooks(ctx, u.ID, ids)
	if err != nil {
		return pages.HomeSeriesView{}, err
	}
	v := pages.HomeSeriesView{Title: homeSeriesTitle}
	for _, sn := range rows {
		started := sn.Started && sn.PositionMs > 0
		row := pages.HomeSeriesRowView{Cover: cardView(sn.Next), Name: sn.Series.Name, Href: "/series/" + sn.Series.ID}
		if started {
			pct := 0
			if sn.Next.DurationMs > 0 {
				pct = int(math.Round(float64(sn.PositionMs) * 100 / float64(sn.Next.DurationMs)))
			}
			row.Meta = fmt.Sprintf(homeSeriesLine, sn.NextIndex, sn.Total, sn.Next.Title, min(max(pct, 0), 100))
		} else {
			row.Meta = fmt.Sprintf(homeSeriesNext, sn.NextIndex, sn.Total, sn.Next.Title)
		}
		if sn.Next.ID != heroID {
			book, err := s.lib.Book(ctx, sn.Next.ID)
			if err != nil && !errors.Is(err, library.ErrNotFound) {
				return pages.HomeSeriesView{}, err
			}
			if err == nil {
				var p *progress.Progress
				if x, ok := prog[sn.Next.ID]; ok {
					p = &x
				}
				link, err := s.playLinkFor(ctx, u, book, p)
				if err != nil {
					return pages.HomeSeriesView{}, err
				}
				if link != nil {
					label := homeSeriesStart
					if started {
						label = homeSeriesCont
					}
					row.Play = link.button(fmt.Sprintf(label, sn.Series.Name), true)
				}
			}
		}
		v.Rows = append(v.Rows, row)
	}
	return v, nil
}

// homeBecauseView builds "Because you listened to {seed}", or nil when there is nothing to suggest.
func (s *server) homeBecauseView(ctx context.Context, r *http.Request) (*pages.HomeBecauseView, error) {
	b, ok, err := s.lib.BecauseYouListened(ctx, s.scope(r), currentUser(r).ID, homeBecauseRows)
	if err != nil || !ok {
		return nil, err
	}
	v := &pages.HomeBecauseView{Title: homeBecause, Seed: b.Seed.Title}
	for _, c := range b.Books {
		v.Cards = append(v.Cards, cardView(c))
	}
	return v, nil
}

// getHomeHero answers GET /hx/home/hero: the hero card in its wrapper (an empty wrapper when nothing
// is in progress). Home re-reads it when progress is saved.
func (s *server) getHomeHero(w http.ResponseWriter, r *http.Request) {
	v, _, err := s.homeHeroView(r.Context(), r)
	if err != nil {
		s.serverError(w, r, "home hero", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.HomeHero(v))
}

// homeSharedView builds "Shared with you": the newest three visible shared books and who shared
// them. It returns nil when nothing visible is shared, and the total number of shared books.
func (s *server) homeSharedView(ctx context.Context, r *http.Request) (*pages.HomeSharedView, int, error) {
	row, err := s.lib.SharedWithYou(ctx, s.scope(r), homeSharedCards)
	if err != nil || row.Total == 0 || len(row.Books) == 0 {
		return nil, 0, err
	}
	v := &pages.HomeSharedView{Title: homeShared, From: sharedFrom(row.Sharers), SeeAllURL: "/libraries?show=" + library.ShowShared}
	for _, b := range row.Books {
		v.Cards = append(v.Cards, cardView(b.Book))
	}
	return v, row.Total, nil
}

// sharedFrom is the caption after "Shared with you": "from A", "from A and B" or
// "from A, B and 3 others", newest sharer first. No names gives "".
func sharedFrom(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return fmt.Sprintf(homeSharedFrom, names[0])
	case 2:
		return fmt.Sprintf(homeSharedFrom, fmt.Sprintf(homeSharedTwo, names[0], names[1]))
	}
	if len(names) == 3 {
		return fmt.Sprintf(homeSharedFrom, fmt.Sprintf(homeSharedOne, names[0], names[1]))
	}
	return fmt.Sprintf(homeSharedFrom, fmt.Sprintf(homeSharedMany, names[0], names[1], len(names)-2))
}
