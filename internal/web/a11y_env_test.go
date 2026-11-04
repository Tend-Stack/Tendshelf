//go:build dev

package web

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
	MemberID                               string
	InviteToken, LinkToken                 string
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

	memberCk := a.as(t, users.RoleMember)
	e.MemberID = sweepUserID(t, a, memberCk)
	if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
		VALUES ('gr_a11y_member', ?, 'library', ?, 0, 0, 1, 1)`, e.MemberID, e.LibID); err != nil {
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
	as("main/owner", a, ownerCk)
	as("main/member", a, memberCk)
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
	return e
}
