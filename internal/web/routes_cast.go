package web

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Casting (decision 0047). A Cast receiver cannot send a cookie, so a signed-in person who turned casting on mints a
// short-lived token for one book or episode and hands its address to the TV: /cast/{token}/... Like podcast feeds
// (routes_feeds.go) the fetch routes sit on the outer mux, outside the cookie chain: nothing here reads or sets a
// cookie, calls currentUser or accepts a bearer token, and the owner reaches a handler only as the explicit castReq
// argument. GET and HEAD only. Every request re-checks that the owner still has casting on and still has the right.

const castPrefix = "/cast/"

// castReq is what a handler gets once the token is accepted: the token row and its owner.
type castReq struct {
	tok   share.CastToken
	owner users.User
}

func (s *server) registerCast(outer *http.ServeMux) {
	m := http.NewServeMux()
	// No method in the patterns: castRoute answers any method but GET and HEAD with 405 and Allow itself.
	m.HandleFunc("/cast/{token}/audio/{fileID}", s.castRoute(share.CastBook, s.getCastAudio))
	m.HandleFunc("/cast/{token}/episode", s.castRoute(share.CastEpisode, s.getCastEpisode))
	m.HandleFunc("/cast/{token}/art", s.castAnyRoute(s.getCastArt))
	m.HandleFunc(castPrefix, func(w http.ResponseWriter, r *http.Request) { castNotFound(w) })
	outer.Handle(castPrefix, m)
}

// castHeaders are on every /cast/ answer. The Allow-Origin header is a guess that a receiver may need it; it is harmless
// because the address is the credential and no credentials header is sent.
func castHeaders(w http.ResponseWriter) {
	h := w.Header()
	linkHeaders(w)
	h.Set("Cache-Control", "private, no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Access-Control-Allow-Origin", "*")
}

func castNotFound(w http.ResponseWriter) {
	castHeaders(w)
	http.Error(w, "Not found.", http.StatusNotFound)
}

// castRoute runs the checks every cast request passes, in order: method, headers, the address limit (429 before any
// database read), the token (a guess costs the address one failure; an expired token costs nothing), the owner, the
// owner's casting switch and the token's kind. Only then the handler runs.
func (s *server) castRoute(kind string, h func(http.ResponseWriter, *http.Request, castReq)) http.HandlerFunc {
	return s.castGate(func(k string) bool { return k == kind }, h)
}

// castAnyRoute is castRoute for a handler that serves more than one kind of token (the artwork).
func (s *server) castAnyRoute(h func(http.ResponseWriter, *http.Request, castReq)) http.HandlerFunc {
	return s.castGate(func(k string) bool { return k == share.CastBook || k == share.CastEpisode }, h)
}

func (s *server) castGate(kindOK func(string) bool, h func(http.ResponseWriter, *http.Request, castReq)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		castHeaders(w)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed.", http.StatusMethodNotAllowed)
			return
		}
		ctx := r.Context()
		ip := s.proxies.ClientIP(r).String()
		if ok, wait := s.keyLimiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			http.Error(w, "Too many tries. Wait a little and try again.", http.StatusTooManyRequests)
			return
		}
		tok, reason, err := s.casts.Lookup(ctx, r.PathValue("token"))
		switch {
		case errors.Is(err, share.ErrCastExpired):
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		case errors.Is(err, share.ErrCastUnknown):
			s.castGuess(w, r, ip, reason)
			return
		case err != nil:
			s.castServerError(w, "cast lookup", err)
			return
		}
		owner, err := s.users.ByID(ctx, tok.UserID)
		if errors.Is(err, users.ErrNotFound) || err == nil && owner.Disabled {
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		}
		if err != nil {
			s.castServerError(w, "cast owner", err)
			return
		}
		set, err := s.users.LoadSettings(ctx, owner.ID)
		if err != nil {
			s.castServerError(w, "cast owner settings", err)
			return
		}
		if !set.Cast || !kindOK(tok.Kind) {
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		}
		h(w, r, castReq{tok: tok, owner: owner})
	}
}

// castGuess answers a malformed or unknown token: one failure on the address and one audit row, never the token.
func (s *server) castGuess(w http.ResponseWriter, r *http.Request, ip, reason string) {
	s.keyLimiter.Fail(ip)
	err := s.lib.Store().WriteTx(r.Context(), func(tx *sql.Tx) error {
		return db.Audit(r.Context(), tx, db.AuditEntry{At: s.now().UnixMilli(), Action: "cast.failed",
			Detail: map[string]string{"ip": ip, "reason": reason}})
	})
	if err != nil {
		slog.Error("cast audit", "err", err)
	}
	http.Error(w, "Not found.", http.StatusNotFound)
}

func (s *server) castServerError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err) // never the token
	http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
}

