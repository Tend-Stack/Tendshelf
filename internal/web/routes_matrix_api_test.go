package web

import "net/http"

// apiSweepRows are the route matrix rows of the JSON read API (P3-9a). The sessions of the matrix
// are accepted under /api/v1 for GET, so the same roles are tried as on the pages; the anonymous
// answer is a 401 in JSON, never a redirect.
func apiSweepRows() []sweepRow {
	api := func(r sweepRow) sweepRow {
		r.Anon = http.StatusUnauthorized
		return r
	}
	shelfPath := func(other bool) func(*sweepEnv, int) string {
		return func(e *sweepEnv, role int) string {
			if other {
				return "/api/v1/shelves/" + e.otherShelf(role)
			}
			return "/api/v1/shelves/" + e.ownShelf(role)
		}
	}
	bookPath := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.book.ID + suffix }
	}
	filePath := func(prefix string, idx int) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.audio[idx] }
	}
	ok := sweepSame(http.StatusOK)
	notGuest := sweepNotGuest(http.StatusOK, http.StatusNotFound)
	staff := sweepStaff(http.StatusOK, http.StatusNotFound)
	post405 := func(pattern string, path func(*sweepEnv, int) string) sweepRow {
		r := sweepPost(pattern, path, nil, [4]int{http.StatusMethodNotAllowed, 0, 0, 0}, "")
		r.Label = "POST"
		return api(r)
	}
	other := api(sweepGet("GET /api/v1/shelves/{id}", shelfPath(true), sweepSame(http.StatusNotFound)))
	other.Label = "someone else's shelf"
	unknown := api(sweepGet("GET /api/v1/books/{id}", func(*sweepEnv, int) string { return "/api/v1/books/bk_unknown" }, sweepSame(http.StatusNotFound)))
	unknown.Label = "unknown book"
	return []sweepRow{
		api(sweepGet("GET /api/v1/libraries", sweepStatic("/api/v1/libraries"), ok)),
		api(sweepGet("GET /api/v1/books", sweepStatic("/api/v1/books"), ok)),
		api(sweepGet("GET /api/v1/libraries/{id}/books", func(e *sweepEnv, _ int) string { return "/api/v1/libraries/" + e.libID + "/books" }, staff)),
		api(sweepGet("GET /api/v1/books/{id}", bookPath("/api/v1/books/", ""), staff)),
		unknown,
		api(sweepGet("GET /api/v1/search", sweepStatic("/api/v1/search?q=Lantern"), ok)),
		api(sweepGet("GET /api/v1/shelves", sweepStatic("/api/v1/shelves"), notGuest)),
		api(sweepGet("GET /api/v1/shelves/{id}", shelfPath(false), notGuest)),
		other,
		api(sweepGet("GET /api/v1/shared", sweepStatic("/api/v1/shared"), ok)),
		api(sweepGet("GET /api/v1/covers/{bookID}", bookPath("/api/v1/covers/", "?w=320"), staff)),
		api(sweepGet("GET /api/v1/stream/{audioFileID}", filePath("/api/v1/stream/", 0), staff)),
		api(sweepGet("GET /api/v1/peaks/{audioFileID}", filePath("/api/v1/peaks/", 0), staff)),
		post405("/api/v1/covers/{bookID}", bookPath("/api/v1/covers/", "")),
		post405("/api/v1/stream/{audioFileID}", filePath("/api/v1/stream/", 0)),
		post405("/api/v1/peaks/{audioFileID}", filePath("/api/v1/peaks/", 0)),
	}
}
