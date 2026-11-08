package web

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
)

// deviceSeed gives every role and the extra member one live device row (no usable token: the
// hash is random), so the sign-out rows have something to end.
func deviceSeed(t *testing.T, e *sweepEnv) {
	t.Helper()
	add := func(userID string) string {
		id := db.NewID("dev")
		if _, err := e.a.st.Writer().Exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at)
			VALUES (?, ?, 'Sweep phone', 'mobile', randomblob(32), 1)`, id, userID); err != nil {
			t.Fatal(err)
		}
		return id
	}
	for i := range sweepRoles {
		e.devices[i] = add(e.uid[i])
	}
	e.extraDevice = add(e.extra)
}

// devicesSweepRows are the route matrix rows of the device routes (P3-7a).
func devicesSweepRows() []sweepRow {
	tokenRow := sweepPost("POST /api/v1/auth/token", sweepStatic("/api/v1/auth/token"), nil, sweepSame(http.StatusUnsupportedMediaType), "")
	tokenRow.NoCSRF, tokenRow.Anon = true, http.StatusUnsupportedMediaType

	del := sweepRow{Pattern: "DELETE /api/v1/auth/token", Method: http.MethodDelete, Path: sweepStatic("/api/v1/auth/token"),
		Want: sweepSame(http.StatusBadRequest), Anon: http.StatusUnauthorized}

	me := sweepGet("GET /api/v1/me", sweepStatic("/api/v1/me"), sweepSame(http.StatusOK))
	me.Anon = http.StatusUnauthorized

	nope := sweepGet("/api/", sweepStatic("/api/v1/nope"), sweepSame(http.StatusNotFound))
	nope.Anon = http.StatusUnauthorized

	own := sweepPost("POST /me/devices/{did}/signout",
		func(e *sweepEnv, role int) string { return "/me/devices/" + e.devices[role] + "/signout" }, nil, sweepSame(http.StatusSeeOther), "/me/devices")

	name := func(label string, path func(*sweepEnv, int) string, form url.Values, want [4]int, redirect string) sweepRow {
		r := sweepPost("POST /me/devices/{did}/name", path, sweepFormOf(form), want, redirect)
		r.Label = label
		return r
	}
	ownName := func(e *sweepEnv, role int) string { return "/me/devices/" + e.devices[role] + "/name" }
	nameRows := []sweepRow{
		name("own", ownName, url.Values{"name": {"Tablet"}}, sweepSame(http.StatusSeeOther), "/me/devices"),
		name("empty name", ownName, url.Values{"name": {"  "}}, sweepSame(http.StatusUnprocessableEntity), ""),
		name("someone else's device", func(e *sweepEnv, role int) string { return "/me/devices/" + e.devices[(role+1)%4] + "/name" },
			url.Values{"name": {"Stolen"}}, sweepSame(http.StatusNotFound), ""),
		name("unknown device", func(*sweepEnv, int) string { return "/me/devices/dev_nothere/name" },
			url.Values{"name": {"Stolen"}}, sweepSame(http.StatusNotFound), ""),
	}

	staffPost := sweepStaff(http.StatusSeeOther, http.StatusNotFound)
	onlyOwner := [4]int{http.StatusSeeOther, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound}
	none := sweepSame(http.StatusNotFound)
	const back = "/admin/users#users-devices"
	admin := func(label string, path func(*sweepEnv, int) string, want [4]int) sweepRow {
		r := sweepPost("POST /admin/users/{id}/devices/{did}/signout", path, nil, want, back)
		r.Label = label
		return r
	}
	// The Connect page of the phone app (A1). The matrix sends no ts_shell mark, so a signed-in person
	// gets the 403 page with no form, on GET and on POST alike; shell_android_test.go has the rest.
	connectPath := "/app/connect?challenge=" + a1Challenge
	connectGet := sweepGet("GET /app/connect", sweepStatic(connectPath), sweepSame(http.StatusForbidden))
	connectPost := sweepPost("POST /app/connect", sweepStatic("/app/connect"),
		sweepFormOf(url.Values{"challenge": {a1Challenge}}), sweepSame(http.StatusForbidden), "")
	connectDone := sweepGet("GET /app/connect/done", sweepStatic("/app/connect/done"), sweepSame(http.StatusForbidden))
	return append(nameRows, []sweepRow{
		connectGet, connectPost, connectDone,
		tokenRow, del, me, nope, own,
		admin("device", func(e *sweepEnv, _ int) string {
			return "/admin/users/" + e.extra + "/devices/" + e.extraDevice + "/signout"
		}, staffPost),
		admin("session", func(e *sweepEnv, _ int) string {
			return "/admin/users/" + e.uid[sweepAdmin] + "/devices/" + e.secondS[sweepAdmin] + "/signout"
		}, onlyOwner),
		admin("admin's device", func(e *sweepEnv, _ int) string {
			return "/admin/users/" + e.uid[sweepAdmin] + "/devices/" + e.devices[sweepAdmin] + "/signout"
		}, onlyOwner),
		admin("owner target", func(e *sweepEnv, _ int) string {
			return "/admin/users/" + e.uid[sweepOwner] + "/devices/" + e.devices[sweepOwner] + "/signout"
		}, none),
		admin("unknown device", func(e *sweepEnv, _ int) string {
			return "/admin/users/" + e.extra + "/devices/dev_nothere/signout"
		}, none),
		admin("another person's device", func(e *sweepEnv, _ int) string {
			return "/admin/users/" + e.extra + "/devices/" + e.devices[sweepMember] + "/signout"
		}, none),
	}...)
}

// deviceBearer issues a token for a role through the repository (most test people have no password)
// and returns it.
func deviceBearer(t *testing.T, a *testApp, userID string) string {
	t.Helper()
	var tok string
	err := a.st.WriteTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = a.srv.devices.Issue(t.Context(), tx, userID, "Matrix app", auth.KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestBearerMatrix: a device token opens /api/v1/me for every role and nothing else.
func TestBearerMatrix(t *testing.T) {
	e := sweepSeed(t)
	for role := range sweepRoles {
		tok := deviceBearer(t, e.a, e.uid[role])
		send := func(method, path string) *httptest.ResponseRecorder {
			return e.a.do(method, path, url.Values(nil), func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
		}
		for _, c := range []struct {
			method, path string
			want         int
		}{
			{http.MethodGet, "/api/v1/me", http.StatusOK},
			{http.MethodGet, "/", http.StatusUnauthorized},
			{http.MethodPost, "/me/sessions/signout-others", http.StatusUnauthorized},
			{http.MethodGet, "/api/v1/nope", http.StatusNotFound},
		} {
			if rec := send(c.method, c.path); rec.Code != c.want {
				t.Errorf("%s bearer %s %s: %d, want %d", sweepRoleNames[role], c.method, c.path, rec.Code, c.want)
			}
		}
	}
}
