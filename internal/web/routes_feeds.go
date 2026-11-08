package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Podcast feeds (decision 0031). A feed token in the path, /feeds/{token}/..., opens one book or series as an RSS
// feed with its audio, cover and chapters, for a podcast app that cannot sign in. GET and HEAD only. The routes sit on
// the outer mux, outside the cookie chain: nothing here reads or sets a cookie, calls currentUser or accepts a bearer
// token, and the owner reaches the handlers only as the explicit feedReq argument. Every request re-checks the owner.

const (
	feedPrefix   = "/feeds/"
	feedMaxBooks = 100
	feedMaxFiles = 1000
)

var errFeedGone = errors.New("feed: nothing to serve")

// feedReq is what a handler gets once the token is accepted: the feed row, its owner and the absolute origin.
type feedReq struct {
	feed  share.Feed
	owner users.User
	base  string // scheme and host of this request
	token string // the raw token from the path, only to write absolute URLs into the feed
}

func (s *server) registerFeeds(outer *http.ServeMux) {
	m := http.NewServeMux()
	m.HandleFunc("GET /feeds/{token}/feed.xml", s.feedRoute(s.getFeedXML))
	m.HandleFunc("GET /feeds/{token}/audio/{file}", s.feedRoute(s.getFeedAudio))
	m.HandleFunc("GET /feeds/{token}/cover/{file}", s.feedRoute(s.getFeedCover))
	m.HandleFunc("GET /feeds/{token}/chapters/{file}", s.feedRoute(s.getFeedChapters))
	m.HandleFunc(feedPrefix, func(w http.ResponseWriter, r *http.Request) { linkMediaNotFound(w) })
	outer.Handle(feedPrefix, m)
}

// feedsFront answers 404 for a /feeds/ path that is not clean, before the mux can redirect it: a redirect's Location
// would repeat the token.
func feedsFront(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rest, ok := strings.CutPrefix(r.URL.Path, feedPrefix); ok {
			segs := strings.Split(strings.TrimSuffix(rest, "/"), "/")
			if slices.Contains(segs, "") && rest != "" || slices.Contains(segs, ".") || slices.Contains(segs, "..") {
				linkMediaNotFound(w)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// feedRoute runs the checks every feed request passes, in order: headers, the address limit (429 before any database
// read), the token (a guess costs the address one failure; a feed that merely ended costs nothing), the owner, the
// per-feed limit and last use. Only then the handler runs.
func (s *server) feedRoute(h func(http.ResponseWriter, *http.Request, feedReq)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		linkHeaders(w)
		ctx := r.Context()
		ip := s.proxies.ClientIP(r).String()
		if ok, wait := s.keyLimiter.Allow(ip); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			http.Error(w, "Too many tries. Wait a little and try again.", http.StatusTooManyRequests)
			return
		}
		feed, reason, err := s.feeds.Lookup(ctx, r.PathValue("token"))
		switch {
		case errors.Is(err, share.ErrFeedEnded):
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		case errors.Is(err, share.ErrFeedUnknown):
			s.feedGuess(w, r, ip, reason)
			return
		case err != nil:
			s.feedServerError(w, "feed lookup", err)
			return
		}
		owner, err := s.users.ByID(ctx, feed.UserID)
		if errors.Is(err, users.ErrNotFound) || err == nil && owner.Disabled {
			http.Error(w, "Not found.", http.StatusNotFound)
			return
		}
		if err != nil {
			s.feedServerError(w, "feed owner", err)
			return
		}
		key := "feed:" + feed.ID
		if ok, wait := s.apiLimiter.Allow(key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(max(1, int((wait+time.Second-1)/time.Second))))
			http.Error(w, "Too many requests from this app. Wait a little and try again.", http.StatusTooManyRequests)
			return
		}
		s.apiLimiter.Fail(key)
		s.feeds.Touch(ctx, feed)
		scheme := "http://"
		if s.proxies.Secure(r) {
			scheme = "https://"
		}
		h(w, r, feedReq{feed: feed, owner: owner, base: scheme + r.Host, token: r.PathValue("token")})
	}
}

