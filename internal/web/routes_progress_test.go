package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// progRouteEnv is an app with a fake clock, an owner and a member, and one 6 s book of two WAV files
// with four chapters and a cover with accent colours.
type progRouteEnv struct {
	a              *testApp
	owner, member  *http.Cookie
	ownerID        string
	memberID       string
	book           library.Book
	files          []library.AudioFile
	nowMs          *atomic.Int64
	ownerTok, mTok string
}

const progRouteFF = "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0"

func progRouteEnvOf(t *testing.T) *progRouteEnv {
	t.Helper()
	clock := new(atomic.Int64)
	clock.Store(time.Now().UnixMilli())
	a := newTestApp(t, func(o *Options) { o.Now = func() time.Time { return time.UnixMilli(clock.Load()) } })
	e := &progRouteEnv{a: a, nowMs: clock}
	e.owner = a.owner(t)
	e.member = a.as(t, users.RoleMember)
	e.ownerID = sweepUserID(t, a, e.owner)
	e.memberID = sweepUserID(t, a, e.member)
	e.ownerTok = a.tokenFor(t, e.owner)
	e.mTok = a.tokenFor(t, e.member)
	_, f := librarytest.NewLibrary(t, a.lib, "Progress library", t.TempDir())
	e.book = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
		Title: "Progword Harbour", Authors: []string{"Pia Progauthor"}, DurationMs: 6000, AddedAt: 5, Files: 2})
	files, err := a.lib.AudioFiles(context.Background(), e.book.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	for i, af := range files {
		if err := wav.Write(af.Path, 3000, 440); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3000, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			i*3000, fi.Size(), fi.ModTime().UnixNano(), af.ID)
	}
	e.files, _ = a.lib.AudioFiles(context.Background(), e.book.ID)
	bookSeedChapters(t, a, e.book.ID, 4)
	sha := "abcdef" + strings.Repeat("0", 58)
	cover := library.Cover{BookID: e.book.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha,
		Path: "covers/progress.png", Tint: "#1f4a52", Accent: json.RawMessage(`{"dark":"#9ac9c2","light":"#35615c","sepia":"#7a4a1e"}`)}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(e.book.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

// post sends a progress save as a cookie holder with the header token, returning the recorder.
func (e *progRouteEnv) post(bookID string, ck *http.Cookie, tok string, form url.Values, mod func(*http.Request)) *httptest.ResponseRecorder {
	return e.a.do(http.MethodPost, "/progress/"+bookID, form, func(r *http.Request) {
		r.Header.Set("X-CSRF-Token", tok)
		if mod != nil {
			mod(r)
		}
	}, ck)
}

func progRouteForm(pos int64, cu int64, device string, more ...string) url.Values {
	f := url.Values{"position_ms": {strconv.FormatInt(pos, 10)}, "client_updated_at": {strconv.FormatInt(cu, 10)}, "device_id": {device}}
	for i := 0; i+1 < len(more); i += 2 {
		f.Set(more[i], more[i+1])
	}
	return f
}

type progRouteAnswer struct {
	Applied  bool `json:"applied"`
	Progress *struct {
		PositionMs      int64  `json:"positionMs"`
		DurationMs      int64  `json:"durationMs"`
		Finished        bool   `json:"finished"`
		FinishedAt      int64  `json:"finishedAt"`
		ClientUpdatedAt int64  `json:"clientUpdatedAt"`
		DeviceID        string `json:"deviceId"`
		DeviceLabel     string `json:"deviceLabel"`
	} `json:"progress"`
	ServerNow int64 `json:"serverNow"`
	Error     *struct {
		Code  string `json:"code"`
		Field string `json:"field"`
	} `json:"error"`
}

func progRouteDecode(t *testing.T, rec *httptest.ResponseRecorder) progRouteAnswer {
	t.Helper()
	var out progRouteAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("answer %d is not JSON: %v: %q", rec.Code, err, rec.Body.String())
	}
	return out
}

// stored reads the progress row of a person straight from the service.
func (e *progRouteEnv) stored(userID string) (progress.Progress, bool) {
	p, err := progress.New(e.a.st, nil).Get(context.Background(), userID, e.book.ID)
	return p, err == nil
}

