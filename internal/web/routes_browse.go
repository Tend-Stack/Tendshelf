package web

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

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
	homeShelfMax      = 40 // newest books sent for the row; shell.js shows as many as fit
	browseCategoryMax = 40 // genres listed in the Category menu
	browseMenuMax     = 30 // people or series listed in a chip menu
)

func (s *server) registerBrowse(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.getHome)
	mux.HandleFunc("GET /hx/home/hero", s.getHomeHero)
	mux.HandleFunc("GET /libraries", s.getLibrary)
	mux.HandleFunc("GET /libraries/{id}", s.getLibrary)
	mux.HandleFunc("GET /hx/libraries/items", s.getLibraryItems)
	mux.HandleFunc("GET /hx/libraries/{id}/items", s.getLibraryItems)
	mux.HandleFunc("GET /hx/library-facets/{kind}", s.getLibraryFacet)
	mux.HandleFunc("GET /hx/library-facets/{id}/{kind}", s.getLibraryFacet)
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
	// "New on the shelf" leaves out what the person has already finished.
	page, err := s.lib.ListBooks(ctx, sc, library.ListQuery{Sort: library.SortAdded, Limit: homeShelfMax,
		Show: library.ShowNotFinished, UserID: currentUser(r).ID})
	if err != nil {
		s.serverError(w, r, "home books", err)
		return
	}
	v := pages.HomeView{Greeting: browseGreetingFor(currentUser(r).DisplayName), SeeAllURL: "/libraries?sort=added"}
	for _, b := range page.Books {
		v.New = append(v.New, cardView(b))
	}
	_, textSize, _, _ := s.lookFor(r)
	v.Large = textSize == "large"
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
	var sharedTotal int
	if v.Shared, sharedTotal, err = s.homeSharedView(ctx, r); err != nil {
		s.serverError(w, r, "home shared", err)
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
	// Everything the person can see was shared with them (a guest with a shelf): "New on the shelf"
	// would repeat the shared row.
	if v.Shared != nil && sharedTotal >= page.Total {
		v.New = nil
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
		next := func(q *browseQuery) { q.Cursor = page.Next }
		v.MoreURL, v.MoreHX = q.href(pagePath, next), q.href(hxPath, next)
	}
	return v
}

func (s *server) listQuery(u users.User, q browseQuery, cur *library.LibrarySummary) library.ListQuery {
	from, to := lengthRange(q.Length)
	return library.ListQuery{LibraryID: curID(cur), Sort: q.Sort, Genres: q.Genres, Cursor: q.Cursor, Limit: browsePageSize,
		AuthorID: q.Author, NarratorID: q.Narrator, SeriesID: q.Series, Title: q.Title, MinDurationMs: from, MaxDurationMs: to,
		Format: q.Format, Show: q.Show, UserID: u.ID, AddedBy: addedByFilter(u, q.Added)}
}

// addedByFilter is the "Added by" choice the person may use: any id for an owner or admin, only their own for
// everyone else (decision 0029). Anything else is dropped, so the filter never tells whether another
// person added books.
func addedByFilter(u users.User, id string) string {
	switch {
	case id == "":
		return ""
	case u.Role == users.RoleOwner || u.Role == users.RoleAdmin || id == u.ID:
		return id
	}
	return ""
}

// libraryFacets are the choices the Author, Narrator and Series menus list.
type libraryFacets struct {
	Authors, Narrators, Series []library.NamedFacet
	// Added are the "Added by" choices (decision 0029): the people with books in view for an owner or admin,
	// only "You" for a contributor who has a visible book of their own, nothing for anyone else.
	// AddedCounts says whether the choices carry book counts.
	Added       []library.NamedFacet
	AddedCounts bool
}

func (s *server) libraryFacets(r *http.Request, lq library.ListQuery) (f libraryFacets, err error) {
	ctx, sc := r.Context(), s.scope(r)
	if f.Authors, err = s.lib.PeopleFacet(ctx, sc, lq, library.RoleAuthor, browseMenuMax); err != nil {
		return f, err
	}
	if f.Narrators, err = s.lib.PeopleFacet(ctx, sc, lq, library.RoleNarrator, browseMenuMax); err != nil {
		return f, err
	}
	if f.Series, err = s.lib.SeriesFacet(ctx, sc, lq, browseMenuMax); err != nil {
		return f, err
	}
	f.Added, f.AddedCounts, err = s.addedFacet(r, lq)
	return f, err
}

