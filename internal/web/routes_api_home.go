package web

import (
	"net/http"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// GET /api/v1/home: the car's start page in one request (docs/plans/car-home.md). It only calls the
// functions the web Home already uses, so every list filters by the person's grants in SQL; each book is
// checked once more for ListenBook, and a text-only book never appears (the car cannot play one).

const (
	carHomeContinue = 6
	carHomeSeries   = 4
	carHomeNew      = 8
	carHomeBecause  = 4
	carHomeEpisodes = 4
)

type apiHomeSeries struct {
	Book       apiBookCard `json:"book"`
	SeriesName string      `json:"seriesName"`
	Index      int         `json:"index"`
	Total      int         `json:"total"`
}

type apiHomeBecause struct {
	Seed  string        `json:"seed"`
	Books []apiBookCard `json:"books"`
}

type apiHome struct {
	Continue []apiContinueItem `json:"continue"`
	Series   []apiHomeSeries   `json:"series"`
	New      []apiBookCard     `json:"new"`
	Because  *apiHomeBecause   `json:"because"`
	Episodes []apiPodEpisode   `json:"episodes"`
}

func (s *server) getAPIHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	sc := s.scope(r)
	seen := map[string]bool{}
	keep := func(b library.BookCard) bool {
		if seen[b.ID] || b.Kind == library.BookText {
			return false
		}
		if !s.can.Can(ctx, u, share.ListenBook, share.Target{Kind: share.TargetBook, ID: b.ID, LibraryID: b.LibraryID}) {
			return false
		}
		seen[b.ID] = true
		return true
	}
	out := apiHome{
		Continue: []apiContinueItem{}, Series: []apiHomeSeries{}, New: []apiBookCard{}, Episodes: []apiPodEpisode{},
	}

	cont, err := s.lib.ContinueListening(ctx, sc, u.ID, carHomeContinue)
	if err != nil {
		s.serverError(w, r, "api home", err)
		return
	}
	for _, l := range cont {
		if keep(l.Book) {
			out.Continue = append(out.Continue, apiContinueItem{Book: apiCardOf(l.Book), PositionMs: l.PositionMs, DurationMs: l.DurationMs, UpdatedAt: l.UpdatedAt})
		}
	}

	series, err := s.lib.SeriesInProgress(ctx, sc, u.ID, carHomeSeries*2)
	if err != nil {
		s.serverError(w, r, "api home", err)
		return
	}
	for _, sn := range series {
		if len(out.Series) >= carHomeSeries {
			break
		}
		if sn.Started || !keep(sn.Next) {
			continue
		}
		out.Series = append(out.Series, apiHomeSeries{Book: apiCardOf(sn.Next), SeriesName: sn.Series.Name, Index: sn.NextIndex, Total: sn.Total})
	}

	page, err := s.lib.ListBooks(ctx, sc, library.ListQuery{
		Sort: library.SortAdded, Show: library.ShowNotStarted, Format: library.BookAudio,
		UserID: u.ID, Limit: carHomeNew + carHomeSeries,
	})
	if err != nil {
		s.serverError(w, r, "api home", err)
		return
	}
	for _, b := range page.Books {
		if len(out.New) >= carHomeNew {
			break
		}
		if keep(b) {
			out.New = append(out.New, apiCardOf(b))
		}
	}

	bec, found, err := s.lib.BecauseYouListened(ctx, sc, u.ID, carHomeBecause*2)
	if err != nil {
		s.serverError(w, r, "api home", err)
		return
	}
	if found {
		var books []apiBookCard
		for _, b := range bec.Books {
			if len(books) >= carHomeBecause {
				break
			}
			if keep(b) {
				books = append(books, apiCardOf(b))
			}
		}
		if len(books) > 0 {
			out.Because = &apiHomeBecause{Seed: bec.Seed.Title, Books: books}
		}
	}

	if s.podcastsOn(r) && s.can.Can(ctx, u, share.UsePodcasts, podServer) {
		eps, err := s.podcasts.NewEpisodes(ctx, u.ID, carHomeEpisodes, 0)
		if err != nil {
			s.serverError(w, r, "api home", err)
			return
		}
		for _, ep := range eps {
			out.Episodes = append(out.Episodes, apiPodEpisodeOf(ep))
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	api.Write(w, http.StatusOK, out)
}