func progRouteCount(t *testing.T, e *progRouteEnv, table string) int {
	t.Helper()
	var n int
	if err := e.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestProgressRouteSave(t *testing.T) {
	e := progRouteEnvOf(t)
	now := e.nowMs.Load()
	rec := e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(2000, now, "devicealpha1", "duration_ms", "1"),
		func(r *http.Request) { r.Header.Set("User-Agent", progRouteFF) })
	if rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	got := progRouteDecode(t, rec)
	if !got.Applied || got.Progress == nil || got.Progress.PositionMs != 2000 {
		t.Fatalf("answer = %s", rec.Body)
	}
	if got.Progress.DeviceLabel != "Firefox on Linux" {
		t.Errorf("deviceLabel = %q", got.Progress.DeviceLabel)
	}
	if got.Progress.DurationMs != 6000 {
		t.Errorf("duration = %d, want the book's 6000 (the client's duration_ms=1 is ignored)", got.Progress.DurationMs)
	}
	// An older save is not applied and returns the stored row.
	rec = e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(500, now-1000, "devicebeta12"), nil)
	got = progRouteDecode(t, rec)
	if rec.Code != 200 || got.Applied || got.Progress.PositionMs != 2000 {
		t.Errorf("older save: %d %s", rec.Code, rec.Body)
	}
	// A position past the end is clamped to the book's duration.
	rec = e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(99_000, now+10, "devicealpha1"), nil)
	got = progRouteDecode(t, rec)
	if !got.Applied || got.Progress.PositionMs != 6000 {
		t.Errorf("clamp: %s", rec.Body)
	}
}

func TestProgressRouteBeacon(t *testing.T) {
	e := progRouteEnvOf(t)
	now := e.nowMs.Load()
	beacon := func(form url.Values, site string) *httptest.ResponseRecorder {
		return e.a.do(http.MethodPost, "/progress/"+e.book.ID, form, func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
			r.Header.Set("Sec-Fetch-Site", site)
		}, e.owner)
	}
	f := progRouteForm(1000, now, "devicealpha1")
	f.Set("_csrf", e.ownerTok)
	if rec := beacon(f, "same-origin"); rec.Code != 200 || !progRouteDecode(t, rec).Applied {
		t.Fatalf("beacon: %d %s", rec.Code, rec.Body)
	}
	// Cross-site and tokenless beacons are refused and store nothing new.
	g := progRouteForm(4000, now+10, "devicealpha1")
	if rec := beacon(g, "same-origin"); rec.Code != 403 {
		t.Errorf("no _csrf: %d", rec.Code)
	}
	g.Set("_csrf", e.ownerTok)
	if rec := beacon(g, "cross-site"); rec.Code != 403 {
		t.Errorf("cross-site: %d", rec.Code)
	}
	if p, _ := e.stored(e.ownerID); p.PositionMs != 1000 {
		t.Errorf("stored position = %d after refused beacons", p.PositionMs)
	}
	// Over 4 KiB, with the header token: 413.
	big := progRouteForm(3000, now+20, "devicealpha1", "pad", strings.Repeat("x", 5000))
	if rec := e.post(e.book.ID, e.owner, e.ownerTok, big, nil); rec.Code != 413 {
		t.Errorf("big body: %d", rec.Code)
	}
	// text/plain: 415 with the header token, 403 without.
	plain := func(tok string) *httptest.ResponseRecorder {
		return e.a.do(http.MethodPost, "/progress/"+e.book.ID, nil, func(r *http.Request) {
			r.Body = http.NoBody
			r.Header.Set("Content-Type", "text/plain")
			if tok != "" {
				r.Header.Set("X-CSRF-Token", tok)
			}
		}, e.owner)
	}
	if rec := plain(e.ownerTok); rec.Code != 415 {
		t.Errorf("text/plain with token: %d", rec.Code)
	}
	if rec := plain(""); rec.Code != 403 {
		t.Errorf("text/plain without token: %d", rec.Code)
	}
	if p, _ := e.stored(e.ownerID); p.PositionMs != 1000 {
		t.Errorf("stored position = %d after refused saves", p.PositionMs)
	}
}

