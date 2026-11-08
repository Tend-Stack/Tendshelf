package web

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// The pages that make and turn off podcast feeds (decision 0031). The feed itself is in routes_feeds.go. The
// address is shown once, in the answer to the POST; only its SHA-256 is stored, and it never reaches the audit
// detail, a log or a redirect.

func (s *server) registerFeedPages(mux *http.ServeMux) {
	mux.HandleFunc("GET /books/{id}/feed", s.getBookFeed)
	mux.HandleFunc("POST /books/{id}/feed", s.postBookFeed)
	mux.HandleFunc("GET /series/{id}/feed", s.getSeriesFeed)
	mux.HandleFunc("POST /series/{id}/feed", s.postSeriesFeed)
	mux.HandleFunc("POST /me/feeds/{id}/revoke", s.postFeedRevoke)
}

// feedTarget checks that the signed-in person may make a feed for the book or series named in the path. It has
// answered 404 when false. bookID or seriesID is set, never both.
func (s *server) feedTarget(w http.ResponseWriter, r *http.Request, series bool) (bookID, seriesID string, ok bool) {
	id := r.PathValue("id")
	if series {
		ids, err := s.lib.SeriesFeedBooks(r.Context(), s.feedScope(currentUser(r)), id, 1)
		if err != nil {
			s.serverError(w, r, "series feed", err)
			return "", "", false
		}
		if len(ids) == 0 {
			s.notFound(w, r)
			return "", "", false
		}
		return "", id, true
	}
	book, ok := s.loadBook(w, r, id, share.DownloadBook)
	if !ok {
		return "", "", false
	}
	files, err := s.lib.AudioFiles(r.Context(), book.ID)
	if err != nil {
		s.serverError(w, r, "book feed", err)
		return "", "", false
	}
	if !feedListable(book) || len(feedFiles(book, files)) == 0 {
		s.notFound(w, r)
		return "", "", false
	}
	return book.ID, "", true
}

func (s *server) feedMakeView(r *http.Request, series bool) pages.FeedMakeView {
	id := r.PathValue("id")
	v := pages.FeedMakeView{
		CSRF: s.csrf.TokenFrom(r.Context()), Title: feedTitle, Lead: feedLeadBook, Privacy: feedPrivacy,
		NameLabel: akeyNameLabel, NameHint: feedNameHint, Make: feedMake, ListNote: feedListNote, DevicesLabel: feedDevicesLink,
		BackHref: "/books/" + id, BackLabel: feedBackBook, Action: "/books/" + id + "/feed",
	}
	if series {
		v.Lead, v.BackHref, v.BackLabel, v.Action = feedLeadSeries, "/series/"+id, feedBackSeries, "/series/"+id+"/feed"
	}
	return v
}

func (s *server) getBookFeed(w http.ResponseWriter, r *http.Request)   { s.getFeedMake(w, r, false) }
func (s *server) getSeriesFeed(w http.ResponseWriter, r *http.Request) { s.getFeedMake(w, r, true) }
func (s *server) postBookFeed(w http.ResponseWriter, r *http.Request)  { s.postFeed(w, r, false) }
func (s *server) postSeriesFeed(w http.ResponseWriter, r *http.Request) {
	s.postFeed(w, r, true)
}

func (s *server) getFeedMake(w http.ResponseWriter, r *http.Request, series bool) {
	if _, _, ok := s.feedTarget(w, r, series); !ok {
		return
	}
	s.pageStyled(w, r, http.StatusOK, feedTitle, "library", pages.FeedMake(s.feedMakeView(r, series)), nil, append(pages.MeStyles, pages.AppKeyStyles...)...)
}

// postFeed makes a feed and answers 200 with its address, never cached and never a redirect.
func (s *server) postFeed(w http.ResponseWriter, r *http.Request, series bool) {
	bookID, seriesID, ok := s.feedTarget(w, r, series)
	if !ok {
		return
	}
	u := currentUser(r)
	var tok string
	err := s.lib.Store().WriteTx(r.Context(), func(tx *sql.Tx) error {
		var f share.Feed
		var err error
		tok, f, err = s.feeds.Create(r.Context(), tx, u.ID, r.PostFormValue("name"), bookID, seriesID)
		if err != nil {
			return err
		}
		return db.Audit(r.Context(), tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: u.ID, Action: "feed.create", Target: u.ID,
			Detail: map[string]string{"feed": f.ID, "target": f.Target()}}) // never the address
	})
	switch {
	case errors.Is(err, share.ErrTooManyFeeds):
		s.keyError(w, r, http.StatusConflict, feedLimit)
		return
	case err != nil:
		s.serverError(w, r, "make feed", err)
		return
	}
	scheme := "http://"
	if s.proxies.Secure(r) {
		scheme = "https://"
	}
	m := s.feedMakeView(r, series)
	addr := feedRoot(scheme+r.Host, tok) + "feed.xml"
	v := pages.FeedShownView{
		Title: feedShownTitle, Lead: akeyShownLead, Note: feedNote, AddressLabel: feedAddress, Address: addr,
		QRNote: feedQRNote, BackHref: m.BackHref, BackLabel: m.BackLabel, CopyLabel: feedCopy,
	}
	if reachableHost(r.Host) {
		if q, ok := qrView(addr, feedQRLabel); ok {
			v.QR = &q
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	s.pageStyled(w, r, http.StatusOK, feedShownTitle, "library", pages.FeedShown(v), nil, append(pages.MeStyles, pages.AppKeyStyles...)...)
}

// postFeedRevoke turns off one of the signed-in person's own feeds. Another person's feed is a 404.
func (s *server) postFeedRevoke(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	err := s.lib.Store().WriteTx(r.Context(), func(tx *sql.Tx) error {
		f, err := s.feeds.Revoke(r.Context(), tx, u.ID, r.PathValue("id"))
		if err != nil {
			return err
		}
		return db.Audit(r.Context(), tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: u.ID, Action: "feed.revoke", Target: u.ID,
			Detail: map[string]string{"feed": f.ID, "target": f.Target()}})
	})
	switch {
	case errors.Is(err, share.ErrFeedUnknown):
		s.notFound(w, r)
		return
	case err != nil:
		s.serverError(w, r, "turn off feed", err)
		return
	}
	http.Redirect(w, r, "/me/devices", http.StatusSeeOther)
}

// feedsView is the Podcast feeds section for the signed-in person's own feeds.
func (s *server) feedsView(r *http.Request) (pages.FeedsView, error) {
	u := currentUser(r)
	rows, err := s.feeds.Own(r.Context(), u.ID)
	if err != nil {
		return pages.FeedsView{}, err
	}
	v := pages.FeedsView{CSRF: s.csrf.TokenFrom(r.Context()), Heading: feedsHeading, Lead: feedsLead, None: feedsNone, TurnOff: akeyTurnOff}
	now := s.now()
	for _, f := range rows {
		title := f.TargetName
		if title == "" {
			title = feedGone
		}
		kind := feedKindBook
		if f.SeriesID != "" {
			kind = feedKindSeries
		}
		used := msgNotUsedYet
		if f.LastUsedAt > 0 {
			used = agoText(now, time.UnixMilli(f.LastUsedAt))
		}
		meta := joinNonEmpty(" · ", f.Name, kind, used)
		v.Lines = append(v.Lines, pages.AppKeyLine{Title: title, Meta: meta, Action: "/me/feeds/" + f.ID + "/revoke", Aria: akeyTurnOff + " " + title})
	}
	return v, nil
}
