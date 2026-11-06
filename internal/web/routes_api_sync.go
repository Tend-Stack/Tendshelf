package web

import (
	"encoding/json"
	"errors"
	"math"
	"mime"
	"net/http"
	"slices"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/api"
	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// The JSON API's sync side (P3-9b): progress and bookmarks. Every write goes through the same
// service calls as the player (progress.Service.Save holds the clamp and the newest-wins rule), and
// every read is the person's own rows, filtered by the scope SQL so a hidden book never shows.

const (
	progressBatchLimit = 64 << 10 // the body of POST /api/v1/progress/batch
	progressBatchMax   = 100      // updates in one batch
	apiBookmarkBody    = 8 << 10  // the body of a bookmark write: a position, a note, an id
	apiFeedDefault     = 100
	apiFeedMax         = 500
)

func (s *server) registerAPISync(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/progress", s.getAPIProgressFeed)
	mux.HandleFunc("GET /api/v1/progress/{bookID}", s.getProgress)
	mux.HandleFunc("PUT /api/v1/progress/{bookID}", s.putAPIProgress)
	mux.HandleFunc("POST /api/v1/progress/batch", s.postAPIProgressBatch)
	mux.HandleFunc("GET /api/v1/books/{id}/bookmarks", s.getAPIBookmarks)
	mux.HandleFunc("POST /api/v1/books/{id}/bookmarks", s.postAPIBookmark)
	mux.HandleFunc("GET /api/v1/bookmarks", s.getAPIBookmarkFeed)
	mux.HandleFunc("PATCH /api/v1/bookmarks/{id}", s.patchAPIBookmark)
	mux.HandleFunc("DELETE /api/v1/bookmarks/{id}", s.deleteAPIBookmark)
}

// apiBadField answers 400 naming the wrong field.
func apiBadField(w http.ResponseWriter, field, msg string) {
	api.ErrorField(w, http.StatusBadRequest, api.CodeInvalid, field, msg)
}

// apiJSONBody reads a JSON object into v after the caller has authorized: 415 for another content
// type, 413 past limit, 400 for text that is not one JSON value. Unknown fields are ignored so
// newer clients can send more (docs/api.md, versioning).
func apiJSONBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		api.StatusError(w, http.StatusUnsupportedMediaType)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	err := json.NewDecoder(r.Body).Decode(v)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		api.StatusError(w, http.StatusRequestEntityTooLarge)
		return false
	case err != nil:
		api.Error(w, http.StatusBadRequest, api.CodeInvalid, "The body is not valid JSON.")
		return false
	}
	return true
}

// apiSince reads ?since=: absent is 0, otherwise a non-negative integer of Unix milliseconds.
func apiSince(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		apiBadField(w, "since", "Since must be a time in Unix milliseconds, 0 or more.")
		return 0, false
	}
	return n, true
}

// camelField turns a service field name (position_ms) into the API's (positionMs).
func camelField(f string) string {
	out := []byte{}
	up := false
	for i := 0; i < len(f); i++ {
		switch {
		case f[i] == '_':
			up = true
		case up:
			out = append(out, f[i]-('a'-'A'))
			up = false
		default:
			out = append(out, f[i])
		}
	}
	return string(out)
}

// apiProgressIn is one progress save as the client sends it. The pointers tell "missing" from zero.
type apiProgressIn struct {
	BookID          string   `json:"bookId"`
	PositionMs      *int64   `json:"positionMs"`
	ClientUpdatedAt *int64   `json:"clientUpdatedAt"`
	DeviceID        string   `json:"deviceId"`
	Finished        bool     `json:"finished"`
	Force           bool     `json:"force"`
	Speed           float64  `json:"speed"`
	ListenedMs      *int64   `json:"listenedMs"`
	DurationMs      *float64 `json:"durationMs"` // accepted and ignored: the book's duration is used
}

// check validates the body into a progressInput, or names the wrong field.
func (in apiProgressIn) check(r *http.Request) (progressInput, string) {
	out := progressInput{DeviceID: in.DeviceID, Finished: in.Finished, Force: in.Force, Speed: in.Speed}
	switch {
	case in.PositionMs == nil || *in.PositionMs < 0:
		return out, "positionMs"
	case in.ClientUpdatedAt == nil || *in.ClientUpdatedAt <= 0:
		return out, "clientUpdatedAt"
	case math.IsNaN(in.Speed) || math.IsInf(in.Speed, 0):
		return out, "speed"
	case in.ListenedMs != nil && *in.ListenedMs < 0:
		return out, "listenedMs"
	}
	out.PositionMs, out.ClientUpdatedAt = *in.PositionMs, *in.ClientUpdatedAt
	if in.ListenedMs != nil {
		out.ListenedMs = *in.ListenedMs
	}
	if out.DeviceID == "" {
		// A token client is its device; a browser has to say which one it is.
		d, ok := auth.DeviceFrom(r.Context())
		if !ok {
			return out, "deviceId"
		}
		out.DeviceID = d.ID
	}
	return out, ""
}