// feedGuess answers a malformed or unknown token: one failure on the address and one audit row, never the token.
func (s *server) feedGuess(w http.ResponseWriter, r *http.Request, ip, reason string) {
	s.keyLimiter.Fail(ip)
	err := s.lib.Store().WriteTx(r.Context(), func(tx *sql.Tx) error {
		return db.Audit(r.Context(), tx, db.AuditEntry{At: s.now().UnixMilli(), Action: "feed.failed",
			Detail: map[string]string{"ip": ip, "reason": reason}})
	})
	if err != nil {
		slog.Error("feed audit", "err", err)
	}
	http.Error(w, "Not found.", http.StatusNotFound)
}

func (s *server) feedServerError(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err) // never the token
	http.Error(w, "Something went wrong on our side.", http.StatusInternalServerError)
}

// feedFail answers an error from a feed helper: gone is the plain 404, anything else is a logged 500.
func (s *server) feedFail(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, errFeedGone) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	s.feedServerError(w, what, err)
}

// feedScope is what the owner may download, for series listings filtered in SQL.
func (s *server) feedScope(u users.User) library.Scope {
	return library.Scope{Books: s.can.BookFilterFor(u, "b", share.DownloadBook), Libraries: s.can.LibraryFilter(u, "l")}
}

// feedListable reports whether a book may be listed by a feed at all: not refused, not missing.
func feedListable(b library.Book) bool { return b.RefusedReason == "" && b.MissingSince == 0 }

// feedBookFor loads a book the feed carries and the owner may download, or errFeedGone. A book feed carries only its
// own book; a series feed carries the books that belong to its series.
func (s *server) feedBookFor(ctx context.Context, fr feedReq, bookID string) (library.Book, error) {
	if fr.feed.BookID != "" && bookID != fr.feed.BookID {
		return library.Book{}, errFeedGone
	}
	book, err := s.lib.Book(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		return library.Book{}, errFeedGone
	}
	if err != nil {
		return library.Book{}, err
	}
	if fr.feed.SeriesID != "" {
		links, err := s.lib.BookSeries(ctx, bookID)
		if err != nil {
			return library.Book{}, err
		}
		if !slices.ContainsFunc(links, func(l library.BookSeriesLink) bool { return l.SeriesID == fr.feed.SeriesID }) {
			return library.Book{}, errFeedGone
		}
	}
	if !feedListable(book) || !s.can.Can(ctx, fr.owner, share.DownloadBook, share.Target{Kind: share.TargetBook, ID: book.ID, LibraryID: book.LibraryID}) {
		return library.Book{}, errFeedGone
	}
	return book, nil
}

