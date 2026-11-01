package web

import (
	"errors"
	"net/http"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Home and the Library (P1b-5). Every list is library.ListBooks with the person's scope, so no book
// they cannot open appears in a row, a grid, a count, a genre chip or a library chip. Filters, sort,
// view and page position live in the address; the infinite scroll is a link first (a no-JavaScript
// "Show more") and a fragment second.

const (
	homeShelfSize  = 12
	browseTopChips = 3  // genre chips in the row; the sheet lists them all
	browseSheetMax = 40 // genres listed in the sheet
)

func (s *server) registerBrowse(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.getHome)
	mux.HandleFunc("GET /libraries", s.getLibrary)
	mux.HandleFunc("GET /libraries/{id}", s.getLibrary)
	mux.HandleFunc("GET /hx/libraries/items", s.getLibraryItems)
	mux.HandleFunc("GET /hx/libraries/{id}/items", s.getLibraryItems)
}

// canManage reports whether the person sees scan banners and the "Add a folder" action.
func (s *server) canManage(r *http.Request) bool {
	return s.can.Can(r.Context(), currentUser(r), share.ManageLibraries, share.Target{Kind: share.TargetServer})
}

// activeScan returns the status of a running scan of one of the libraries, or nil.
func (s *server) activeScan(r *http.Request, libs []library.LibrarySummary) (*components.ScanStatusView, error) {
	if !s.canManage(r) {
		return nil, nil
	}
	for _, l := range libs {
		j, ok, err := s.jobs.ActiveFor(r.Context(), scan.KindScan, "library_id", l.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sv, err := s.scanView(r.Context(), r, j, l.ID, false)
		if err != nil {
			return nil, err
		}
		return &sv, nil
	}
	return nil, nil
}

func (s *server) getHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	sc := s.scope(r)
	libs, err := s.lib.VisibleLibraries(ctx, sc)
	if err != nil {
		s.serverError(w, r, "home libraries", err)
		return
	}
	page, err := s.lib.ListBooks(ctx, sc, library.ListQuery{Sort: library.SortAdded, Limit: homeShelfSize})
	if err != nil {
		s.serverError(w, r, "home books", err)
		return
	}
	v := pages.HomeView{Greeting: browseGreetingFor(currentUser(r).DisplayName), SeeAllURL: "/libraries?sort=added"}
	for _, b := range page.Books {
		v.New = append(v.New, cardView(b))
	}
	if v.Scan, err = s.activeScan(r, libs); err != nil {
		s.serverError(w, r, "home scan", err)
		return
	}
	if len(v.New) == 0 && v.Scan == nil {
		v.Empty = browseEmpty(s.emptyKind(r), "")
	}
	s.pageStyled(w, r, http.StatusOK, "Home", "home", pages.Home(v), nil, pages.BrowseStyles...)
}

// emptyKind says which empty state a person with no books gets.
func (s *server) emptyKind(r *http.Request) string {
	if s.canManage(r) {
		return "first-run"
	}
	return "nothing-shared"
}

// browseTarget resolves the library the address names ("" for all of them) among the libraries the
// person may see. An unknown or hidden library answers 404 and ok is false.
func (s *server) browseTarget(w http.ResponseWriter, r *http.Request) (libs []library.LibrarySummary, cur *library.LibrarySummary, ok bool) {
	libs, err := s.lib.VisibleLibraries(r.Context(), s.scope(r))
	if err != nil {
		s.serverError(w, r, "libraries", err)
		return nil, nil, false
	}
	id := r.PathValue("id")
	if id == "" {
		return libs, nil, true
	}
	for i := range libs {
		if libs[i].ID == id {
			return libs, &libs[i], true
		}
	}
	s.notFound(w, r)
	return nil, nil, false
}

// browsePaths are the page address and the fragment address of the list.
func browsePaths(cur *library.LibrarySummary) (page, fragment string) {
	if cur == nil {
		return "/libraries", "/hx/libraries/items"
	}
	return "/libraries/" + cur.ID, "/hx/libraries/" + cur.ID + "/items"
}

func curID(cur *library.LibrarySummary) string {
	if cur == nil {
		return ""
	}
	return cur.ID
}

// browseItems fills in the part of the view the fragment shares with the page: the books of this
// page, the view, and the links to the next page.
func browseItems(q browseQuery, cur *library.LibrarySummary, page library.BookPage) pages.LibraryView {
	pagePath, hxPath := browsePaths(cur)
	v := pages.LibraryView{View: q.View}
	for _, b := range page.Books {
		v.Cards = append(v.Cards, cardView(b))
	}
	if page.Next != "" {
		next := func(q *browseQuery) { q.Cursor, q.Sheet = page.Next, false }
		v.MoreURL, v.MoreHX = q.href(pagePath, next), q.href(hxPath, next)
	}
	return v
}

func (s *server) listQuery(q browseQuery, cur *library.LibrarySummary) library.ListQuery {
	return library.ListQuery{LibraryID: curID(cur), Sort: q.Sort, Genres: q.Genres, Cursor: q.Cursor, Limit: browsePageSize}
}