func TestProgressRouteValidation(t *testing.T) {
	e := progRouteEnvOf(t)
	now := e.nowMs.Load()
	cases := []struct {
		name  string
		edit  func(url.Values)
		field string
	}{
		{"position missing", func(f url.Values) { f.Del("position_ms") }, "position_ms"},
		{"position negative", func(f url.Values) { f.Set("position_ms", "-1") }, "position_ms"},
		{"position text", func(f url.Values) { f.Set("position_ms", "abc") }, "position_ms"},
		{"client time zero", func(f url.Values) { f.Set("client_updated_at", "0") }, "client_updated_at"},
		{"client time missing", func(f url.Values) { f.Del("client_updated_at") }, "client_updated_at"},
		{"device missing", func(f url.Values) { f.Del("device_id") }, "device_id"},
		{"device short", func(f url.Values) { f.Set("device_id", "short") }, "device_id"},
		{"speed NaN", func(f url.Values) { f.Set("speed", "NaN"); f.Set("listened_ms", "2000") }, "speed"},
		{"speed too high", func(f url.Values) { f.Set("speed", "4"); f.Set("listened_ms", "2000") }, "speed"},
		{"listened negative", func(f url.Values) { f.Set("listened_ms", "-1") }, "listened_ms"},
		{"finished 2", func(f url.Values) { f.Set("finished", "2") }, "finished"},
		{"force yes", func(f url.Values) { f.Set("force", "yes") }, "force"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := progRouteForm(1000, now, "devicealpha1")
			c.edit(f)
			rec := e.post(e.book.ID, e.owner, e.ownerTok, f, nil)
			got := progRouteDecode(t, rec)
			if rec.Code != 400 || got.Error == nil || got.Error.Code != "invalid" || got.Error.Field != c.field {
				t.Errorf("%d %s, want 400 invalid %s", rec.Code, rec.Body, c.field)
			}
		})
	}
	if _, ok := e.stored(e.ownerID); ok {
		t.Error("a rejected save stored a row")
	}
}

func TestProgressRouteAccess(t *testing.T) {
	e := progRouteEnvOf(t)
	now := e.nowMs.Load()
	// The member has no grant: the book answers exactly like an unknown one.
	if rec := e.a.do(http.MethodGet, "/progress/"+e.book.ID, nil, nil, e.member); rec.Code != 404 {
		t.Errorf("member GET: %d", rec.Code)
	}
	if rec := e.post(e.book.ID, e.member, e.mTok, progRouteForm(1000, now, "devicealpha1"), nil); rec.Code != 404 {
		t.Errorf("member POST: %d", rec.Code)
	}
	if _, ok := e.stored(e.memberID); ok {
		t.Error("the member's refused save stored a row")
	}
	if rec := e.a.do(http.MethodGet, "/progress/bk_unknown", nil, nil, e.owner); rec.Code != 404 {
		t.Errorf("unknown GET: %d", rec.Code)
	}
	if rec := e.post("bk_unknown", e.owner, e.ownerTok, progRouteForm(1000, now, "devicealpha1"), nil); rec.Code != 404 {
		t.Errorf("unknown POST: %d", rec.Code)
	}
	for name, sql := range map[string]string{
		"refused": `UPDATE books SET refused_reason='drm' WHERE id=?`,
		"missing": `UPDATE books SET missing_since=5 WHERE id=?`,
	} {
		streamExec(t, e.a, sql, e.book.ID)
		if rec := e.a.do(http.MethodGet, "/progress/"+e.book.ID, nil, nil, e.owner); rec.Code != 404 {
			t.Errorf("%s book GET: %d", name, rec.Code)
		}
		if rec := e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(1000, now, "devicealpha1"), nil); rec.Code != 404 {
			t.Errorf("%s book POST: %d", name, rec.Code)
		}
		streamExec(t, e.a, `UPDATE books SET refused_reason=NULL, missing_since=NULL WHERE id=?`, e.book.ID)
	}
	if _, ok := e.stored(e.ownerID); ok {
		t.Error("a save for a refused or missing book stored a row")
	}
	// Anonymous: the gate's 303 to the sign-in page.
	c, tok := e.a.anon(t, "/login")
	rec := e.a.do(http.MethodPost, "/progress/"+e.book.ID, progRouteForm(1000, now, "devicealpha1"),
		func(r *http.Request) { r.Header.Set("X-CSRF-Token", tok) }, c)
	if rec.Code != 303 || location(rec) != "/login" {
		t.Errorf("anonymous POST: %d %q", rec.Code, location(rec))
	}
}

func TestProgressRouteGet(t *testing.T) {
	e := progRouteEnvOf(t)
	rec := e.a.do(http.MethodGet, "/progress/"+e.book.ID, nil, nil, e.owner)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"progress":null`) {
		t.Fatalf("none: %d %s", rec.Code, rec.Body)
	}
	if got := progRouteDecode(t, rec); got.ServerNow != e.nowMs.Load() {
		t.Errorf("serverNow = %d, clock %d", got.ServerNow, e.nowMs.Load())
	}
	e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(1200, e.nowMs.Load(), "devicealpha1"), nil)
	rec = e.a.do(http.MethodGet, "/progress/"+e.book.ID, nil, nil, e.owner)
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var raw struct {
		Progress map[string]any `json:"progress"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"positionMs", "durationMs", "finished", "finishedAt", "clientUpdatedAt", "serverUpdatedAt", "deviceId", "deviceLabel"} {
		if _, ok := raw.Progress[k]; !ok {
			t.Errorf("key %q missing in %s", k, rec.Body)
		}
	}
	if raw.Progress["positionMs"] != float64(1200) {
		t.Errorf("positionMs = %v", raw.Progress["positionMs"])
	}
}