// save runs a checked save; field is the wrong field when the service refused a value.
func (s *server) apiSaveProgress(r *http.Request, b library.Book, in progressInput) (res progress.Result, field string, err error) {
	res, err = s.saveProgress(r, b, in)
	var inv *progress.InvalidError
	if errors.As(err, &inv) {
		return res, camelField(inv.Field), err
	}
	return res, "", err
}

func (s *server) putAPIProgress(w http.ResponseWriter, r *http.Request) {
	b, ok := s.playableBook(w, r, s.followMerged(r.Context(), r.PathValue("bookID")))
	if !ok {
		return
	}
	var body apiProgressIn
	if !apiJSONBody(w, r, progressBodyLimit, &body) {
		return
	}
	in, bad := body.check(r)
	if bad != "" {
		apiBadField(w, bad, "That value is missing or not valid.")
		return
	}
	res, field, err := s.apiSaveProgress(r, b, in)
	switch {
	case field != "":
		apiBadField(w, field, "That value is not valid.")
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "save progress", err)
	default:
		api.Write(w, http.StatusOK, map[string]any{
			"applied": res.Applied, "progress": apiProgressOf(res.Progress), "serverNow": s.now().UnixMilli(),
		})
	}
}

// apiBatchError is the per-item error of a batch.
type apiBatchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

type apiBatchResult struct {
	BookID   string         `json:"bookId"`
	Applied  bool           `json:"applied"`
	Progress *apiProgress   `json:"progress"`
	Error    *apiBatchError `json:"error"`
}

func (s *server) postAPIProgressBatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Updates []apiProgressIn `json:"updates"`
	}
	if !apiJSONBody(w, r, progressBatchLimit, &body) {
		return
	}
	if n := len(body.Updates); n < 1 || n > progressBatchMax {
		apiBadField(w, "updates", "Send 1 to "+strconv.Itoa(progressBatchMax)+" updates at a time.")
		return
	}
	u := currentUser(r)
	results := make([]apiBatchResult, 0, len(body.Updates))
	for _, up := range body.Updates {
		res := apiBatchResult{BookID: up.BookID}
		fail := func(code, msg, field string) {
			res.Error = &apiBatchError{Code: code, Message: msg, Field: field}
		}
		b, err := s.playableFor(r.Context(), u, up.BookID)
		switch {
		case errors.Is(err, library.ErrNotFound):
			fail(api.CodeNotFound, "There is nothing at this address.", "")
		case err != nil:
			s.serverError(w, r, "load book", err)
			return
		default:
			in, bad := up.check(r)
			if bad != "" {
				fail(api.CodeInvalid, "That value is missing or not valid.", bad)
				break
			}
			saved, field, err := s.apiSaveProgress(r, b, in)
			switch {
			case field != "":
				fail(api.CodeInvalid, "That value is not valid.", field)
			case errors.Is(err, progress.ErrNotFound):
				fail(api.CodeNotFound, "There is nothing at this address.", "")
			case err != nil:
				s.serverError(w, r, "save progress", err)
				return
			default:
				p := apiProgressOf(saved.Progress)
				res.Applied, res.Progress = saved.Applied, &p
			}
		}
		results = append(results, res)
	}
	api.Write(w, http.StatusOK, map[string]any{"results": results, "serverNow": s.now().UnixMilli()})
}

// apiFeed is the body of a change feed: the changed rows, the cursor for more, and the server clock a
// client keeps as its next "since" (minus a margin) once next is null.
type apiFeed[T any] struct {
	Items     []T     `json:"items"`
	Next      *string `json:"next"`
	ServerNow int64   `json:"serverNow"`
}

func newFeed[T any](items []T, next string, now int64) apiFeed[T] {
	if items == nil {
		items = []T{}
	}
	f := apiFeed[T]{Items: items, ServerNow: now}
	if next != "" {
		f.Next = &next
	}
	return f
}

func (s *server) getAPIProgressFeed(w http.ResponseWriter, r *http.Request) {
	since, ok := apiSince(w, r)
	if !ok {
		return
	}
	limit, ok := apiLimit(w, r, apiFeedDefault, apiFeedMax)
	if !ok {
		return
	}
	refs, next, err := s.lib.ProgressChanges(r.Context(), s.scope(r), currentUser(r).ID, since, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, library.ErrInvalid) {
		apiBadField(w, "cursor", "That cursor is not valid. Start again without it.")
		return
	}
	if err != nil {
		s.serverError(w, r, "api progress feed", err)
		return
	}
	ids := make([]string, len(refs))
	for i, x := range refs {
		ids[i] = x.ID
	}
	rows, err := s.progress.ForBooks(r.Context(), currentUser(r).ID, ids)
	if err != nil {
		s.serverError(w, r, "api progress feed", err)
		return
	}
	items := make([]apiProgress, 0, len(ids))
	for _, id := range ids {
		if p, ok := rows[id]; ok {
			items = append(items, apiProgressOf(p))
		}
	}
	api.Write(w, http.StatusOK, newFeed(items, next, s.now().UnixMilli()))
}

