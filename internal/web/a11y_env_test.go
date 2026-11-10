//go:build dev

package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// a11yEnv is everything the audit's page table needs: the ids and tokens of the seeded data, and one
// server for each fixture and role. It reuses the seeds of the visual tests (bookVisualSeed,
// shelfVisualSeed, queueVisualSeed's queue, adminSeed, sharedHomeSeed's member, uploadVisualApp,
// playerApp); it adds no data of its own except a member grant, an invite and a share link.
type a11yEnv struct {
	// BookID is the first Harbour Lights book; AuthorID, NarratorID and SeriesID belong to it.
	BookID, AuthorID, NarratorID, SeriesID string
	LibID                                  string // the Harbour Lights library
	ShelfID, EmptyShelfID                  string
	AdminLibID, AdminFolderID              string // the library of adminSeed
	EbookFoundID                           string // the ready ebook no book has (the Ebooks to pair inbox)
	ReaderBookID                           string // the reader fixture's book (brSeed)
	ReaderLab                              *rdrLab
	TextBookID                             string // a text book (an ebook with no audiobook), with a ready ebook
	MemberID                               string
	InviteToken, LinkToken                 string
	PairToken, PairID                      string // a waiting pairing of the owner's session (decision 0028)
	SignInToken, SignInPanelID             string // an unspent sign-in link; the person whose page "main/panel" shows a new one (decision 0030)
	UploadLibID, UploadBookID              string

	Main, Upload, Fresh, Setup *testApp
	Player                     *playerEnv

	// URLs maps "fixture/role" (for example "main/owner") to the address of a server.
	URLs map[string]string
}

// a11yServers returns the address for a fixture and role, or "" when there is none.
func (e *a11yEnv) url(fixture, role string) string { return e.URLs[fixture+"/"+role] }

