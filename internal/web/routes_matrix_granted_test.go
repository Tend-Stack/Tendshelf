package web

import (
	"net/http"
	"testing"
)

// sweepSeedGranted is sweepSeed plus a library grant on the sweep library for the member and the
// guest: no download, no end. Everything else about the app is the same.
func sweepSeedGranted(t *testing.T) *sweepEnv {
	t.Helper()
	e := sweepSeed(t)
	e.granted = true
	for _, role := range []int{sweepMember, sweepGuest} {
		if _, err := e.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
			VALUES (?, ?, 'library', ?, 0, 0, 1, 1)`, "gr_sweep_"+sweepRoleNames[role], e.uid[role], e.libID); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

// sweepGrantedWant is what the rows answer once the member and the guest hold the library grant,
// where that differs from Want, keyed by the row's name. The member's own shelf and bookmark
// exist; the guest has neither, so the guest is pointed at the owner's and is refused.
var sweepGrantedWant = func() map[string][4]int {
	ok := sweepSame(200)
	redirect := sweepSame(303)
	ownRow := [4]int{303, 303, 303, 404} // the member acts on their own shelf or bookmark; the guest has none
	return map[string][4]int{
		"GET /hx/books/{id}/play":                       ok,
		"GET /libraries/{id}":                           ok,
		"GET /hx/libraries/{id}/items":                  ok,
		"GET /books/{id}":                               ok,
		"GET /books/{id}/{tab} about":                   ok,
		"GET /books/{id}/{tab} bookmarks":               ok,
		"GET /books/{id}/bookmarks/export":              ok,
		"GET /authors/{id}":                             ok,
		"GET /narrators/{id}":                           ok,
		"GET /series/{id}":                              ok,
		"GET /covers/{bookID}":                          ok,
		"GET /stream/{audioFileID}":                     ok,
		"GET /stream/{audioFileID} second file":         ok,
		"GET /peaks/{audioFileID}":                      ok,
		"GET /progress/{bookID}":                        ok,
		"GET /hx/player":                                ok, // the seeded progress now points at a visible book
		"GET /hx/player/{bookID}":                       ok,
		"GET /hx/player/panel/{bookID}":                 ok,
		"GET /hx/player/panel/{bookID}/about":           ok,
		"GET /hx/player/panel/{bookID}/bookmarks":       ok,
		"GET /hx/player/panel/{bookID}/count":           ok,
		"GET /hx/player/panel/{bookID}/chapter/{index}": ok,
		"GET /books/{id}/{tab} chapters":                ok,
		"GET /books/{id}/{tab} details":                 ok,
		"GET /books/{id} ?sheet=shelves":                ok,
		"GET /libraries/{id} ?view=list":                ok,
		"POST /books/{id}/bookmarks":                    redirect,
		"POST /bookmarks/{id}/note":                     ownRow,
		"POST /bookmarks/{id}/delete":                   ownRow,
		"POST /progress/{bookID}":                       ok,
		"POST /hx/books/{id}/finished":                  redirect,
		"POST /shelves/{id}/items":                      ownRow,
		"POST /shelves/{id}/items/{bookID}/move":        ownRow,
		"GET /api/v1/libraries/{id}/books":              ok,
		"GET /api/v1/books/{id}":                        ok,
		"GET /api/v1/covers/{bookID}":                   ok,
		"GET /api/v1/stream/{audioFileID}":              ok,
		"GET /api/v1/peaks/{audioFileID}":               ok,
	}
}()

// sweepGrantedRows is the table with Granted filled in.
func sweepGrantedRows() []sweepRow {
	rows := sweepRows()
	for i := range rows {
		if g, ok := sweepGrantedWant[rows[i].name()]; ok {
			rows[i].Granted = g
		}
	}
	return rows
}

// TestRouteMatrixGranted runs the rows that a library grant changes for the member and the guest:
// they get the pages of the granted library and nothing else. Every other row keeps its answer
// (TestRouteMatrix proves it without the grant). A disagreement is reported, never patched in a handler.
func TestRouteMatrixGranted(t *testing.T) {
	rows := sweepGrantedRows()
	known := map[string]bool{}
	for _, r := range rows {
		known[r.name()] = true
	}
	for name := range sweepGrantedWant {
		if !known[name] {
			t.Errorf("sweepGrantedWant names %q, which is not a row", name)
		}
	}
	shared := sweepSeedGranted(t)
	for _, row := range rows {
		if row.Method != http.MethodGet {
			continue
		}
		t.Run(row.name(), func(t *testing.T) {
			for role := range sweepRoles {
				shared.check(t, row, role, shared.send(t, row, role, true))
			}
		})
	}
	for _, row := range rows {
		if row.Method == http.MethodGet || row.Granted == ([4]int{}) {
			continue
		}
		t.Run(row.name(), func(t *testing.T) {
			for role := range sweepRoles {
				e := sweepSeedGranted(t)
				e.check(t, row, role, e.send(t, row, role, true))
			}
		})
	}
}