// addedFacet fills the "Added by" choices for the signed-in person. The own choice is always named "You".
// A contributor learns only whether one of their own books is in view, never who else added books.
func (s *server) addedFacet(r *http.Request, lq library.ListQuery) ([]library.NamedFacet, bool, error) {
	ctx, u := r.Context(), currentUser(r)
	var out []library.NamedFacet
	counts := false
	switch {
	case u.Role == users.RoleOwner || u.Role == users.RoleAdmin:
		list, err := s.lib.AddedByFacet(ctx, s.scope(r), lq, browseMenuMax)
		if err != nil {
			return nil, false, err
		}
		out, counts = list, true
	case u.Contributor():
		page, err := s.lib.ListBooks(ctx, s.scope(r), library.ListQuery{LibraryID: lq.LibraryID, UserID: u.ID, AddedBy: u.ID, Limit: 1})
		if err != nil {
			return nil, false, err
		}
		if len(page.Books) > 0 {
			out = []library.NamedFacet{{ID: u.ID, Name: u.ID}}
		}
	}
	if lq.AddedBy != "" && !slices.ContainsFunc(out, func(o library.NamedFacet) bool { return o.ID == lq.AddedBy }) {
		// The chosen person stays listed so the choice can be taken off, even with no book in view.
		if t, err := s.users.ByID(ctx, lq.AddedBy); err == nil {
			out = append(out, library.NamedFacet{ID: t.ID, Name: personName(t)})
		}
	}
	for i := range out {
		if out[i].ID == u.ID {
			out[i].Name = libAddedYou
		}
	}
	return out, counts, nil
}

func (s *server) getLibraryItems(w http.ResponseWriter, r *http.Request) {
	_, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	q := parseBrowseQuery(r)
	q.Added = addedByFilter(currentUser(r), q.Added)
	page, err := s.lib.ListBooks(r.Context(), s.scope(r), s.listQuery(currentUser(r), q, cur))
	if errors.Is(err, library.ErrInvalid) {
		s.fragment(w, r, http.StatusBadRequest, components.ErrorToast(browseBadCursorToast))
		return
	}
	if err != nil {
		s.serverError(w, r, "library items", err)
		return
	}
	v := browseItems(q, cur, page)
	if err := s.withCardProgress(r.Context(), currentUser(r).ID, page.Books, v.Cards); err != nil {
		s.serverError(w, r, "library items progress", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.LibraryItems(v))
}

func (s *server) getLibrary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	libs, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	q := parseBrowseQuery(r)
	q.Added = addedByFilter(currentUser(r), q.Added)
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
	v := s.libraryView(q, libs, cur, page)
	if err := s.withCardProgress(r.Context(), currentUser(r).ID, page.Books, v.Cards); err != nil {
		s.serverError(w, r, "library progress", err)
		return
	}
	hasText, err := s.hasTextBooks(r, cur)
	if err != nil {
		s.serverError(w, r, "library formats", err)
		return
	}
	s.libraryFilters(&v, q, cur, facets, genres, hasText)
	v.HeaderLine = browseHeaderLine(totals, s.canManage(r), q.filtered(), page.Total)
	if v.AddBooksHref = s.addBooksHref(r, cur, libs); v.AddBooksHref != "" {
		v.AddBooksLabel = uploadTitle
	}
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
	s.pageStyled(w, r, http.StatusOK, title, "library", pages.Library(v), nil, pages.BrowseStyles...)
}

// libraryView builds the whole page view: the list part plus the title, chips and view links.
func (s *server) libraryView(q browseQuery, libs []library.LibrarySummary, cur *library.LibrarySummary, page library.BookPage) pages.LibraryView {
	pagePath, _ := browsePaths(cur)
	v := browseItems(q, cur, page)
	v.Title = browseLibraryTitle
	if cur != nil {
		v.Title = cur.Name
	}
	v.FormAction = pagePath
	reset := func(q *browseQuery) { q.Cursor = "" }
	v.GridHref = q.href(pagePath, func(q *browseQuery) { reset(q); q.View = "grid" })
	v.ListHref = q.href(pagePath, func(q *browseQuery) { reset(q); q.View = "list" })
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

	return v
}