func a11yOne(t *testing.T, a *testApp, query string, args ...any) string {
	t.Helper()
	var s string
	if err := a.st.Reader().QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

func a11yIDSet(t *testing.T, a *testApp, query string) map[string]bool {
	t.Helper()
	rows, err := a.st.Reader().Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	return out
}

// a11ySeed builds every fixture. Ids are seeded (TestA11y* is one of the names seedVisualIDs repeats),
// so selectors in findings.json are the same from run to run.
func a11ySeed(t *testing.T) *a11yEnv {
	t.Helper()
	seedVisualIDs(t)
	ctx := context.Background()
	e := &a11yEnv{URLs: map[string]string{}}
	serve := func(key string, h http.Handler) {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		e.URLs[key] = srv.URL
	}
	as := func(key string, a *testApp, ck *http.Cookie) {
		e.URLs[key] = asServer(t, a, func() *http.Cookie { return ck })
	}

	// main: the Harbour Lights library with its shelves, a queue, a member, an invite and a link.
	// The lookup service sits on a fake network, as in the route sweep, so the lookup pages exist.
	a := newTestApp(t, compareMod(t, &compareNet{}))
	e.Main = a
	ownerCk := a.owner(t)
	var full, empty string
	e.BookID, full, empty = shelfVisualSeed(t, a)
	e.ShelfID, e.EmptyShelfID = full, empty
	card, err := a.lib.BookDetail(ctx, bookAllScope, e.BookID)
	if err != nil {
		t.Fatal(err)
	}
	e.AuthorID, e.NarratorID, e.SeriesID = card.Card.Authors[0].ID, card.Card.Narrators[0].ID, card.Series[0].ID
	e.LibID = a11yOne(t, a, `SELECT id FROM libraries`)
	owner := sweepUserID(t, a, ownerCk)
	// The queue of queueVisualSeed, without building the library a second time.
	page, err := a.lib.ListBooks(ctx, bookAllScope, library.ListQuery{Sort: library.SortTitle, Limit: 20})
	if err != nil || len(page.Books) < 5 {
		t.Fatalf("seed books = %d, %v", len(page.Books), err)
	}
	for _, b := range page.Books[1:5] {
		if _, err := a.lib.QueueAdd(ctx, owner, b.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	before := a11yIDSet(t, a, `SELECT id FROM libraries`)
	adminSeed(t, a)
	for id := range a11yIDSet(t, a, `SELECT id FROM libraries`) {
		if !before[id] {
			e.AdminLibID = id
		}
	}
	e.AdminFolderID = a11yOne(t, a, `SELECT id FROM library_folders WHERE library_id = ?`, e.AdminLibID)

	if bk, err := a.lib.Book(ctx, e.BookID); err != nil {
		t.Fatal(err)
	} else {
		e.EbookFoundID = textSeedStates(t, a, bk, owner)
		tid, err := a.lib.CreateTextBook(ctx, library.TextBookIn{LibraryID: bk.LibraryID, FolderID: bk.FolderID,
			RootPath: a.dir + "/the-salt-road.epub", Title: "The Salt Road"})
		if err != nil {
			t.Fatal(err)
		}
		tb, err := a.lib.Book(ctx, tid)
		if err != nil {
			t.Fatal(err)
		}
		placeEbook(t, a, tb, owner, "the-salt-road.epub", "ready", "")
		e.TextBookID = tid
		// Reading stats: a few sessions over the last days and one finished book, so /me/reading has its charts.
		for i, ago := range []time.Duration{50 * time.Hour, 26 * time.Hour, 3 * time.Hour} {
			if err := a.srv.progress.AddReadingTime(ctx, owner, tid, "a11ydevice000001", 9000+int64(i)*400, 900_000, time.Now().Add(-ago).UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := a.srv.progress.SetReadingFinished(ctx, owner, tid, true); err != nil {
			t.Fatal(err)
		}
		eadmSeedMaybe(t, a, bk, owner, card.Card.Authors[0].Name) // a "Maybe the same book" row on /admin/ebooks
	}
	memberCk := a.as(t, users.RoleMember)
	e.MemberID = sweepUserID(t, a, memberCk)
	if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
		VALUES ('gr_a11y_member', ?, 'library', ?, 0, 0, 1, 1)`, e.MemberID, e.LibID); err != nil {
		t.Fatal(err)
	}
	// The member added the first book (decision 0029), so their page shows "Books they added" and the remove-all confirm opens.
	if _, err := a.st.Writer().Exec(`INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at)
		SELECT root_path, 'up_a11y', library_id, ?, 1 FROM books WHERE id = ?`, e.MemberID, e.BookID); err != nil {
		t.Fatal(err)
	}
	ownerUser, err := a.srv.users.ByID(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		_, e.InviteToken, err = a.srv.invites.Create(ctx, tx, ownerUser, share.InviteSpec{
			Name: "Rosa", Email: "rosa@example.org", Note: "Hope you enjoy these.", Role: users.RoleMember,
			Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.LibID, Download: true}},
		})
		if err != nil {
			return err
		}
		_, e.LinkToken, err = a.srv.links.Create(ctx, tx, ownerUser, share.LinkSpec{BookID: e.BookID, Days: 7, Label: "Tia"}, "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if sess, _, _, err := auth.NewSessions(a.st, nil).Lookup(ctx, ownerCk.Value); err != nil {
		t.Fatal(err)
	} else if p, tok, err := a.srv.auth.StartPairing(ctx, sess, testAddr); err != nil {
		t.Fatal(err)
	} else {
		e.PairToken, e.PairID = tok, p.ID
		// The audit takes longer than a pairing lasts: this one must outlive it.
		if _, err := a.st.Writer().ExecContext(ctx, `UPDATE device_pairings SET expires_at = expires_at + 3600000 WHERE id = ?`, p.ID); err != nil {
			t.Fatal(err)
		}
	}
	as("main/owner", a, ownerCk)
	as("main/member", a, memberCk)
	a11ySignIn(t, e, a, ownerCk, serve, as)
	a11yFeeds(t, e, a, ownerCk, serve)
	serve("main/anon", a.h)

	// upload: the Add books page and its queue.
	up, upLib := uploadVisualApp(t)
	e.Upload, e.UploadLibID = up, upLib
	e.UploadBookID = a11yOne(t, up, `SELECT id FROM books`)
	// The book was "placed by an upload": give each of its files a ledger row, so /books/{id}/remove is a page.
	files, err := up.lib.AudioFiles(ctx, e.UploadBookID)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range files {
		streamExec(t, up, `INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, quick_hash, created_at, updated_at)
			SELECT ?, id, folder_id, ?, 'file', 'present', 1, 1, 'x', 1, 1 FROM uploads WHERE id = 'upl_a3'`, fmt.Sprintf("upp_a11y_%d", i), f.Path)
	}
	as("upload/owner", up, up.owner(t))

	// fresh: no owner yet (/setup); setup: an owner who has not finished the three steps.
	e.Fresh = newTestApp(t)
	serve("fresh/anon", e.Fresh.h)
	e.Setup = newTestApp(t)
	as("setup/owner", e.Setup, e.Setup.owner(t))

	// player: the book with real audio and a position from another device, so the bar is up.
	e.Player = playerApp(t)
	e.Player.store(t, 100_000, "a11ydevice001", "Other device", 0, false)
	e.URLs["player/owner"] = e.Player.URL

	// reader: the synthetic book of brSeed. The owner reads in pages, the admin in scroll.
	e.ReaderLab = brSeed(t)
	e.ReaderBookID = e.ReaderLab.book.ID
	as("reader/owner", e.ReaderLab.a, e.ReaderLab.owner)
	as("reader/admin", e.ReaderLab.a, e.ReaderLab.admin)
	return e
}

// a11ySignIn seeds sign-in links (decision 0030): an unspent link (the confirm page), a person who
// spent one ("main/linked": /me/new-password opens for them), and "main/panel", a server that turns a
// visit to the member's page into the POST that makes a link, so the page with the one-time panel is
// audited too.
func a11ySignIn(t *testing.T, e *a11yEnv, a *testApp, ownerCk *http.Cookie, serve func(string, http.Handler), as func(string, *testApp, *http.Cookie)) {
	t.Helper()
	ctx := context.Background()
	owner := sweepUserID(t, a, ownerCk)
	withPassword := func() string {
		id := sweepUserID(t, a, a.as(t, users.RoleMember))
		hash, err := a.srv.users.HashPassword(ctx, "an old password 1")
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, a, `UPDATE users SET password_hash = ? WHERE id = ?`, hash, id)
		return id
	}
	link := func(id string) string {
		var tok string
		err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
			var err error
			tok, _, err = a.srv.auth.MakeSignInLink(ctx, tx, owner, id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	e.SignInToken = link(withPassword())
	l, _, err := a.srv.auth.SpendSignInLink(ctx, link(withPassword()), testAddr, "a11y", nil)
	if err != nil {
		t.Fatal(err)
	}
	as("main/linked", a, &http.Cookie{Name: auth.CookieName, Value: l.Token})
	e.SignInPanelID = e.MemberID
	// Sign-in with a provider (decision 0037) is on, so /login shows the button and /admin/sso, /me/sso have
	// something to audit. Pages never call the provider; the address only has to parse.
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := oidc.SaveConfig(ctx, tx, tx, users.User{ID: owner}, oidc.ConfigInput{Enabled: true, Name: "Authelia",
			Issuer: "https://auth.example.org", ClientID: "tendshelf", ServerAddress: "http://127.0.0.1:8095"}, "", time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	serve("main/panel", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/users/"+e.SignInPanelID {
			form := url.Values{"_csrf": {a.tokenFor(t, ownerCk)}}
			req := httptest.NewRequest(http.MethodPost, r.URL.Path+"/signin-link", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Host = r.Host
			for _, c := range r.Cookies() {
				req.AddCookie(c)
			}
			r = req
		}
		r.AddCookie(&http.Cookie{Name: ownerCk.Name, Value: ownerCk.Value})
		a.h.ServeHTTP(w, r)
	}))
}

// a11yFeeds seeds podcast feeds (decision 0031): one feed of the owner, so "Your devices" lists a feed, and
// "main/feedmade", a server that turns a visit to the book's feed page into the POST that makes an address, so the
// page with the one-time address is audited too.
func a11yFeeds(t *testing.T, e *a11yEnv, a *testApp, ownerCk *http.Cookie, serve func(string, http.Handler)) {
	t.Helper()
	ctx := context.Background()
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, _, err := a.srv.feeds.Create(ctx, tx, sweepUserID(t, a, ownerCk), "Pocket Casts", e.BookID, "")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	serve("main/feedmade", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/books/"+e.BookID+"/feed" {
			form := url.Values{"_csrf": {a.tokenFor(t, ownerCk)}}
			req := httptest.NewRequest(http.MethodPost, r.URL.Path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Host = r.Host
			for _, c := range r.Cookies() {
				req.AddCookie(c)
			}
			r = req
		}
		r.AddCookie(&http.Cookie{Name: ownerCk.Name, Value: ownerCk.Value})
		a.h.ServeHTTP(w, r)
	}))
}