// castBook loads the book of a book token if its owner may still listen to it, else writes the plain 404.
func (s *server) castBook(w http.ResponseWriter, r *http.Request, cr castReq) (library.Book, bool) {
	b, err := s.playableFor(r.Context(), cr.owner, cr.tok.TargetID)
	if errors.Is(err, library.ErrNotFound) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return library.Book{}, false
	}
	if err != nil {
		s.castServerError(w, "cast book", err)
		return library.Book{}, false
	}
	return b, true
}

// castEpisode loads the episode of an episode token if podcasts are on and its owner may still listen to it.
func (s *server) castEpisode(w http.ResponseWriter, r *http.Request, cr castReq) (podcast.Episode, bool) {
	ctx := r.Context()
	if !s.podcastsOn(r) || !s.can.Can(ctx, cr.owner, share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: cr.tok.TargetID}) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return podcast.Episode{}, false
	}
	ep, err := s.podcasts.Episode(ctx, cr.owner.ID, cr.tok.TargetID)
	if errors.Is(err, podcast.ErrNotFound) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return podcast.Episode{}, false
	}
	if err != nil {
		s.castServerError(w, "cast episode", err)
		return podcast.Episode{}, false
	}
	return ep, true
}

func (s *server) getCastAudio(w http.ResponseWriter, r *http.Request, cr castReq) {
	ctx := r.Context()
	book, ok := s.castBook(w, r, cr)
	if !ok {
		return
	}
	f, err := s.lib.AudioFile(ctx, r.PathValue("fileID"))
	if errors.Is(err, library.ErrNotFound) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	if err != nil {
		s.castServerError(w, "cast audio file", err)
		return
	}
	if f.BookID != book.ID || !streamable(book, f) || f.MissingSince != 0 {
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	compat, ok := compatParam(w, r)
	if !ok {
		return
	}
	s.serveAudio(w, r, f, compat, "private, no-store")
}

func (s *server) getCastEpisode(w http.ResponseWriter, r *http.Request, cr castReq) {
	ep, ok := s.castEpisode(w, r, cr)
	if !ok {
		return
	}
	s.serveEpisodeAudio(w, r, ep, cr.owner.ID, false)
}

func (s *server) getCastArt(w http.ResponseWriter, r *http.Request, cr castReq) {
	if cr.tok.Kind == share.CastEpisode {
		ep, ok := s.castEpisode(w, r, cr)
		if !ok {
			return
		}
		s.serveShowArt(w, r, cr.owner.ID, ep.ShowID)
		return
	}
	book, ok := s.castBook(w, r, cr)
	if !ok {
		return
	}
	s.serveCoverFile(w, r, book.CoverID, 0, "private, no-store")
}

// postCastAddress answers POST /casting/address (cookie chain, CSRF header): it mints a token for a book or episode the
// signed-in person may listen to and answers where the TV can fetch it. Order: form type, the casting switch, the
// fields, the right on the target, then whether a TV could reach this address at all.
func (s *server) postCastAddress(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		jsonError(w, http.StatusUnsupportedMediaType, "unsupported", "")
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "server", "")
		return
	}
	if !set.Cast || shellFrom(r) != "" {
		jsonError(w, http.StatusNotFound, "not_found", "")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, progressBodyLimit)
	if err := r.ParseForm(); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid", "")
		return
	}
	kind, id := r.PostForm.Get("kind"), r.PostForm.Get("id")
	if kind != share.CastBook && kind != share.CastEpisode {
		jsonError(w, http.StatusBadRequest, "invalid", "kind")
		return
	}
	if id == "" || len(id) > 64 {
		jsonError(w, http.StatusBadRequest, "invalid", "id")
		return
	}
	var life time.Duration
	switch kind {
	case share.CastBook:
		b, err := s.playableFor(ctx, u, id)
		if errors.Is(err, library.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "")
			return
		}
		if err != nil {
			slog.Error("cast mint book", "err", err)
			jsonError(w, http.StatusInternalServerError, "server", "")
			return
		}
		id, life = b.ID, share.CastLife(b.DurationMs)
	default:
		if !s.podcastsOn(r) || !s.can.Can(ctx, u, share.ListenEpisode, share.Target{Kind: share.TargetEpisode, ID: id}) {
			jsonError(w, http.StatusNotFound, "not_found", "")
			return
		}
		ep, err := s.podcasts.Episode(ctx, u.ID, id)
		if errors.Is(err, podcast.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "not_found", "")
			return
		}
		if err != nil {
			slog.Error("cast mint episode", "err", err)
			jsonError(w, http.StatusInternalServerError, "server", "")
			return
		}
		life = share.CastLife(ep.DurationMs)
	}
	secure := s.proxies.Secure(r)
	if !secure || !reachableHost(r.Host) {
		jsonError(w, http.StatusConflict, "unreachable", "")
		return
	}
	tok, expires, err := s.casts.Mint(ctx, u.ID, kind, id, life)
	if err != nil {
		slog.Error("cast mint", "err", err) // never the token
		jsonError(w, http.StatusInternalServerError, "server", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"base": "https://" + r.Host + castPrefix + tok, "expiresAt": expires})
}