// filterSet writes the address of the list with one change made, from the first page.
func filterSet(q browseQuery, pagePath string) func(apply func(*browseQuery)) string {
	return func(apply func(*browseQuery)) string {
		return q.href(pagePath, func(q *browseQuery) { q.Cursor = ""; apply(q) })
	}
}

// libraryKinds builds every kind of filter with its choices: Author, Narrator, Series, Category,
// Length and Format (Format only once the person can see an ebook, or has chosen one). Every choice is
// an address; a kind's "Any ..." link drops its own filter and keeps the others. A kind with nothing
// to choose is left out. Which choices are filled in follows the facets given: the page passes all of
// them, a kind's fragment only its own.
func libraryKinds(q browseQuery, cur *library.LibrarySummary, f libraryFacets, genres []library.Facet, hasText bool) []pages.FilterKind {
	pagePath, _ := browsePaths(cur)
	set := filterSet(q, pagePath)
	people := func(id, label, any string, opts []library.NamedFacet, chosen string, assign func(*browseQuery, string)) pages.FilterKind {
		k := pages.FilterKind{ID: id, Label: label, AnyLabel: any, Counts: true, On: chosen != "",
			AnyHref: set(func(q *browseQuery) { assign(q, "") })}
		for _, o := range opts {
			oid := o.ID
			k.Options = append(k.Options, pages.FilterOption{Name: o.Name, Count: o.Count, On: oid == chosen,
				Href: set(func(q *browseQuery) { assign(q, oid) })})
			if oid == chosen {
				k.Chosen = o.Name
			}
		}
		return k
	}
	author := people("author", "Author", "Any author", f.Authors, q.Author, func(q *browseQuery, id string) { q.Author = id })
	narrator := people("narrator", "Narrator", "Any narrator", f.Narrators, q.Narrator, func(q *browseQuery, id string) { q.Narrator = id })
	series := people("series", "Series", "Any series", f.Series, q.Series, func(q *browseQuery, id string) { q.Series = id })

	// Category: a multi-select of toggle links; at the limit the unchosen ones cannot be picked.
	toggle := func(g string) string {
		return set(func(q *browseQuery) {
			if q.hasGenre(g) {
				q.without(g)
			} else if len(q.Genres) < browseMaxGenres {
				q.Genres = append(q.Genres, g)
			}
		})
	}
	option := func(g string, n int) pages.FilterOption {
		on := q.hasGenre(g)
		return pages.FilterOption{Name: g, Count: n, On: on, Href: toggle(g), Disabled: !on && len(q.Genres) >= browseMaxGenres}
	}
	category := pages.FilterKind{ID: "category", Label: libCategory, AnyLabel: libCategoryAny, Counts: true,
		On: len(q.Genres) > 0, AnyHref: set(func(q *browseQuery) { q.Genres = nil })}
	listed := map[string]bool{}
	for _, g := range genres {
		if len(category.Options) == browseCategoryMax {
			break
		}
		category.Options = append(category.Options, option(g.Value, g.Count))
		listed[g.Value] = true
	}
	for _, g := range q.Genres { // a chosen genre stays listed, so it can be taken off
		if !listed[g] {
			category.Options = append(category.Options, option(g, 0))
		}
	}
	if n := len(q.Genres); n == 1 {
		category.Chosen = q.Genres[0]
	} else if n > 1 {
		category.Chosen = fmt.Sprintf(libCategoryMore, q.Genres[0], n-1)
	}

	length := pages.FilterKind{ID: "length", Label: "Length", AnyLabel: "Any length", On: q.Length != "",
		AnyHref: set(func(q *browseQuery) { q.Length = "" })}
	for _, o := range browseLengthOptions {
		val := o.Value
		length.Options = append(length.Options, pages.FilterOption{Name: o.Label, Value: val, On: val == q.Length,
			Href: set(func(q *browseQuery) { q.Length = val })})
		if val == q.Length {
			length.Chosen = o.Label
		}
	}

	format := pages.FilterKind{ID: "format", Label: libFormat, AnyLabel: libFormatAny, On: q.Format != "",
		AnyHref: set(func(q *browseQuery) { q.Format = "" })}
	for _, o := range browseFormatOptions {
		val := o.Value
		format.Options = append(format.Options, pages.FilterOption{Name: o.Label, Value: val, On: val == q.Format,
			Href: set(func(q *browseQuery) { q.Format = val })})
		if val == q.Format {
			format.Chosen = o.Label
		}
	}

	added := people("added", libAddedBy, libAddedAny, f.Added, q.Added, func(q *browseQuery, id string) { q.Added = id })
	added.Counts = f.AddedCounts
	for _, o := range added.Options {
		if o.On {
			added.Chosen = o.Name
		}
	}

	var kinds []pages.FilterKind
	for _, k := range [...]struct {
		k      pages.FilterKind
		always bool
	}{{author, false}, {narrator, false}, {series, false}, {category, false}, {length, true}, {format, false}, {added, false}} {
		if k.k.ID == "format" && !hasText && !k.k.On {
			continue
		}
		if !k.always && !k.k.On && len(k.k.Options) == 0 {
			continue
		}
		kinds = append(kinds, k.k)
	}
	return kinds
}

