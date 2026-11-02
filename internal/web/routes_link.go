package web

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Share links. /s/{token} and the three routes under it are public (see gate.go) and never read the
// session: the token in the path is the whole credential, checked against the row on every request,
// and it reaches one book. A link that is unknown, malformed, revoked, expired, whose maker is no
// longer staff, or whose book is gone all give one and the same page, so a link says nothing about
// why it fails. A link with a password hands out nothing (title, cover, audio) until it is unlocked
// with the ts_link cookie.

func (s *server) registerLinks(mux *http.ServeMux) {
	mux.HandleFunc("GET /s/{token}", s.getLink)
	mux.HandleFunc("POST /s/{token}", s.postLink)
	mux.HandleFunc("GET /s/{token}/stream/{audioFileID}", s.getLinkStream)
	mux.HandleFunc("GET /s/{token}/peaks/{audioFileID}", s.getLinkPeaks)
	mux.HandleFunc("GET /s/{token}/cover", s.getLinkCover)
}

// linkHeaders keeps the token out of referrers, search indexes and caches.
func linkHeaders(w http.ResponseWriter) { inviteHeaders(w) }

const (
	linkCookie     = "ts_link"
	linkCookieLife = 7 * 24 * time.Hour
	linkMaxPW      = 1024
)

// linkFrame renders a share-link page: the sign-in frame, plus the player when data is set. It never
// puts a person's name or token-bound state in the page; base is "" for the pages that name no link.
func (s *server) linkFrame(w http.ResponseWriter, r *http.Request, status int, title, base string, data *components.PlayerDataView, body templ.Component) {
	m := playerMsgs()
	m.Link = true
	var buf bytes.Buffer
	err := components.LinkShell(components.Page{
		Title:  title,
		Theme:  themeFrom(r), // the cookie only: a link page never reads the session
		Assets: s.assets,
		Styles: pages.InviteStyles,
		Player: m,
	}, base, data, body).Render(r.Context(), &buf)
	if err != nil {
		slog.Error("render link page", "path", logPath(r), "err", err)
		http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		return
	}
	linkHeaders(w)
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// linkBroken is the one answer to every link that does not work, whatever the reason.
func (s *server) linkBroken(w http.ResponseWriter, r *http.Request) {
	s.linkFrame(w, r, http.StatusNotFound, linkBrokenTitle, "", nil,
		pages.InviteState(pages.InviteStateView{Title: linkBrokenTitle, Body: linkBrokenBody}))
}

func (s *server) linkLimited(w http.ResponseWriter, r *http.Request, rl *auth.RateLimitedError) {
	body := tooMany(w, rl)
	s.linkFrame(w, r, http.StatusTooManyRequests, linkWaitTitle, "", nil,
		pages.InviteState(pages.InviteStateView{Title: linkWaitTitle, Body: body}))
}

// linkMediaNotFound is the bare 404 of the routes a player calls.
func linkMediaNotFound(w http.ResponseWriter) {
	linkHeaders(w)
	http.Error(w, "Not found.", http.StatusNotFound)
}

// linkFor finds the link a request names and the book it opens, or has answered. Unknown and
// malformed tokens cost the address one failure on the sign-in limiter; a link that merely ended
// does not (a revoked link in an open tab must not lock a family out of sign-in). A link with a
// password that is not unlocked yet is only returned when needUnlocked is false, so the page can
// ask for the password; a link whose password was guessed too often answers 429. needUnlocked is
// also the routes a player calls: they answer with bare text, not pages.
func (s *server) linkFor(w http.ResponseWriter, r *http.Request, needUnlocked bool) (share.Link, library.Book, bool) {
	ctx := r.Context()
	linkHeaders(w)
	deny := func() (share.Link, library.Book, bool) {
		if needUnlocked {
			linkMediaNotFound(w)
		} else {
			s.linkBroken(w, r)
		}
		return share.Link{}, library.Book{}, false
	}
	ip := s.proxies.ClientIP(r)
	limited := func(err error) bool {
		var rl *auth.RateLimitedError
		if !errors.As(err, &rl) {
			return false
		}
		if needUnlocked {
			tooMany(w, rl)
			http.Error(w, "Too many tries.", http.StatusTooManyRequests)
		} else {
			s.linkLimited(w, r, rl)
		}
		return true
	}
	if limited(s.auth.LinkAllowed(ip, "")) {
		return share.Link{}, library.Book{}, false
	}
	l, err := s.links.Lookup(ctx, r.PathValue("token"))
	if errors.Is(err, share.ErrLinkUnknown) {
		s.auth.LinkFailed(ctx, ip, "", "unknown")
		return deny()
	}
	if err != nil {
		slog.Error("link lookup", "err", err)
		if needUnlocked {
			http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		} else {
			s.errorPage(w, r, http.StatusInternalServerError)
		}
		return share.Link{}, library.Book{}, false
	}
	book, err := s.lib.Book(ctx, l.BookID)
	if err != nil && !errors.Is(err, library.ErrNotFound) {
		slog.Error("link book", "err", err)
		return deny()
	}
	if err != nil || book.RefusedReason != "" || book.MissingSince != 0 ||
		!s.can.CanLink(l, share.ViewBook, share.Target{Kind: share.TargetBook, ID: book.ID}) {
		return deny()
	}
	if l.HasPassword() && !s.linkUnlocked(r, l) {
		if limited(s.auth.LinkAllowed(ip, l.ID)) {
			return share.Link{}, library.Book{}, false
		}
		if needUnlocked {
			return deny()
		}
	}
	return l, book, true
}

// linkMAC signs the unlock cookie: it binds the link, its password hash (so a changed password
// would lock the cookie out) and the end time.
func (s *server) linkMAC(l share.Link, expMs string) string {
	m := hmac.New(sha256.New, s.linkKey)
	m.Write([]byte("v1|" + l.ID + "|" + l.PasswordHash + "|" + expMs))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// setLinkCookie remembers that this browser knows the link's password: HttpOnly, SameSite=Lax (a
// Strict cookie is dropped when the link is opened from a chat app), scoped to the link's own path,
// ending with the link or in seven days.
func (s *server) setLinkCookie(w http.ResponseWriter, r *http.Request, l share.Link) {
	now := s.now()
	end := now.Add(linkCookieLife)
	if exp := time.UnixMilli(l.ExpiresAt); exp.Before(end) {
		end = exp
	}
	expMs := strconv.FormatInt(end.UnixMilli(), 10)
	http.SetCookie(w, &http.Cookie{
		Name: linkCookie, Value: expMs + "." + s.linkMAC(l, expMs), Path: linkPrefix + r.PathValue("token"),
		HttpOnly: true, Secure: s.proxies.Secure(r), SameSite: http.SameSiteLaxMode, MaxAge: int(end.Sub(now).Seconds()),
	})
}

// linkUnlocked reports whether the request carries a valid, unexpired unlock cookie for this link.
func (s *server) linkUnlocked(r *http.Request, l share.Link) bool {
	c, err := r.Cookie(linkCookie)
	if err != nil {
		return false
	}
	expMs, mac, ok := strings.Cut(c.Value, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expMs, 10, 64)
	if err != nil || exp <= s.now().UnixMilli() {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(s.linkMAC(l, expMs)))
}

func (s *server) linkLocked(w http.ResponseWriter, r *http.Request, status int, msg string) {
	base := linkPrefix + r.PathValue("token")
	s.linkFrame(w, r, status, linkLockedTitle, base, nil, pages.LinkLocked(pages.LinkLockedView{
		CSRF: s.csrf.TokenFrom(r.Context()), Action: base, ServerName: s.serverName(r.Context()),
		Heading: linkLockedTitle, Label: linkPasswordLabel, Submit: linkUnlock, Error: msg,
	}))
}

func (s *server) getLink(w http.ResponseWriter, r *http.Request) {
	l, book, ok := s.linkFor(w, r, false)
	if !ok {
		return
	}
	if l.HasPassword() && !s.linkUnlocked(r, l) {
		s.linkLocked(w, r, http.StatusOK, "")
		return
	}
	ctx := r.Context()
	base := linkPrefix + r.PathValue("token")
	books, libs := share.LinkScope(l)
	cards, err := s.lib.CardsByID(ctx, library.Scope{Books: books, Libraries: libs}, []string{book.ID})
	if err != nil {
		s.serverError(w, r, "link book", err)
		return
	}
	files, err := s.lib.AudioFiles(ctx, book.ID)
	if err != nil {
		s.serverError(w, r, "link files", err)
		return
	}
	pl := playLinkIn(book, files, nil)
	if len(cards) == 0 || pl == nil {
		s.linkBroken(w, r)
		return
	}
	data, err := s.linkPlayerView(ctx, l, book, base)
	if err != nil {
		s.serverError(w, r, "link player", err)
		return
	}
	cv := cardView(cards[0])
	if cv.CoverSrc != "" {
		cv.CoverSrc = base + "/cover?w=640"
	}
	cv.CoverSrcset = ""
	v := pages.LinkView{
		ServerName: s.serverName(ctx), Title: cv.Title, Author: cv.Byline, Length: cv.Duration, Cover: cv,
		Ends: fmt.Sprintf(linkEnds, time.UnixMilli(l.ExpiresAt).In(s.now().Location()).Format("2 January")), Fine: linkFine,
		Play: components.PlayButtonView{
			BookID: book.ID, FileID: pl.FileID, Mime: pl.Mime, Href: base + "/stream/" + pl.FileID + "#t=0", Label: linkPlayLabel,
			FileStartMs: pl.FileStartMs, StartMs: pl.StartMs, MayNeedCompat: pl.MayNeedCompat,
		},
	}
	v.BandTint, v.BandClass = bandClasses(cv.Tint)
	s.linkFrame(w, r, http.StatusOK, cv.Title, base, &data, pages.LinkPage(v))
}

func (s *server) postLink(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	l, _, ok := s.linkFor(w, r, false)
	if !ok {
		return
	}
	base := linkPrefix + r.PathValue("token")
	if !l.HasPassword() || s.linkUnlocked(r, l) {
		http.Redirect(w, r, base, http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	pw := r.PostForm.Get("password")
	good := false
	if pw != "" && len(pw) <= linkMaxPW {
		var err error
		if good, err = s.users.VerifyPassword(ctx, l.PasswordHash, pw); err != nil {
			s.serverError(w, r, "link password", err)
			return
		}
	}
	if !good {
		s.auth.LinkFailed(ctx, s.proxies.ClientIP(r), l.ID, "password")
		s.linkLocked(w, r, http.StatusUnprocessableEntity, linkWrongPassword)
		return
	}
	s.auth.LinkUnlocked(l.ID)
	s.setLinkCookie(w, r, l)
	http.Redirect(w, r, base, http.StatusSeeOther)
}

// linkFile loads the audio file a player asked for on a link: it must belong to the link's book,
// the link must allow listening to it and the file must be one /stream would serve. Anything else is
// the same bare 404.
func (s *server) linkFile(w http.ResponseWriter, r *http.Request) (library.AudioFile, bool) {
	l, book, ok := s.linkFor(w, r, true)
	if !ok {
		return library.AudioFile{}, false
	}
	f, err := s.lib.AudioFile(r.Context(), r.PathValue("audioFileID"))
	switch {
	case errors.Is(err, library.ErrNotFound):
		linkMediaNotFound(w)
		return f, false
	case err != nil:
		slog.Error("link audio file", "err", err)
		http.Error(w, "Something went wrong on your server.", http.StatusInternalServerError)
		return f, false
	}
	if f.BookID != l.BookID || !s.can.CanLink(l, share.ListenBook, share.Target{Kind: share.TargetBook, ID: f.BookID}) || !streamable(book, f) {
		linkMediaNotFound(w)
		return f, false
	}
	return f, true
}

func (s *server) getLinkStream(w http.ResponseWriter, r *http.Request) {
	f, ok := s.linkFile(w, r)
	if !ok {
		return
	}
	compat, ok := compatParam(w, r)
	if !ok {
		return
	}
	s.serveAudio(w, r, f, compat, "private, no-store")
}

func (s *server) getLinkPeaks(w http.ResponseWriter, r *http.Request) {
	f, ok := s.linkFile(w, r)
	if !ok {
		return
	}
	if !s.peaks.Enabled() {
		s.peaksNotFound(w, "")
		return
	}
	s.servePeaks(w, r, f, "private, no-store")
}

func (s *server) getLinkCover(w http.ResponseWriter, r *http.Request) {
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCoverWidth {
			linkHeaders(w)
			http.Error(w, "Bad width.", http.StatusBadRequest)
			return
		}
		width = n
	}
	_, book, ok := s.linkFor(w, r, true)
	if !ok {
		return
	}
	s.serveCoverFile(w, r, book.CoverID, width, "private, no-store")
}