func (s *server) getLibraryItems(w http.ResponseWriter, r *http.Request) {
	_, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	q := parseBrowseQuery(r)
	page, err := s.lib.ListBooks(r.Context(), s.scope(r), s.listQuery(q, cur))
	if errors.Is(err, library.ErrInvalid) {
		s.fragment(w, r, http.StatusBadRequest, components.ErrorToast(browseBadCursorToast))
		return
	}
	if err != nil {
		s.serverError(w, r, "library items", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.LibraryItems(browseItems(q, cur, page)))
}

func (s *server) getLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	libs, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	q := parseBrowseQuery(r)
	sc := s.scope(r)
	page, err := s.lib.ListBooks(ctx, sc, s.listQuery(q, cur))
	if errors.Is(err, library.ErrInvalid) {
		s.errorPage(w, r, http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, "library books", err)
		return
	}
	genres, err := s.lib.Genres(ctx, sc, curID(cur))
	if err != nil {
		s.serverError(w, r, "library genres", err)
		return
	}
	shown := libs
	if cur != nil {
		shown = []library.LibrarySummary{*cur}
	}
	scanStatus, err := s.activeScan(r, shown)
	if err != nil {
		s.serverError(w, r, "library scan", err)
		return
	}
	v := s.libraryView(q, libs, cur, genres, page)
	v.Scan = scanStatus
	if len(v.Cards) == 0 && q.Cursor == "" && v.Scan == nil {
		if len(q.Genres) > 0 {
			v.Empty = browseEmpty("no-match", v.ClearHref)
		} else {
			v.Empty = browseEmpty(s.emptyKind(r), "")
		}
	}
	title := "Library"
	if cur != nil {
		title = cur.Name
	}
	var sheet templ.Component
	if q.Sheet {
		sheet = pages.LibraryFilterSheet(v)
	}
	s.pageStyled(w, r, http.StatusOK, title, "library", pages.Library(v), sheet, pages.BrowseStyles...)
}

// libraryView builds the whole page view: the list part plus the title, chips, sort and sheet links.
func (s *server) libraryView(q browseQuery, libs []library.LibrarySummary, cur *library.LibrarySummary, genres []library.Facet, page library.BookPage) pages.LibraryView {
	pagePath, _ := browsePaths(cur)
	v := browseItems(q, cur, page)
	v.Title = browseLibraryTitle
	if cur != nil {
		v.Title = cur.Name
	}
	v.FormAction = pagePath
	v.Count = browseCount(page.Total)
	v.SortLabel = browseSortLabel(q.Sort)
	for _, o := range browseSortLabels {
		v.SortOptions = append(v.SortOptions, pages.SortOption{Value: string(o.Sort), Label: o.Label, On: o.Sort == q.Sort})
	}
	reset := func(q *browseQuery) { q.Cursor, q.Sheet = "", false }
	v.GridHref = q.href(pagePath, func(q *browseQuery) { reset(q); q.View = "grid" })
	v.ListHref = q.href(pagePath, func(q *browseQuery) { reset(q); q.View = "list" })
	v.SheetHref = q.href(pagePath, func(q *browseQuery) { q.Sheet = true })
	v.CloseHref = q.href(pagePath, func(q *browseQuery) { q.Sheet = false })
	v.SelectedGenres = q.Genres
	if len(q.Genres) > 0 {
		v.ClearHref = q.href(pagePath, func(q *browseQuery) { reset(q); q.Genres = nil })
	}
	if q.Cursor != "" {
		v.StartHref = q.href(pagePath, reset)
	}

	if len(libs) > 1 {
		libHref := func(id string) string {
			path := "/libraries"
			if id != "" {
				path += "/" + id
			}
			return q.href(path, func(q *browseQuery) { reset(q); q.Genres = nil })
		}
		v.Libraries = append(v.Libraries, pages.LibChip{Name: "All", Href: libHref(""), Current: cur == nil})
		for _, l := range libs {
			v.Libraries = append(v.Libraries, pages.LibChip{Name: l.Name, Href: libHref(l.ID), Current: cur != nil && cur.ID == l.ID})
		}
	}

	toggle := func(g string) string {
		return q.href(pagePath, func(q *browseQuery) {
			reset(q)
			if q.hasGenre(g) {
				q.without(g)
			} else if len(q.Genres) < browseMaxGenres {
				q.Genres = append(q.Genres, g)
			}
		})
	}
	chip := func(g string) pages.GenreChip { return pages.GenreChip{Name: g, Href: toggle(g), On: q.hasGenre(g)} }
	listed := map[string]bool{}
	for i, f := range genres {
		if i < browseTopChips {
			v.Genres = append(v.Genres, chip(f.Value))
			listed[f.Value] = true
		}
	}
	for _, g := range q.Genres { // a chosen genre stays in the row, so it can be taken off
		if !listed[g] {
			v.Genres = append(v.Genres, chip(g))
			listed[g] = true
		}
	}
	inSheet := map[string]bool{}
	for _, f := range genres {
		if len(v.AllGenres) == browseSheetMax {
			break
		}
		v.AllGenres = append(v.AllGenres, chip(f.Value))
		inSheet[f.Value] = true
	}
	for _, g := range q.Genres {
		if !inSheet[g] {
			v.AllGenres = append(v.AllGenres, chip(g))
		}
	}
	return v
}