// lazyKinds are the kinds whose lists are long enough to load on first open instead of riding in the page.
var lazyKinds = map[string]bool{"author": true, "narrator": true, "series": true, "category": true}

// libraryFilters adds the Filter, Show and Sort menus, the active filter row and the values the forms
// carry. The Filter menu is one <details> with a nested disclosure per kind; a kind's list loads on
// its first open (or rides in the page when the address says add=kind). Every choice is an address.
func (s *server) libraryFilters(v *pages.LibraryView, q browseQuery, cur *library.LibrarySummary, f libraryFacets, genres []library.Facet, hasText bool) {
	pagePath, _ := browsePaths(cur)
	set := filterSet(q, pagePath)
	kinds := libraryKinds(q, cur, f, genres, hasText)

	filter := pages.FilterMenu{ID: "filter", Icon: "filter", Label: libFilterGroup, Chip: libFilterChip}
	for _, k := range kinds {
		if lazyKinds[k.ID] {
			k.PageHref = q.href(pagePath, func(q *browseQuery) { q.Cursor = ""; q.Add = k.ID })
			k.LazyHref = q.href(facetPath(cur, k.ID), func(q *browseQuery) { q.Cursor = "" })
		}
		if q.Add == k.ID {
			k.Open, filter.Open = true, true
		}
		filter.Kinds = append(filter.Kinds, k)
	}
	v.FilterMenu = filter
	v.Active = activeFilters(q, pagePath, kinds)

	// Show holds the listening states; Sort is its own chip and never counts as a filter.
	show := pages.FilterMenu{ID: "show", Titled: true, Label: showLabel, Chip: showLabel, AnyLabel: showAny, On: q.Show != "",
		AnyHref: set(func(q *browseQuery) { q.Show = "" })}
	for _, o := range browseShowOptions {
		val := o.Value
		show.Options = append(show.Options, pages.FilterOption{Name: o.Label, Value: val, On: val == q.Show,
			Href: set(func(q *browseQuery) { q.Show = val })})
		if val == q.Show {
			show.Chip = showLabel + ": " + o.Label
		}
	}
	sort := pages.FilterMenu{ID: "sort", Titled: true, Label: libSortHeading, Chip: libSortLabel}
	for _, o := range browseSortLabels {
		sv := o.Sort
		sort.Options = append(sort.Options, pages.FilterOption{Name: o.Label, Value: string(sv), On: sv == q.Sort,
			Href: set(func(q *browseQuery) { q.Sort = sv })})
		if sv == q.Sort {
			sort.Chip = libSortLabel + ": " + o.Label
		}
	}
	sort.End = true
	v.ShowMenu, v.ShowValue, v.SortMenu = show, q.Show, sort

	v.TitleFilter, v.LengthValue = q.Title, q.Length
	for _, h := range [...]pages.HiddenField{{Name: "author", Value: q.Author}, {Name: "narrator", Value: q.Narrator}, {Name: "series", Value: q.Series},
		{Name: "format", Value: q.Format}, {Name: "added", Value: q.Added}} {
		if h.Value != "" {
			v.Hidden = append(v.Hidden, h)
		}
	}
	if q.Sort != library.SortAdded { // the title form keeps the order
		v.Hidden = append(v.Hidden, pages.HiddenField{Name: "sort", Value: string(q.Sort)})
	}
}

