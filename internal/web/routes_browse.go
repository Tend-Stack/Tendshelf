package web

import (
	"errors"
	"net/http"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
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
	browseMenuMax  = 30 // people or series listed in a chip menu
)

func (s *server) registerBrowse(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.getHome)
	mux.HandleFunc("GET /hx/home/hero", s.getHomeHero)
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
	var heroID string
	if v.Hero, heroID, err = s.homeHeroView(ctx, r); err != nil {
		s.serverError(w, r, "home hero", err)
		return
	}
	if v.Series, err = s.homeSeriesView(ctx, r, heroID); err != nil {
		s.serverError(w, r, "home series", err)
		return
	}
	if v.Because, err = s.homeBecauseView(ctx, r); err != nil {
		s.serverError(w, r, "home because", err)
		return
	}
	if v.Scan, err = s.activeScan(r, libs); err != nil {
		s.serverError(w, r, "home scan", err)
		return
	}
	if len(v.New) == 0 && v.Scan == nil {
		if v.Empty, err = s.emptyFor(r, ""); err != nil {
			s.serverError(w, r, "home empty", err)
			return
		}
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

// emptyFor is the empty state of Home and the Library when nothing is listed and no scan runs. For
// staff whose folders (in libraryID, or anywhere when it is "") held nothing playable, it names them
// instead of offering to add a folder.
func (s *server) emptyFor(r *http.Request, libraryID string) (*pages.EmptyView, error) {
	kind := s.emptyKind(r)
	if kind != "first-run" {
		return browseEmpty(kind, ""), nil
	}
	all, err := s.lib.FolderSummaries(r.Context(), s.scope(r))
	if err != nil {
		return nil, err
	}
	var folders []library.FolderSummary
	for _, f := range all {
		if libraryID == "" || f.LibraryID == libraryID {
			folders = append(folders, f)
		}
	}
	if len(folders) == 0 {
		return browseEmpty(kind, ""), nil
	}
	v := browseEmpty("no-books", "")
	v.Body = browseNoBooksBody(folders)
	return v, nil
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

func (s *server) listQuery(u users.User, q browseQuery, cur *library.LibrarySummary) library.ListQuery {
	from, to := lengthRange(q.Length)
	return library.ListQuery{LibraryID: curID(cur), Sort: q.Sort, Genres: q.Genres, Cursor: q.Cursor, Limit: browsePageSize,
		AuthorID: q.Author, NarratorID: q.Narrator, SeriesID: q.Series, Title: q.Title, MinDurationMs: from, MaxDurationMs: to,
		Show: q.Show, UserID: u.ID}
}

// libraryFacets are the choices the Author, Narrator and Series menus list.
type libraryFacets struct{ Authors, Narrators, Series []library.NamedFacet }

func (s *server) libraryFacets(r *http.Request, lq library.ListQuery) (f libraryFacets, err error) {
	ctx, sc := r.Context(), s.scope(r)
	if f.Authors, err = s.lib.PeopleFacet(ctx, sc, lq, library.RoleAuthor, browseMenuMax); err != nil {
		return f, err
	}
	if f.Narrators, err = s.lib.PeopleFacet(ctx, sc, lq, library.RoleNarrator, browseMenuMax); err != nil {
		return f, err
	}
	f.Series, err = s.lib.SeriesFacet(ctx, sc, lq, browseMenuMax)
	return f, err
}

func (s *server) getLibraryItems(w http.ResponseWriter, r *http.Request) {
	_, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	q := parseBrowseQuery(r)
	page, err := s.lib.ListBooks(r.Context(), s.scope(r), s.listQuery(currentUser(r), q, cur))
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
	page, err := s.lib.ListBooks(ctx, sc, s.listQuery(currentUser(r), q, cur))
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
	totals, err := s.lib.Totals(ctx, sc, curID(cur))
	if err != nil {
		s.serverError(w, r, "library totals", err)
		return
	}
	facets, err := s.libraryFacets(r, s.listQuery(currentUser(r), q, cur))
	if err != nil {
		s.serverError(w, r, "library facets", err)
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
	s.libraryFilters(&v, q, cur, facets)
	v.HeaderLine = browseHeaderLine(totals, s.canManage(r))
	v.Scan = scanStatus
	if len(v.Cards) == 0 && q.Cursor == "" && v.Scan == nil {
		if q.filtered() {
			v.Empty = browseEmpty("no-match", v.ClearHref)
		} else if v.Empty, err = s.emptyFor(r, curID(cur)); err != nil {
			s.serverError(w, r, "library empty", err)
			return
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
	if q.filtered() {
		v.Match = browseMatch(page.Total)
	}
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
	if q.filtered() {
		v.ClearHref = q.href(pagePath, func(q *browseQuery) { reset(q); q.clearFilters() })
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
			return q.href(path, func(q *browseQuery) { reset(q); q.clearFilters() })
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

// libraryFilters adds the Author, Narrator, Series and Length menus and the values the forms carry.
// Every choice is an address; a menu's "Any ..." link drops its own filter and keeps the others.
func (s *server) libraryFilters(v *pages.LibraryView, q browseQuery, cur *library.LibrarySummary, f libraryFacets) {
	pagePath, _ := browsePaths(cur)
	set := func(apply func(*browseQuery)) string {
		return q.href(pagePath, func(q *browseQuery) { q.Cursor, q.Sheet = "", false; apply(q) })
	}
	people := func(label, chip, any string, opts []library.NamedFacet, chosen string, assign func(*browseQuery, string)) pages.FilterMenu {
		m := pages.FilterMenu{Label: label, Chip: label, AnyLabel: any, Counts: true, On: chosen != "",
			AnyHref: set(func(q *browseQuery) { assign(q, "") })}
		for _, o := range opts {
			id := o.ID
			m.Options = append(m.Options, pages.FilterOption{Name: o.Name, Count: o.Count, On: id == chosen,
				Href: set(func(q *browseQuery) { assign(q, id) })})
			if id == chosen {
				m.Chip = chip + ": " + o.Name
			}
		}
		return m
	}
	add := func(m pages.FilterMenu) {
		if m.On || len(m.Options) > 0 {
			v.Menus = append(v.Menus, m)
		}
	}
	add(people("Author", "Author", "Any author", f.Authors, q.Author, func(q *browseQuery, id string) { q.Author = id }))
	add(people("Narrator", "Narrator", "Any narrator", f.Narrators, q.Narrator, func(q *browseQuery, id string) { q.Narrator = id }))
	add(people("Series", "Series", "Any series", f.Series, q.Series, func(q *browseQuery, id string) { q.Series = id }))

	lm := pages.FilterMenu{Label: "Length", Chip: "Length", AnyLabel: "Any length", On: q.Length != "", End: true,
		AnyHref: set(func(q *browseQuery) { q.Length = "" })}
	for _, o := range browseLengthOptions {
		val := o.Value
		lm.Options = append(lm.Options, pages.FilterOption{Name: o.Label, Value: val, On: val == q.Length,
			Href: set(func(q *browseQuery) { q.Length = val })})
		if val == q.Length {
			lm.Chip = "Length: " + o.Label
		}
	}
	v.LengthMenu = lm

	sm := pages.FilterMenu{Label: showLabel, Chip: showLabel, AnyLabel: showAny, On: q.Show != "", End: true,
		AnyHref: set(func(q *browseQuery) { q.Show = "" })}
	for _, o := range browseShowOptions {
		val := o.Value
		sm.Options = append(sm.Options, pages.FilterOption{Name: o.Label, Value: val, On: val == q.Show,
			Href: set(func(q *browseQuery) { q.Show = val })})
		if val == q.Show {
			sm.Chip = showLabel + ": " + o.Label
		}
	}
	v.ShowMenu, v.ShowValue = sm, q.Show

	v.TitleFilter, v.LengthValue = q.Title, q.Length
	for _, h := range [...]pages.HiddenField{{Name: "author", Value: q.Author}, {Name: "narrator", Value: q.Narrator}, {Name: "series", Value: q.Series}} {
		if h.Value != "" {
			v.Hidden = append(v.Hidden, h)
		}
	}
}
