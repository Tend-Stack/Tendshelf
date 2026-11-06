package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The read side of the JSON API. Every route below reuses the function the matching page calls
// (loadBook, browseTarget, viewShelf, the library repo with the person's scope), so what a person
// can read here is exactly what the pages show them, and a hidden book is a 404 here as there.

// Limits of the list routes (the page size is the default).
const (
	apiBooksDefault  = browsePageSize
	apiBooksMax      = 120
	apiSearchDefault = 20
	apiSearchMax     = 200
	apiSharedDefault = 24
	apiSharedMax     = 50
)

func (s *server) registerAPIRead(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/libraries", s.getAPILibraries)
	mux.HandleFunc("GET /api/v1/books", s.getAPIBooks)
	mux.HandleFunc("GET /api/v1/libraries/{id}/books", s.getAPIBooks)
	mux.HandleFunc("GET /api/v1/books/{id}", s.getAPIBook)
	mux.HandleFunc("GET /api/v1/search", s.getAPISearch)
	mux.HandleFunc("GET /api/v1/shelves", s.getAPIShelves)
	mux.HandleFunc("GET /api/v1/shelves/{id}", s.getAPIShelf)
	mux.HandleFunc("GET /api/v1/shared", s.getAPIShared)

	// The media routes under /api/v1 so a device token can reach them (a token is refused
	// elsewhere). They are the same handlers as /covers, /stream and /peaks.
	mux.HandleFunc("GET /api/v1/covers/{bookID}", s.getCover)
	mux.HandleFunc("GET /api/v1/stream/{audioFileID}", s.getStream)
	mux.HandleFunc("GET /api/v1/peaks/{audioFileID}", s.getPeaks)
	mux.HandleFunc("/api/v1/covers/{bookID}", s.apiGetOnly)
	mux.HandleFunc("/api/v1/stream/{audioFileID}", s.apiGetOnly)
	mux.HandleFunc("/api/v1/peaks/{audioFileID}", s.apiGetOnly)
}

// apiGetOnly answers any method but GET and HEAD on a media alias, as the originals do.
func (s *server) apiGetOnly(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "GET, HEAD")
	s.plainError(w, r, http.StatusMethodNotAllowed, "Method not allowed.")
}

// apiLimit reads ?limit=: absent is def, otherwise an integer in 1..max. A bad value answers 400
// and ok is false.
func apiLimit(w http.ResponseWriter, r *http.Request, def, max int) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > max {
		api.ErrorField(w, http.StatusBadRequest, api.CodeInvalid, "limit", "The limit must be a whole number from 1 to "+strconv.Itoa(max)+".")
		return 0, false
	}
	return n, true
}

func (s *server) getAPILibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.lib.VisibleLibraries(r.Context(), s.scope(r))
	if err != nil {
		s.serverError(w, r, "api libraries", err)
		return
	}
	items := make([]apiLibrary, 0, len(libs))
	for _, l := range libs {
		items = append(items, apiLibrary{ID: l.ID, Name: l.Name, Books: l.Books})
	}
	api.WriteCached(w, r, http.StatusOK, struct {
		Items []apiLibrary `json:"items"`
	}{items})
}

// getAPIBooks lists books with the Library page's filters. The page ignores a value it does not
// know, and so does this; only the cursor and the limit are refused.
func (s *server) getAPIBooks(w http.ResponseWriter, r *http.Request) {
	limit, ok := apiLimit(w, r, apiBooksDefault, apiBooksMax)
	if !ok {
		return
	}
	_, cur, ok := s.browseTarget(w, r)
	if !ok {
		return
	}
	lq := s.listQuery(currentUser(r), parseBrowseQuery(r), cur)
	lq.Limit = limit
	page, err := s.lib.ListBooks(r.Context(), s.scope(r), lq)
	if errors.Is(err, library.ErrInvalid) {
		api.ErrorField(w, http.StatusBadRequest, api.CodeInvalid, "cursor", "That cursor is not valid. Start again from the first page.")
		return
	}
	if err != nil {
		s.serverError(w, r, "api books", err)
		return
	}
	total := page.Total
	api.Write(w, http.StatusOK, api.NewList(apiCardsOf(page.Books), page.Next, &total))
}

type apiBookPermissions struct {
	Listen   bool `json:"listen"`
	Download bool `json:"download"`
}