// activeFilters is the active filter row: one chip per filter in use (each chosen category on its own,
// and the typed title in quotes), each a link to the list without only that filter.
func activeFilters(q browseQuery, pagePath string, kinds []pages.FilterKind) []pages.ActiveFilter {
	set := filterSet(q, pagePath)
	chosen := map[string]string{}
	for _, k := range kinds {
		chosen[k.ID] = k.Chosen
	}
	var out []pages.ActiveFilter
	add := func(kind, value string, drop func(*browseQuery)) {
		out = append(out, pages.ActiveFilter{Kind: kind, Value: value, Name: libRemoveFilter + ": " + strings.TrimSpace(kind+" "+value), Href: set(drop)})
	}
	if q.Author != "" {
		add("Author", chosen["author"], func(q *browseQuery) { q.Author = "" })
	}
	if q.Series != "" {
		add("Series", chosen["series"], func(q *browseQuery) { q.Series = "" })
	}
	if q.Narrator != "" {
		add("Narrator", chosen["narrator"], func(q *browseQuery) { q.Narrator = "" })
	}
	for _, g := range q.Genres {
		add(libCategory, g, func(q *browseQuery) { q.without(g) })
	}
	if q.Length != "" {
		add("Length", chosen["length"], func(q *browseQuery) { q.Length = "" })
	}
	if q.Format != "" {
		add(libFormat, chosen["format"], func(q *browseQuery) { q.Format = "" })
	}
	if q.Added != "" {
		add(libAddedBy, chosen["added"], func(q *browseQuery) { q.Added = "" })
	}
	if q.Show != "" {
		for _, o := range browseShowOptions {
			if o.Value == q.Show {
				add(showLabel, o.Label, func(q *browseQuery) { q.Show = "" })
			}
		}
	}
	if q.Title != "" {
		add("Title", "\u201c"+q.Title+"\u201d", func(q *browseQuery) { q.Title = "" })
	}
	return out
}

// facetPath is the fragment address of one kind's list.
func facetPath(cur *library.LibrarySummary, kind string) string {
	if cur == nil {
		return "/hx/library-facets/" + kind
	}
	return "/hx/library-facets/" + cur.ID + "/" + kind
}

// getLibraryFacet is the fragment a closed kind loads on its first open: that kind's "Any ..." line
// and choices under the address's other filters. It reads only what that kind needs.
func (s *server) getLibraryFacet(w http.ResponseWriter, r *http.Request) {
	_, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if !lazyKinds[kind] {
		s.notFound(w, r)
		return
	}
	q := parseBrowseQuery(r)
	q.Added = addedByFilter(currentUser(r), q.Added)
	lq := s.listQuery(currentUser(r), q, cur)
	ctx, sc := r.Context(), s.scope(r)
	var (
		f      libraryFacets
		genres []library.Facet
		err    error
	)
	switch kind {
	case "author":
		f.Authors, err = s.lib.PeopleFacet(ctx, sc, lq, library.RoleAuthor, browseMenuMax)
	case "narrator":
		f.Narrators, err = s.lib.PeopleFacet(ctx, sc, lq, library.RoleNarrator, browseMenuMax)
	case "series":
		f.Series, err = s.lib.SeriesFacet(ctx, sc, lq, browseMenuMax)
	case "category":
		genres, err = s.lib.Genres(ctx, sc, curID(cur))
	}
	if errors.Is(err, library.ErrInvalid) {
		s.fragment(w, r, http.StatusBadRequest, components.ErrorToast(browseBadCursorToast))
		return
	}
	if err != nil {
		s.serverError(w, r, "library facet", err)
		return
	}
	for _, k := range libraryKinds(q, cur, f, genres, false) {
		if k.ID == kind {
			s.fragment(w, r, http.StatusOK, pages.FilterKindChoices(k))
			return
		}
	}
	s.fragment(w, r, http.StatusOK, pages.FilterKindChoices(pages.FilterKind{}))
}

// hasTextBooks reports whether the person can see any ebook in the shown library, which is when the
// Format filter is offered.
func (s *server) hasTextBooks(r *http.Request, cur *library.LibrarySummary) (bool, error) {
	page, err := s.lib.ListBooks(r.Context(), s.scope(r), library.ListQuery{LibraryID: curID(cur), Format: library.BookText, Limit: 1})
	return page.Total > 0, err
}