func TestProgressRouteForce(t *testing.T) {
	e := progRouteEnvOf(t)
	now := e.nowMs.Load()
	// Stored at now + 60 s (the furthest ahead the service accepts).
	if got := progRouteDecode(t, e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(4000, now+60_000, "devicealpha1"), nil)); !got.Applied {
		t.Fatal("seed save not applied")
	}
	if got := progRouteDecode(t, e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(1000, now, "devicebeta12"), nil)); got.Applied {
		t.Error("a plain older save was applied")
	}
	got := progRouteDecode(t, e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(1000, now, "devicebeta12", "force", "1"), nil))
	if !got.Applied || got.Progress.PositionMs != 1000 || got.Progress.DeviceID != "devicebeta12" {
		t.Errorf("forced save: %+v", got)
	}
}

func TestProgressRouteSession(t *testing.T) {
	e := progRouteEnvOf(t)
	now := e.nowMs.Load()
	e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(2000, now, "devicealpha1", "speed", "1", "listened_ms", "10000"), nil)
	e.nowMs.Add(10_000)
	e.post(e.book.ID, e.owner, e.ownerTok, progRouteForm(4000, now+10_000, "devicealpha1", "speed", "1", "listened_ms", "10000"), nil)
	if n := progRouteCount(t, e, "listening_sessions"); n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}
	var listened int64
	if err := e.a.st.Reader().QueryRow(`SELECT listened_ms FROM listening_sessions`).Scan(&listened); err != nil || listened != 20000 {
		t.Errorf("listened_ms = %d, %v", listened, err)
	}
}

func TestProgressRouteFinished(t *testing.T) {
	e := progRouteEnvOf(t)
	finished := func(ck *http.Cookie, tok string, form url.Values, htmx bool) *httptest.ResponseRecorder {
		return e.a.do(http.MethodPost, "/hx/books/"+e.book.ID+"/finished", form, func(r *http.Request) {
			r.Header.Set("X-CSRF-Token", tok)
			if htmx {
				r.Header.Set("HX-Request", "true")
			}
		}, ck)
	}
	rec := finished(e.owner, e.ownerTok, url.Values{"finished": {"1"}}, true)
	if rec.Code != 204 || rec.Header().Get("HX-Trigger") != "progress-saved" {
		t.Fatalf("htmx finish: %d %q", rec.Code, rec.Header().Get("HX-Trigger"))
	}
	if p, ok := e.stored(e.ownerID); !ok || !p.Finished() || p.PositionMs != 6000 {
		t.Errorf("stored after finish = %+v", p)
	}
	rec = finished(e.owner, e.ownerTok, url.Values{"finished": {"0"}}, true)
	if p, _ := e.stored(e.ownerID); rec.Code != 204 || p.Finished() || p.PositionMs != 0 {
		t.Errorf("unfinish: %d %+v", rec.Code, p)
	}
	rec = finished(e.owner, e.ownerTok, url.Values{"finished": {"1"}, "next": {"/libraries"}}, false)
	if rec.Code != 303 || location(rec) != "/libraries" {
		t.Errorf("plain with next: %d %q", rec.Code, location(rec))
	}
	rec = finished(e.owner, e.ownerTok, url.Values{"finished": {"1"}, "next": {"//evil.example"}}, false)
	if rec.Code != 303 || location(rec) != "/books/"+e.book.ID {
		t.Errorf("unsafe next: %d %q", rec.Code, location(rec))
	}
	if rec := finished(e.owner, e.ownerTok, url.Values{"finished": {"x"}}, false); rec.Code != 400 {
		t.Errorf("finished=x: %d", rec.Code)
	}
	if rec := finished(e.owner, e.ownerTok, url.Values{"finished": {"x"}}, true); rec.Code != 400 {
		t.Errorf("htmx finished=x: %d", rec.Code)
	}
	if rec := finished(e.member, e.mTok, url.Values{"finished": {"1"}}, false); rec.Code != 404 {
		t.Errorf("member: %d", rec.Code)
	}
	if _, ok := e.stored(e.memberID); ok {
		t.Error("the member's refused finish stored a row")
	}
}