type apiBookDetail struct {
	Book         apiBookCard        `json:"book"`
	Description  string             `json:"description"`
	Publisher    string             `json:"publisher"`
	Language     string             `json:"language"`
	Abridged     bool               `json:"abridged"`
	Explicit     bool               `json:"explicit"`
	Series       []apiSeriesRef     `json:"series"`
	Genres       []string           `json:"genres"`
	Tags         []string           `json:"tags"`
	Chapters     []apiChapter       `json:"chapters"`
	Files        []apiFile          `json:"files"`
	Parts        int                `json:"parts"`
	MissingParts int                `json:"missingParts"`
	Refused      bool               `json:"refused"`
	Missing      bool               `json:"missing"`
	Permissions  apiBookPermissions `json:"permissions"`
	Progress     *apiProgress       `json:"progress"`
	// Kind is "audio" or "text"; Formats lists what this person can open ("audio", "text"); Reading is their
	// place in the text, null when they have none or may not read the book.
	Kind    string      `json:"kind"`
	Formats []string    `json:"formats"`
	Reading *apiReading `json:"reading"`
	// Speed is the person's own speed for this book, else their usual speed.
	Speed float64 `json:"speed"`
}

type apiReading struct {
	PercentBP int   `json:"percentBp"`
	UpdatedAt int64 `json:"updatedAt"`
}

func nonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func (s *server) getAPIBook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	book, ok := s.loadBook(w, r, r.PathValue("id"), share.ViewBook)
	if !ok {
		return
	}
	d, err := s.lib.BookDetail(ctx, s.scope(r), book.ID)
	if errors.Is(err, library.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, "api book detail", err)
		return
	}
	u := currentUser(r)
	target := share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}
	out := apiBookDetail{
		Book: apiCardOf(d.Card), Description: d.Book.Description, Publisher: d.Book.Publisher, Language: d.Book.Language,
		Abridged: d.Book.Abridged, Explicit: d.Book.Explicit, Genres: nonNil(d.Genres), Tags: nonNil(d.Tags),
		Chapters: apiChaptersOf(d.Chapters), Files: []apiFile{}, Parts: d.Parts, MissingParts: d.MissingParts,
		Refused: book.RefusedReason != "", Missing: book.MissingSince != 0,
		Permissions: apiBookPermissions{
			Listen:   s.can.Can(ctx, u, share.ListenBook, target),
			Download: s.can.Can(ctx, u, share.DownloadBook, target),
		},
		Series: make([]apiSeriesRef, 0, len(d.Series)),
	}
	for _, sr := range d.Series {
		out.Series = append(out.Series, apiSeriesOf(sr))
	}
	files, prog, canPlay, err := s.bookPlayData(r, book)
	if err != nil {
		s.serverError(w, r, "api book play", err)
		return
	}
	if canPlay {
		out.Files = apiFilesOf(book, files)
		if prog != nil {
			p := apiProgressOf(*prog)
			out.Progress = &p
		}
	}
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "api book speed", err)
		return
	}
	out.Speed = set.Speed
	if sp, err := s.users.BookSpeed(ctx, u.ID, book.ID); err == nil {
		out.Speed = sp
	} else if !errors.Is(err, users.ErrNoBookSpeed) {
		s.serverError(w, r, "api book speed", err)
		return
	}
	out.Kind, out.Formats = book.Kind, []string{}
	if out.Kind == "" {
		out.Kind = library.BookAudio
	}
	if out.Kind == library.BookAudio && len(out.Files) > 0 && out.Permissions.Listen {
		out.Formats = append(out.Formats, library.BookAudio)
	}
	mayRead, err := s.mayReadBook(ctx, u, book)
	if err != nil {
		s.serverError(w, r, "api book text", err)
		return
	}
	if mayRead {
		out.Formats = append(out.Formats, library.BookText)
		rp, err := s.readingOf(ctx, u.ID, book.ID)
		if err != nil {
			s.serverError(w, r, "api book reading", err)
			return
		}
		if rp != nil {
			out.Reading = &apiReading{PercentBP: rp.PercentBP, UpdatedAt: rp.ClientUpdatedAt}
		}
	}
	api.WriteCached(w, r, http.StatusOK, out)
}

type apiSearchPerson struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type apiSearchResult struct {
	Books      []apiBookCard     `json:"books"`
	Total      int               `json:"total"`
	Suggestion *string           `json:"suggestion"`
	People     []apiSearchPerson `json:"people"`
	Series     []apiPerson       `json:"series"`
}

