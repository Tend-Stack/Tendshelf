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
	apiUnsafe := func(pattern, method string, path func(*sweepEnv, int) string, want [4]int) sweepRow {
		return sweepRow{Pattern: pattern, Method: method, Path: path, Want: want}
	}
	staff415 := sweepStaff(http.StatusUnsupportedMediaType, http.StatusNotFound)
	staff204 := sweepStaff(http.StatusNoContent, http.StatusNotFound)
	// The owner and the admin use their own bookmark; the member's is on a book they cannot listen
	// to (404 proves the book check); the guest points at the owner's.
	bookmarkPath := func(other bool) func(*sweepEnv, int) string {
		return func(e *sweepEnv, role int) string {
			i := role
			switch {
			case other:
				i = (role + 1) % 3
			case role > sweepMember:
				i = sweepOwner
			}
			return "/api/v1/bookmarks/" + e.bookmark[i]
		}
	}
	bmOther := func(pattern, method string) sweepRow {
		r := api(apiUnsafe(pattern, method, bookmarkPath(true), sweepSame(http.StatusNotFound)))
		r.Label = "someone else's bookmark"
		return r
	}
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
		// P3-9b sync side. The unsafe rows send a form, so a person who may act gets 415 (the body is
		// checked after authorization) and a person who may not gets 404.
		api(sweepGet("GET /api/v1/progress", sweepStatic("/api/v1/progress?since=0"), ok)),
		api(sweepGet("GET /api/v1/progress/{bookID}", bookPath("/api/v1/progress/", ""), staff)),
		api(apiUnsafe("PUT /api/v1/progress/{bookID}", http.MethodPut, bookPath("/api/v1/progress/", ""), staff415)),
		api(apiUnsafe("POST /api/v1/progress/batch", http.MethodPost, sweepStatic("/api/v1/progress/batch"), sweepSame(http.StatusUnsupportedMediaType))),
		api(sweepGet("GET /api/v1/books/{id}/bookmarks", bookPath("/api/v1/books/", "/bookmarks"), staff)),
		api(apiUnsafe("POST /api/v1/books/{id}/bookmarks", http.MethodPost, bookPath("/api/v1/books/", "/bookmarks"), staff415)),
		api(sweepGet("GET /api/v1/bookmarks", sweepStatic("/api/v1/bookmarks?since=0"), ok)),
		api(apiUnsafe("PATCH /api/v1/bookmarks/{id}", http.MethodPatch, bookmarkPath(false), staff415)),
		api(apiUnsafe("DELETE /api/v1/bookmarks/{id}", http.MethodDelete, bookmarkPath(false), staff204)),
		bmOther("PATCH /api/v1/bookmarks/{id}", http.MethodPatch),
		bmOther("DELETE /api/v1/bookmarks/{id}", http.MethodDelete),
		// Up next (Q1). The body is checked before the book on the two that name it in the body or
		// the form is sent: a form is 415 for everyone; the book-in-path rows check access first.
		api(sweepGet("GET /api/v1/queue", sweepStatic("/api/v1/queue"), ok)),
		api(apiUnsafe("POST /api/v1/queue", http.MethodPost, sweepStatic("/api/v1/queue"), sweepSame(http.StatusUnsupportedMediaType))),
		api(sweepGet("GET /api/v1/queue/next", sweepStatic("/api/v1/queue/next"), sweepSame(http.StatusNoContent))),
		api(apiUnsafe("POST /api/v1/queue/next", http.MethodPost, sweepStatic("/api/v1/queue/next"), sweepSame(http.StatusUnsupportedMediaType))),
		api(apiUnsafe("POST /api/v1/queue/{bookID}/move", http.MethodPost, bookPath("/api/v1/queue/", "/move"), staff415)),
		// The app's settings, per-book speed and Continue shelf (A1). The unsafe rows send a form: 415 for
		// everyone on settings, and the book check first on the speed row.
		api(sweepGet("GET /api/v1/settings", sweepStatic("/api/v1/settings"), ok)),
		api(apiUnsafe("PATCH /api/v1/settings", http.MethodPatch, sweepStatic("/api/v1/settings"), sweepSame(http.StatusUnsupportedMediaType))),
		api(apiUnsafe("PUT /api/v1/books/{id}/speed", http.MethodPut, bookPath("/api/v1/books/", "/speed"), staff415)),
		api(sweepGet("GET /api/v1/continue", sweepStatic("/api/v1/continue"), ok)),
		api(apiUnsafe("DELETE /api/v1/queue/{bookID}", http.MethodDelete, bookPath("/api/v1/queue/", ""), sweepSame(http.StatusNotFound))),
	}
}