// apiBookmark is a bookmark; a deleted one (a tombstone, only in change feeds) has deletedAt set.
type apiBookmark struct {
	ID         string `json:"id"`
	ClientID   string `json:"clientId,omitempty"`
	BookID     string `json:"bookId"`
	PositionMs int64  `json:"positionMs"`
	Note       string `json:"note"`
	// A clip has an end (a moment in the audio after positionMs); null for a plain bookmark. Kind is one of
	// bookmark, favourite, quote, note, revisit.
	EndMs     *int64 `json:"endMs"`
	Kind      string `json:"kind"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	DeletedAt *int64 `json:"deletedAt"`
	// A place in the text carries its edition and sentence id; both are left out for a moment in the audio.
	EditionID string `json:"editionId,omitempty"`
	SID       string `json:"sid,omitempty"`
}

func apiBookmarkOf(b progress.Bookmark) apiBookmark {
	out := apiBookmark{ID: b.ID, ClientID: b.ClientID, BookID: b.BookID, PositionMs: b.PositionMs, Note: b.Note,
		CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, EditionID: b.EditionID, SID: b.SID}
	if b.DeletedAt != 0 {
		out.DeletedAt = &b.DeletedAt
	}
	if b.EndMs > 0 {
		out.EndMs = &b.EndMs
	}
	out.Kind = b.Kind
	return out
}

func apiBookmarksOf(in []progress.Bookmark) []apiBookmark {
	out := make([]apiBookmark, 0, len(in))
	for _, b := range in {
		out = append(out, apiBookmarkOf(b))
	}
	return out
}

func (s *server) getAPIBookmarks(w http.ResponseWriter, r *http.Request) {
	b, ok := s.loadBook(w, r, r.PathValue("id"), share.ListenBook)
	if !ok {
		return
	}
	q := r.URL.Query()
	notes := q.Get("notes") == "1"
	u := currentUser(r)
	if !q.Has("since") && !q.Has("cursor") {
		list, err := s.progress.Bookmarks(r.Context(), u.ID, b.ID, notes)
		if err != nil {
			s.serverError(w, r, "api bookmarks", err)
			return
		}
		api.Write(w, http.StatusOK, newFeed(apiBookmarksOf(list), "", s.now().UnixMilli()))
		return
	}
	since, ok := apiSince(w, r)
	if !ok {
		return
	}
	list, next, err := s.progress.BookmarksChanged(r.Context(), u.ID, b.ID, since, q.Get("cursor"), 0)
	if errors.Is(err, progress.ErrInvalid) {
		apiBadField(w, "cursor", "That cursor is not valid. Start again without it.")
		return
	}
	if err != nil {
		s.serverError(w, r, "api bookmarks", err)
		return
	}
	api.Write(w, http.StatusOK, newFeed(apiBookmarksOf(list), next, s.now().UnixMilli()))
}

func (s *server) getAPIBookmarkFeed(w http.ResponseWriter, r *http.Request) {
	since, ok := apiSince(w, r)
	if !ok {
		return
	}
	limit, ok := apiLimit(w, r, apiFeedDefault, apiFeedMax)
	if !ok {
		return
	}
	uid := currentUser(r).ID
	refs, next, err := s.lib.BookmarkChanges(r.Context(), s.scope(r), uid, since, r.URL.Query().Get("cursor"), limit)
	if errors.Is(err, library.ErrInvalid) {
		apiBadField(w, "cursor", "That cursor is not valid. Start again without it.")
		return
	}
	if err != nil {
		s.serverError(w, r, "api bookmark feed", err)
		return
	}
	ids := make([]string, len(refs))
	for i, x := range refs {
		ids[i] = x.ID
	}
	rows, err := s.progress.BookmarksByIDs(r.Context(), uid, ids)
	if err != nil {
		s.serverError(w, r, "api bookmark feed", err)
		return
	}
	items := make([]apiBookmark, 0, len(ids))
	for _, id := range ids {
		if b, ok := rows[id]; ok {
			items = append(items, apiBookmarkOf(b))
		}
	}
	api.Write(w, http.StatusOK, newFeed(items, next, s.now().UnixMilli()))
}

func (s *server) postAPIBookmark(w http.ResponseWriter, r *http.Request) {
	b, ok := s.loadBook(w, r, s.followMerged(r.Context(), r.PathValue("id")), share.ListenBook)
	if !ok {
		return
	}
	var body struct {
		PositionMs *int64  `json:"positionMs"`
		Note       string  `json:"note"`
		ClientID   string  `json:"clientId"`
		EndMs      *int64  `json:"endMs"`
		Kind       *string `json:"kind"`
	}
	if !apiJSONBody(w, r, apiBookmarkBody, &body) {
		return
	}
	if body.PositionMs == nil || *body.PositionMs < 0 {
		apiBadField(w, "positionMs", "The position must be 0 or more milliseconds.")
		return
	}
	pos := *body.PositionMs
	if b.DurationMs > 0 {
		pos = min(pos, b.DurationMs)
	}
	patch, ok := apiClipPatch(w, body.EndMs, body.Kind, b.DurationMs, pos)
	if !ok {
		return
	}
	bm, created, err := s.progress.AddBookmarkWithClientID(r.Context(), currentUser(r).ID, b.ID, pos, body.Note, body.ClientID)
	if err == nil && created && (patch.EndMs != nil || patch.Kind != nil) {
		// One shape to retry: the bookmark is made, then the clip fields are set on it.
		bm, err = s.progress.UpdateBookmark(r.Context(), currentUser(r).ID, bm.ID, patch)
	}
	var inv *progress.InvalidError
	switch {
	case errors.As(err, &inv):
		apiBadField(w, camelField(inv.Field), "That value is not valid.")
	case errors.Is(err, progress.ErrLimit):
		api.Error(w, http.StatusConflict, api.CodeConflict, "This book has the most bookmarks it can hold.")
	case errors.Is(err, progress.ErrClientIDTaken):
		api.ErrorField(w, http.StatusConflict, api.CodeConflict, "clientId", "That id is already used by another bookmark.")
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "add bookmark", err)
	case created:
		api.Write(w, http.StatusCreated, apiBookmarkOf(bm))
	default:
		api.Write(w, http.StatusOK, apiBookmarkOf(bm))
	}
}

func (s *server) patchAPIBookmark(w http.ResponseWriter, r *http.Request) {
	bm, ok := s.ownBookmark(w, r)
	if !ok {
		return
	}
	var body struct {
		Note  *string `json:"note"`
		EndMs *int64  `json:"endMs"`
		Kind  *string `json:"kind"`
	}
	if !apiJSONBody(w, r, apiBookmarkBody, &body) {
		return
	}
	if body.Note == nil && body.EndMs == nil && body.Kind == nil {
		apiBadField(w, "note", "Send the note, the end or the kind. An empty note clears it; an end of 0 clears the end.")
		return
	}
	var dur int64
	if b, err := s.lib.Book(r.Context(), bm.BookID); err == nil {
		dur = b.DurationMs
	}
	patch, ok := apiClipPatch(w, body.EndMs, body.Kind, dur, bm.PositionMs)
	if !ok {
		return
	}
	patch.Note = body.Note
	out, err := s.progress.UpdateBookmark(r.Context(), currentUser(r).ID, bm.ID, patch)
	var inv *progress.InvalidError
	switch {
	case errors.As(err, &inv):
		apiBadField(w, camelField(inv.Field), "That value is not valid.")
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "set bookmark note", err)
	default:
		api.Write(w, http.StatusOK, apiBookmarkOf(out))
	}
}

// apiClipPatch checks the clip fields of a bookmark write and clamps an end past the book to its length. It
// answers 400 with the field itself, and returns false.
func apiClipPatch(w http.ResponseWriter, end *int64, kind *string, durationMs, posMs int64) (progress.BookmarkPatch, bool) {
	p := progress.BookmarkPatch{Kind: kind}
	if kind != nil && !slices.Contains(progress.BookmarkKinds, *kind) {
		apiBadField(w, "kind", "The kind must be bookmark, favourite, quote, note or revisit.")
		return p, false
	}
	if end != nil {
		e := *end
		if durationMs > 0 && e > durationMs {
			e = durationMs
		}
		if e < 0 || (e != 0 && e <= posMs) {
			apiBadField(w, "endMs", "The end must be after the position, or 0 to clear it.")
			return p, false
		}
		p.EndMs = &e
	}
	return p, true
}

func (s *server) deleteAPIBookmark(w http.ResponseWriter, r *http.Request) {
	bm, ok := s.ownBookmark(w, r)
	if !ok {
		return
	}
	err := s.progress.DeleteBookmark(r.Context(), currentUser(r).ID, bm.ID)
	switch {
	case errors.Is(err, progress.ErrNotFound):
		s.notFound(w, r)
	case err != nil:
		s.serverError(w, r, "delete bookmark", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