func (s *server) getAPISearch(w http.ResponseWriter, r *http.Request) {
	limit, ok := apiLimit(w, r, apiSearchDefault, apiSearchMax)
	if !ok {
		return
	}
	out := apiSearchResult{Books: []apiBookCard{}, People: []apiSearchPerson{}, Series: []apiPerson{}}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		api.Write(w, http.StatusOK, out)
		return
	}
	ctx, sc := r.Context(), s.scope(r)
	res, err := s.lib.Search(ctx, sc, q, limit)
	if err != nil {
		s.serverError(w, r, "api search", err)
		return
	}
	cards, err := s.lib.CardsByID(ctx, sc, res.IDs)
	if err != nil {
		s.serverError(w, r, "api search cards", err)
		return
	}
	names, err := s.lib.Suggest(ctx, sc, q, searchNamesN)
	if err != nil {
		s.serverError(w, r, "api search names", err)
		return
	}
	out.Books, out.Total = apiCardsOf(cards), res.Total
	if res.Suggestion != "" {
		out.Suggestion = &res.Suggestion
	}
	for _, n := range names {
		switch n.Kind {
		case searchKindAuthor, searchKindNarrator:
			out.People = append(out.People, apiSearchPerson{ID: n.ID, Name: n.Label, Role: n.Kind})
		case searchKindSeries:
			out.Series = append(out.Series, apiPerson{ID: n.ID, Name: n.Label})
		}
	}
	api.Write(w, http.StatusOK, out)
}

func (s *server) getAPIShelves(w http.ResponseWriter, r *http.Request) {
	u, ok := s.shelfUser(w, r)
	if !ok {
		return
	}
	ctx, sc := r.Context(), s.scope(r)
	own, err := s.lib.Shelves(ctx, sc, u.ID)
	if err != nil {
		s.serverError(w, r, "api shelves", err)
		return
	}
	out := struct {
		Own    []apiShelf `json:"own"`
		Shared []apiShelf `json:"shared"`
	}{Own: apiShelvesOf(own), Shared: []apiShelf{}}
	if s.can.Can(ctx, u, share.ViewShelf, share.Target{Kind: share.TargetShelf, Shared: true}) {
		others, err := s.lib.SharedShelves(ctx, sc, u.ID)
		if err != nil {
			s.serverError(w, r, "api shared shelves", err)
			return
		}
		out.Shared = apiShelvesOf(others)
	}
	api.WriteCached(w, r, http.StatusOK, out)
}

func (s *server) getAPIShelf(w http.ResponseWriter, r *http.Request) {
	_, sh, _, ok := s.viewShelf(w, r)
	if !ok {
		return
	}
	page, err := s.lib.ShelfBooks(r.Context(), s.scope(r), sh.OwnerID, sh.ID, r.URL.Query().Get("cursor"), shelfPageSize)
	if errors.Is(err, library.ErrInvalid) {
		api.ErrorField(w, http.StatusBadRequest, api.CodeInvalid, "cursor", "That cursor is not valid. Start again from the first page.")
		return
	}
	if err != nil {
		s.serverError(w, r, "api shelf books", err)
		return
	}
	total := page.Total
	api.Write(w, http.StatusOK, struct {
		Shelf apiShelf              `json:"shelf"`
		Books api.List[apiBookCard] `json:"books"`
	}{
		Shelf: apiShelfOf(library.ShelfSummary{Shelf: sh, Books: total}),
		Books: api.NewList(apiCardsOf(page.Books), page.Next, &total),
	})
}

type apiSharedBook struct {
	Book       apiBookCard `json:"book"`
	SharedAt   int64       `json:"sharedAt"`
	SharerName string      `json:"sharerName"`
}

func (s *server) getAPIShared(w http.ResponseWriter, r *http.Request) {
	limit, ok := apiLimit(w, r, apiSharedDefault, apiSharedMax)
	if !ok {
		return
	}
	row, err := s.lib.SharedWithYou(r.Context(), s.scope(r), limit)
	if err != nil {
		s.serverError(w, r, "api shared", err)
		return
	}
	out := struct {
		Items   []apiSharedBook `json:"items"`
		Total   int             `json:"total"`
		Sharers []string        `json:"sharers"`
	}{Items: make([]apiSharedBook, 0, len(row.Books)), Total: row.Total, Sharers: nonNil(row.Sharers)}
	for _, b := range row.Books {
		out.Items = append(out.Items, apiSharedBook{Book: apiCardOf(b.Book), SharedAt: b.SharedAt, SharerName: b.SharerName})
	}
	api.Write(w, http.StatusOK, out)
}
