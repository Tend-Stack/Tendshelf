package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// The iPhone app's live address (decision 0049, package I2r).

var liveAddressPath = regexp.MustCompile(`^/cast/[A-Za-z0-9_-]{43}/live$`)

// mintLive posts to the mint endpoint with a bearer token (empty: no Authorization header).
func (w *radioWorld) mintLive(tok, stationID string) *httptest.ResponseRecorder {
	w.t.Helper()
	path := "/api/v1/radio/live/" + stationID + "/address"
	if tok == "" {
		return w.a.do(http.MethodPost, path, nil, nil)
	}
	return deviceBearerReq(w.a, http.MethodPost, path, tok, nil)
}

// liveAddress mints and returns the path, failing the test unless the answer is 200.
func (w *radioWorld) liveAddress(tok, stationID string) string {
	w.t.Helper()
	rec := w.mintLive(tok, stationID)
	var out struct {
		Path      string
		ExpiresAt int64
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil || !liveAddressPath.MatchString(out.Path) || out.ExpiresAt == 0 {
		w.t.Fatalf("mint = %d %q", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		w.t.Errorf("Cache-Control = %q", rec.Header().Get("Cache-Control"))
	}
	return out.Path
}

// deviceToken issues a device token of another kind (bearer always issues a phone).
func (w *radioWorld) deviceToken(uid string, kind auth.DeviceKind) string {
	w.t.Helper()
	var tok string
	err := w.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(w.a.st, nil).Issue(context.Background(), tx, uid, "Desk", kind)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return tok
}

func (w *radioWorld) liveRows() int { return w.count(`SELECT count(*) FROM cast_tokens`) }

func TestIOSLiveMintAndPlay(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	tok := w.bearer(w.benID)
	path := w.liveAddress(tok, id)
	// No cookie and no Authorization header: the address is the credential, and casting stays off.
	_, audio := radioAudio()
	rec := w.a.do(http.MethodGet, path, nil, nil)
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), audio) {
		t.Fatalf("live = %d, %d bytes (want %d)", rec.Code, rec.Body.Len(), len(audio))
	}
	h := rec.Header()
	if h.Get("Content-Type") != "audio/mpeg" || h.Get("Accept-Ranges") != "none" || h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("Set-Cookie") != "" {
		t.Errorf("headers %v", h)
	}
	if rec = w.a.do(http.MethodHead, path, nil, nil); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD = %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestIOSLiveForbidden(t *testing.T) {
	w := newRadioWorld(t)
	own := w.add(w.owner, w.fx.pub.URL("radio.test", "/live.mp3?own=1"))
	ben := w.add(w.ben, w.fx.liveURL())
	benTok := w.bearer(w.benID)
	guestTok := w.bearer(sweepUserID(t, w.a, w.guest))
	for _, c := range []struct {
		name string
		rec  func() *httptest.ResponseRecorder
		want int
	}{
		// A POST with no credential at all stops at the CSRF check, before the handler.
		{"no auth", func() *httptest.ResponseRecorder { return w.mintLive("", ben) }, 403},
		{"unknown bearer", func() *httptest.ResponseRecorder { return w.mintLive("not-a-token", ben) }, 401},
		{"cookie session", func() *httptest.ResponseRecorder { return w.post("/api/v1/radio/live/"+ben+"/address", w.ben, nil) }, 403},
		{"guest bearer", func() *httptest.ResponseRecorder { return w.mintLive(guestTok, ben) }, 404},
		{"other person station", func() *httptest.ResponseRecorder { return w.mintLive(benTok, own) }, 404},
		{"unknown station", func() *httptest.ResponseRecorder { return w.mintLive(benTok, "st_nope") }, 404},
		{"GET", func() *httptest.ResponseRecorder {
			return deviceBearerReq(w.a, http.MethodGet, "/api/v1/radio/live/"+ben+"/address", benTok, nil)
		}, 404}, // the API answers an unrouted method with its plain 404
	} {
		if rec := c.rec(); rec.Code != c.want {
			t.Errorf("%s = %d, want %d (%q)", c.name, rec.Code, c.want, rec.Body)
		}
		if n := w.liveRows(); n != 0 {
			t.Fatalf("%s left %d cast_tokens rows", c.name, n)
		}
	}
	// A bearer for a device that is not a phone (a desktop token) is refused too.
	deskTok := w.deviceToken(w.benID, auth.KindDesktop)
	if rec := w.mintLive(deskTok, ben); rec.Code != http.StatusForbidden || w.liveRows() != 0 {
		t.Errorf("desktop token = %d, rows %d", rec.Code, w.liveRows())
	}
	// Radio off: 404.
	w.fx.enable(t, w.a, false)
	if rec := w.mintLive(benTok, ben); rec.Code != http.StatusNotFound || w.liveRows() != 0 {
		t.Errorf("radio off = %d, rows %d", rec.Code, w.liveRows())
	}
}

func TestIOSLiveOnlyLiveRoute(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	path := w.liveAddress(w.bearer(w.benID), id)
	tok := strings.TrimSuffix(strings.TrimPrefix(path, "/cast/"), "/live")
	for _, p := range []string{"/art", "/episode", "/audio/x"} {
		if rec := w.a.do(http.MethodGet, "/cast/"+tok+p, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("live address on %s = %d", p, rec.Code)
		}
	}
	if rec := w.a.do(http.MethodPost, path, nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
	if rec := deviceBearerReq(w.a, http.MethodGet, "/api/v1/me", tok, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("as a bearer token = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, "/me", nil, nil, &http.Cookie{Name: "ts_session", Value: tok}); rec.Code == http.StatusOK {
		t.Errorf("as a session cookie = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, "/feeds/"+tok+"/", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("on /feeds = %d", rec.Code)
	}
}

func TestIOSLiveCastingSwitchAndBrowserSignOut(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	path := w.liveAddress(w.bearer(w.benID), id)
	// A casting token for a book-like target, minted before the switch and the sign-out.
	rec, castTok := w.castMint(w.ben, w.benID, id)
	if rec.Code != http.StatusOK {
		t.Fatalf("cast mint = %d", rec.Code)
	}
	off := w.post("/me/settings", w.ben, url.Values{"cast": {"0", "0"}}, func(r *http.Request) {
		proxyDefault(r)
		r.Header.Set("HX-Request", "true")
	})
	if off.Code != http.StatusNoContent {
		t.Fatalf("casting off = %d", off.Code)
	}
	if rec := w.castGet(http.MethodGet, castTok); rec.Code != http.StatusNotFound {
		t.Errorf("casting token after off = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, path, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("live address after casting off = %d", rec.Code)
	}
	if rec := w.post("/logout", w.ben, nil, proxyDefault); rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, path, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("live address after browser sign-out = %d", rec.Code)
	}
	if n := w.count(`SELECT count(*) FROM cast_tokens WHERE device_id IS NULL`); n != 0 {
		t.Errorf("casting rows left = %d", n)
	}
}

func TestIOSLiveRevokedDevice(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())

	// The app signs itself out.
	tok := w.bearer(w.benID)
	path := w.liveAddress(tok, id)
	if rec := deviceBearerReq(w.a, http.MethodDelete, "/api/v1/auth/token", tok, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("sign-out = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound || w.liveRows() != 0 {
		t.Errorf("after app sign-out = %d, rows %d", rec.Code, w.liveRows())
	}

	// The person signs the device out on the Devices page.
	tok = w.bearer(w.benID)
	path = w.liveAddress(tok, id)
	var did string
	if err := w.a.st.Reader().QueryRow(`SELECT d.id FROM devices d JOIN cast_tokens c ON c.device_id = d.id`).Scan(&did); err != nil {
		t.Fatal(err)
	}
	if rec := w.post("/me/devices/"+did+"/signout", w.ben, nil, proxyDefault); rec.Code != http.StatusSeeOther {
		t.Fatalf("devices sign-out = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound || w.liveRows() != 0 {
		t.Errorf("after Devices sign-out = %d, rows %d", rec.Code, w.liveRows())
	}

	// A revoke that bypasses both handlers: the lookup alone refuses, the row stays, and no guess penalty builds up.
	tok = w.bearer(w.benID)
	path = w.liveAddress(tok, id)
	err := w.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `UPDATE devices SET revoked_at = 1, token_hash = NULL WHERE id = (SELECT device_id FROM cast_tokens)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if rec := w.a.do(http.MethodGet, path, nil, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %d after SQL revoke = %d", i, rec.Code)
		}
	}
	if w.liveRows() != 1 {
		t.Errorf("rows = %d, want the row kept", w.liveRows())
	}
}

func TestIOSLiveOnePerDevice(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	tok := w.bearer(w.benID)
	first := w.liveAddress(tok, id)
	second := w.liveAddress(tok, id)
	if first == second {
		t.Fatal("two mints gave one address")
	}
	if rec := w.a.do(http.MethodGet, first, nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("first after second mint = %d", rec.Code)
	}
	if rec := w.a.do(http.MethodGet, second, nil, nil); rec.Code != http.StatusOK {
		t.Errorf("second = %d", rec.Code)
	}
}
