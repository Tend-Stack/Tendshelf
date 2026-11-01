package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Search (P1b-6). One page, one address: /search?q=...&only=books|people|series. The books come from
// library.Search and the people and series from library.Suggest, both with the person's scope, so a
// count, a name or a "Did you mean" never comes from a book they cannot open. Typing swaps
// #search-results by htmx; Enter does the same as a plain form.

const (
	searchMaxRunes     = 200 // longest query used
	searchEverythingN  = 20  // books shown under "Everything"
	searchBooksN       = 200 // books shown under "Books" (the most Search returns)
	searchNamesN       = 20  // people and series asked of Suggest
	searchViewAll      = "everything"
	searchOnlyBooks    = "books"
	searchOnlyPeople   = "people"
	searchOnlySeries   = "series"
	searchKindBook     = "book"
	searchKindAuthor   = "author"
	searchKindNarrator = "narrator"
	searchKindSeries   = "series"
)

// searchQuery reads the address: q trimmed and cut to 200 runes, only one of the four views.
func searchQuery(r *http.Request) (q, only string) {
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(q) > searchMaxRunes {
		q = strings.TrimSpace(string([]rune(q)[:searchMaxRunes]))
	}
	only = r.URL.Query().Get("only")
	switch only {
	case searchOnlyBooks, searchOnlyPeople, searchOnlySeries:
	default:
		only = searchViewAll
	}
	return q, only
}

// searchHref is the address of a search; the defaults are left out.
func searchHref(q, only string) string {
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	if only != searchViewAll {
		v.Set("only", only)
	}
	if enc := v.Encode(); enc != "" {
		return "/search?" + enc
	}
	return "/search"
}

func (s *server) getSearch(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := s.scope(r)
	q, only := searchQuery(r)
	v := pages.SearchView{
		Q: q, Only: only, Heading: searchPageTitle, Placeholder: searchPlaceholder,
		FieldLabel: searchFieldLabel, ClearLabel: searchClearLabel, SegmentsName: searchShowOnlyLabel,
		Searched: q != "",
	}
	if q != "" {
		v.ClearHref = "/search"
	}
	for _, seg := range []struct{ only, label string }{
		{searchViewAll, searchEverything}, {searchOnlyBooks, searchBooks}, {searchOnlyPeople, searchPeople}, {searchOnlySeries, searchSeries},
	} {
		v.Segments = append(v.Segments, pages.SearchSegment{Label: seg.label, Href: searchHref(q, seg.only), Current: seg.only == only})
	}
	if !v.Searched {
		s.pageStyled(w, r, http.StatusOK, searchPageTitle, "search", pages.Search(v), nil, pages.SearchStyles...)
		return
	}

	limit := 1 // only the total and the suggestion are needed
	switch only {
	case searchViewAll:
		limit = searchEverythingN
	case searchOnlyBooks:
		limit = searchBooksN
	}
	res, err := s.lib.Search(ctx, sc, q, limit)
	if err != nil {
		s.serverError(w, r, "search", err)
		return
	}
	var people []pages.SearchPerson
	var series []pages.Link
	if only != searchOnlyBooks {
		names, err := s.lib.Suggest(ctx, sc, q, searchNamesN)
		if err != nil {
			s.serverError(w, r, "search names", err)
			return
		}
		for _, n := range names {
			switch n.Kind {
			case searchKindAuthor:
				people = append(people, pages.SearchPerson{ID: n.ID, Name: n.Label, Role: bookRoleAuthor, Href: "/authors/" + n.ID})
			case searchKindNarrator:
				people = append(people, pages.SearchPerson{ID: n.ID, Name: n.Label, Role: bookRoleNarrator, Href: "/narrators/" + n.ID})
			case searchKindSeries:
				series = append(series, pages.Link{Name: n.Label, Href: "/series/" + n.ID})
			}
		}
	}

	showBooks := only == searchViewAll || only == searchOnlyBooks
	if showBooks && limit > 1 {
		cards, err := s.lib.CardsByID(ctx, sc, res.IDs)
		if err != nil {
			s.serverError(w, r, "search cards", err)
			return
		}
		for _, c := range cards {
			v.Books = append(v.Books, cardView(c))
		}
		v.BooksHead = fmt.Sprintf("%s · %d", searchBooks, res.Total)
		if only == searchViewAll && res.Total > len(v.Books) {
			v.ShowAllHref, v.ShowAllLabel = searchHref(q, searchOnlyBooks), fmt.Sprintf(searchShowAll, res.Total)
		}
	}
	if only == searchViewAll || only == searchOnlyPeople {
		v.People = people
		v.PeopleHead = fmt.Sprintf("%s · %d", searchPeople, len(people))
	}
	if only == searchViewAll || only == searchOnlySeries {
		v.Series = series
		v.SeriesHead = fmt.Sprintf("%s · %d", searchSeries, len(series))
	}
	switch only {
	case searchOnlyPeople:
		v.Status = fmt.Sprintf(searchPeopleFound, len(people))
	case searchOnlySeries:
		v.Status = fmt.Sprintf(searchSeriesFound, len(series))
	default:
		v.Status = fmt.Sprintf(searchBooksFound, res.Total)
	}
	if len(v.Books) == 0 && len(v.People) == 0 && len(v.Series) == 0 {
		v.NothingFound = true
		v.EmptyTitle = fmt.Sprintf(searchEmptyTitle, q)
		v.EmptyBody = searchEmptyBody
		v.ClearButtonLabel = searchClearLabel
		if res.Suggestion != "" {
			v.DidYouMeanPrefix, v.DidYouMeanSuffix = searchDidYouMean, "?"
			v.Suggestion, v.SuggestionHref = res.Suggestion, searchHref(res.Suggestion, only)
		}
	}
	s.pageStyled(w, r, http.StatusOK, searchPageTitle, "search", pages.Search(v), nil, pages.SearchStyles...)
}