// feedBooks returns the books of the feed in order. A series is listed by SQL filtered with the download right.
func (s *server) feedBooks(ctx context.Context, fr feedReq) ([]library.Book, error) {
	if fr.feed.BookID != "" {
		b, err := s.feedBookFor(ctx, fr, fr.feed.BookID)
		if err != nil {
			return nil, err
		}
		return []library.Book{b}, nil
	}
	ids, err := s.lib.SeriesFeedBooks(ctx, s.feedScope(fr.owner), fr.feed.SeriesID, feedMaxBooks)
	if err != nil {
		return nil, err
	}
	var out []library.Book
	for _, id := range ids {
		b, err := s.lib.Book(ctx, id)
		if errors.Is(err, library.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, errFeedGone
	}
	return out, nil
}

// feedFiles keeps the files a feed may list or serve: the streamable rule and present on disk at the last scan.
func feedFiles(book library.Book, files []library.AudioFile) []library.AudioFile {
	var out []library.AudioFile
	for _, f := range files {
		if streamable(book, f) && f.MissingSince == 0 {
			out = append(out, f)
		}
	}
	return out
}

// feedSegment drops the file extension a feed URL carries for the podcast app's sake.
func feedSegment(v string) string {
	if i := strings.LastIndexByte(v, '.'); i >= 0 {
		return v[:i]
	}
	return v
}

func (s *server) getFeedXML(w http.ResponseWriter, r *http.Request, fr feedReq) {
	ctx := r.Context()
	books, err := s.feedBooks(ctx, fr)
	if err != nil {
		s.feedFail(w, "feed books", err)
		return
	}
	doc, err := s.buildFeed(ctx, fr, books)
	if err != nil {
		s.feedFail(w, "feed build", err)
		return
	}
	writeFeedXML(w, r, doc)
}

func (s *server) getFeedAudio(w http.ResponseWriter, r *http.Request, fr feedReq) {
	ctx := r.Context()
	f, err := s.lib.AudioFile(ctx, feedSegment(r.PathValue("file")))
	if errors.Is(err, library.ErrNotFound) {
		s.feedFail(w, "", errFeedGone)
		return
	}
	if err != nil {
		s.feedFail(w, "feed audio file", err)
		return
	}
	book, err := s.feedBookFor(ctx, fr, f.BookID)
	if err != nil {
		s.feedFail(w, "feed audio book", err)
		return
	}
	if !streamable(book, f) || f.MissingSince != 0 {
		s.feedFail(w, "", errFeedGone)
		return
	}
	s.serveAudio(w, r, f, false, "private, no-store")
}

func (s *server) getFeedCover(w http.ResponseWriter, r *http.Request, fr feedReq) {
	book, err := s.feedBookFor(r.Context(), fr, feedSegment(r.PathValue("file")))
	if err != nil {
		s.feedFail(w, "feed cover book", err)
		return
	}
	s.serveCoverFile(w, r, book.CoverID, 0, "private, no-store")
}

func (s *server) getFeedChapters(w http.ResponseWriter, r *http.Request, fr feedReq) {
	ctx := r.Context()
	f, err := s.lib.AudioFile(ctx, feedSegment(r.PathValue("file")))
	if errors.Is(err, library.ErrNotFound) {
		s.feedFail(w, "", errFeedGone)
		return
	}
	if err != nil {
		s.feedFail(w, "feed chapters file", err)
		return
	}
	book, err := s.feedBookFor(ctx, fr, f.BookID)
	if err != nil {
		s.feedFail(w, "feed chapters book", err)
		return
	}
	if !streamable(book, f) || f.MissingSince != 0 {
		s.feedFail(w, "", errFeedGone)
		return
	}
	chapters, err := s.lib.Chapters(ctx, book.ID)
	if err != nil {
		s.feedFail(w, "feed chapters", err)
		return
	}
	out := feedChapterJSON{Version: "1.2.0", Chapters: []feedChapterEntry{}}
	for _, c := range feedFileChapters(chapters, f) {
		out.Chapters = append(out.Chapters, feedChapterEntry{StartTime: json.Number(strconv.FormatFloat(float64(max(0, c.StartMs-f.OffsetMs))/1000, 'f', 3, 64)), Title: c.Title})
	}
	body, err := json.Marshal(out)
	if err != nil {
		s.feedFail(w, "feed chapters json", err)
		return
	}
	w.Header().Set("Content-Type", "application/json+chapters")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(body)
}

// feedFileChapters are the chapters that overlap the file's span of the book's timeline.
func feedFileChapters(chapters []library.Chapter, f library.AudioFile) []library.Chapter {
	var out []library.Chapter
	for _, c := range chapters {
		if c.StartMs < f.OffsetMs+f.DurationMs && c.EndMs > f.OffsetMs {
			out = append(out, c)
		}
	}
	return out
}

type feedChapterJSON struct {
	Version  string             `json:"version"`
	Chapters []feedChapterEntry `json:"chapters"`
}

type feedChapterEntry struct {
	StartTime json.Number `json:"startTime"`
	Title     string      `json:"title"`
}
