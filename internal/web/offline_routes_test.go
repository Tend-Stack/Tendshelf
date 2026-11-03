package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"image"
	_ "image/png" // registers the PNG decoder
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

// Tests of package A of M3a (docs/plans/m3-offline.md): the worker and manifest routes, the offline
// shell, the download stream, sign-out and the install links. Helpers start with "offlineA".

// offlineAGet is a request without a session.
func offlineAGet(a *testApp, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	return a.do(method, path, nil, func(r *http.Request) {
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
	})
}

// TestServiceWorkerScript checks the /sw.js answer with a fixture worker body, so it holds before the
// real script exists; the same checks then run against the embedded one once it does.
func TestServiceWorkerScript(t *testing.T) {
	const src = "// worker body\nself.addEventListener('fetch', () => {});\n"
	assets := []string{"/static/css/a.0123456789.css", "/static/js/b.0123456789.js"}
	h := newServiceWorker("abcdef012345", assets, []byte(src))
	call := func(method string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/sw.js", nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	want := `self.TS_BUILD = {"build":"abcdef012345","assets":["/static/css/a.0123456789.css","/static/js/b.0123456789.js"]};` + "\n" + src
	rec := call(http.MethodGet, nil)
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("GET: %d %q", rec.Code, rec.Body)
	}
	hd := rec.Header()
	if hd.Get("Content-Type") != "text/javascript; charset=utf-8" || hd.Get("Cache-Control") != "no-cache" ||
		hd.Get("ETag") != `"sw-abcdef012345"` || hd.Get("Set-Cookie") != "" {
		t.Errorf("headers: %v", hd)
	}
	if rec := call(http.MethodGet, map[string]string{"If-None-Match": `"sw-abcdef012345"`}); rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("revalidation: %d %q", rec.Code, rec.Body)
	}
	if rec := call(http.MethodHead, nil); rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") == "" {
		t.Errorf("HEAD: %d %q", rec.Code, rec.Body)
	}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if rec := call(m, nil); rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: %d allow %q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
	// Gzip applies when the body shrinks; the fixture is too small, so use a bigger one.
	big := newServiceWorker("abcdef012345", assets, []byte(strings.Repeat("// padding line for the compressor\n", 200)))
	req := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	grec := httptest.NewRecorder()
	big.ServeHTTP(grec, req)
	if grec.Header().Get("Content-Encoding") != "gzip" || grec.Header().Get("ETag") != `"sw-abcdef012345-gz"` {
		t.Fatalf("gzip headers: %v", grec.Header())
	}
	zr, err := gzip.NewReader(grec.Body)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := io.ReadAll(zr); !bytes.HasPrefix(raw, []byte("self.TS_BUILD = ")) {
		t.Errorf("gzip body starts %q", raw[:20])
	}
	// With no worker script embedded the route is a plain 404.
	none := httptest.NewRecorder()
	newServiceWorker("abcdef012345", assets, nil).ServeHTTP(none, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if none.Code != http.StatusNotFound {
		t.Errorf("no script: %d", none.Code)
	}
}

// TestServiceWorkerRoute is the route on the outer mux: public, with the security headers and no
// cookie, listing every hashed asset but the notices and the worker. It needs js/sw.js (package B1).
func TestServiceWorkerRoute(t *testing.T) {
	a := newTestApp(t)
	set, err := static.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := offlineAGet(a, http.MethodGet, "/sw.js", nil)
	if !set.Has("js/sw.js") {
		if rec.Code != http.StatusNotFound {
			t.Fatalf("no worker embedded yet: %d, want 404", rec.Code)
		}
		t.Skip("js/sw.js is not embedded yet (package B1)")
	}
	if rec.Code != 200 || rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("GET /sw.js: %d headers %v", rec.Code, rec.Header())
	}
	m := regexp.MustCompile(`^self\.TS_BUILD = (\{.*\});\n`).FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no TS_BUILD line: %.80q", rec.Body)
	}
	var tb struct {
		Build  string   `json:"build"`
		Assets []string `json:"assets"`
	}
	if err := json.Unmarshal([]byte(m[1]), &tb); err != nil {
		t.Fatal(err)
	}
	if tb.Build != a.srv.swBuild || len(tb.Assets) != len(set.Precache()) {
		t.Errorf("build %q assets %d, want %q and %d", tb.Build, len(tb.Assets), a.srv.swBuild, len(set.Precache()))
	}
	for _, u := range tb.Assets {
		if strings.HasSuffix(u, ".txt") || strings.Contains(u, "/js/sw.") {
			t.Errorf("asset list holds %s", u)
		}
	}
	other := newTestApp(t, func(o *Options) { o.Version = "other" })
	if other.srv.swBuild == a.srv.swBuild {
		t.Error("two versions share one build id")
	}
}

type offlineAManifest struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ShortName       string `json:"short_name"`
	StartURL        string `json:"start_url"`
	Scope           string `json:"scope"`
	Display         string `json:"display"`
	BackgroundColor string `json:"background_color"`
	ThemeColor      string `json:"theme_color"`
	LaunchHandler   struct {
		ClientMode string `json:"client_mode"`
	} `json:"launch_handler"`
	Icons []struct {
		Src, Sizes, Type, Purpose string
	} `json:"icons"`
}

func TestManifest(t *testing.T) {
	a := newTestApp(t)
	rec := offlineAGet(a, http.MethodGet, "/manifest.webmanifest", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/manifest+json" ||
		rec.Header().Get("Cache-Control") != "no-cache" || rec.Header().Get("Set-Cookie") != "" ||
		rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("manifest: %d %v", rec.Code, rec.Header())
	}
	var m offlineAManifest
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.ID != "/" || m.Name != "Tendshelf" || m.ShortName != "Tendshelf" || m.StartURL != "/" || m.Scope != "/" ||
		m.Display != "standalone" || m.BackgroundColor != "#14110e" || m.ThemeColor != "#14110e" ||
		m.LaunchHandler.ClientMode != "focus-existing" || len(m.Icons) != 4 {
		t.Fatalf("manifest fields: %+v", m)
	}
	wantIcons := []struct{ sizes, typ, purpose string }{
		{"192x192", "image/png", "any"}, {"512x512", "image/png", "any"},
		{"512x512", "image/png", "maskable"}, {"any", "image/svg+xml", "any"},
	}
	for i, ic := range m.Icons {
		w := wantIcons[i]
		if ic.Sizes != w.sizes || ic.Type != w.typ || ic.Purpose != w.purpose || !strings.HasPrefix(ic.Src, "/static/img/") {
			t.Errorf("icon %d: %+v", i, ic)
		}
		got := offlineAGet(a, http.MethodGet, ic.Src, nil)
		if got.Code != 200 || got.Header().Get("Content-Type") != w.typ {
			t.Errorf("icon %s: %d %s", ic.Src, got.Code, got.Header().Get("Content-Type"))
			continue
		}
		if w.typ == "image/png" {
			cfg, _, err := image.DecodeConfig(got.Body)
			if want := map[string]int{"192x192": 192, "512x512": 512}[w.sizes]; err != nil || cfg.Width != want || cfg.Height != want {
				t.Errorf("icon %s is %dx%d (%v), want %s", ic.Src, cfg.Width, cfg.Height, err, w.sizes)
			}
		}
	}
	if rec := offlineAGet(a, http.MethodHead, "/manifest.webmanifest", nil); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", rec.Code, rec.Body)
	}
	if rec := offlineAGet(a, http.MethodPost, "/manifest.webmanifest", nil); rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

// TestHeadInstallLinks: the manifest, the Home Screen icon (180 px PNG) and title are on Layout and
// Frame pages and never on a share-link page.
func TestHeadInstallLinks(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	layout := a.do(http.MethodGet, "/", nil, nil, owner).Body.String()
	frame := offlineAGet(a, http.MethodGet, "/login", nil).Body.String()
	set, err := static.Load()
	if err != nil {
		t.Fatal(err)
	}
	apple := `<link rel="apple-touch-icon" href="` + set.URL("img/apple-touch-icon.png") + `">`
	for name, html := range map[string]string{"layout": layout, "frame": frame} {
		for _, w := range []string{`<link rel="manifest" href="/manifest.webmanifest">`, apple, `<meta name="apple-mobile-web-app-title" content="Tendshelf">`} {
			if !strings.Contains(html, w) {
				t.Errorf("%s lacks %s", name, w)
			}
		}
	}
	var buf bytes.Buffer
	pg := components.Page{Title: "Shared", Assets: set}
	if err := components.LinkShell(pg, "/s/tok", nil, templ.NopComponent).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"manifest", "apple-touch-icon", "apple-mobile-web-app-title", "ts-offline", "data-ts-person"} {
		if strings.Contains(buf.String(), bad) {
			t.Errorf("link shell has %q", bad)
		}
	}
	// Layout carries the person, the chunk size and the banner; Frame carries neither person nor banner.
	uid := sweepUserID(t, a, owner)
	for _, w := range []string{`data-ts-person="` + uid + `"`, `data-ts-chunk="4194304"`, `id="ts-offline"`, `data-ts-msg-offline=`, `data-ts-offline-retry`} {
		if !strings.Contains(layout, w) {
			t.Errorf("layout lacks %s", w)
		}
	}
	if strings.Contains(frame, "data-ts-person") || strings.Contains(frame, `id="ts-offline"`) {
		t.Error("frame carries the person or the banner")
	}
	small := newTestApp(t, func(o *Options) { o.DownloadChunk = 65536 })
	if !strings.Contains(small.do(http.MethodGet, "/", nil, nil, small.owner(t)).Body.String(), `data-ts-chunk="65536"`) {
		t.Error("DownloadChunk is not written to the page")
	}
}

func TestOfflineShell(t *testing.T) {
	a, owner, member, guest, book, _, _ := streamEnv(t)
	ctx := context.Background()
	for name, ck := range map[string]*http.Cookie{"owner": owner, "member": member, "guest": guest} {
		rec := a.do(http.MethodGet, "/offline", nil, nil, ck)
		if rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: %d cache %q", name, rec.Code, rec.Header().Get("Cache-Control"))
		}
		for _, w := range []string{"data-ts-offline-shell", "Showing what&#39;s on this phone.", "No downloads yet", `aria-current="page"`} {
			if !strings.Contains(rec.Body.String(), w) {
				t.Errorf("%s: shell lacks %q", name, w)
			}
		}
	}
	// The owner holds the book: a ready row with its play control, "Ready offline" alone (no progress).
	rec := a.do(http.MethodGet, "/offline?books="+book.ID+",bk_unknown", nil, nil, owner)
	body := rec.Body.String()
	for _, w := range []string{book.Title, "Ready offline", `data-ts-play="` + book.ID + `"`, "On this phone", "Bookmarks and progress you make now sync"} {
		if !strings.Contains(body, w) {
			t.Errorf("owner shell lacks %q", w)
		}
	}
	if strings.Contains(body, "Needs a connection") || strings.Contains(body, "bk_unknown") {
		t.Error("shell lists a book that is not on the phone or echoes an unknown id")
	}
	// Progress adds the percent (the book has no chapters) and sorts; another started book is listed
	// as needing a connection, after the ready ones.
	uid := sweepUserID(t, a, owner)
	if _, err := a.srv.progress.Save(ctx, progress.Update{UserID: uid, BookID: book.ID, PositionMs: 1500, DurationMs: 6000, ClientUpdatedAt: 10, DeviceID: "offlinedev001"}); err != nil {
		t.Fatal(err)
	}
	body = a.do(http.MethodGet, "/offline?books="+book.ID, nil, nil, owner).Body.String()
	if !strings.Contains(body, "Ready offline · 25%") && !strings.Contains(body, "Ready offline · Chapter 1") {
		t.Errorf("ready row has no place: %s", regexp.MustCompile(`Ready offline[^<]*`).FindString(body))
	}
	if body := a.do(http.MethodGet, "/offline", nil, nil, owner).Body.String(); !strings.Contains(body, book.Title) || !strings.Contains(body, "Needs a connection") || strings.Contains(body, "data-ts-play=") {
		t.Error("a book that is not on the phone is not listed as needing a connection, without a play control")
	}
	// Nothing leaks to a person who may not see the book: no title, no id, no play control.
	for _, ck := range []*http.Cookie{member, guest} {
		rec := a.do(http.MethodGet, "/offline?books="+book.ID, nil, nil, ck)
		if rec.Code != 200 || strings.Contains(rec.Body.String(), book.Title) || strings.Contains(rec.Body.String(), book.ID) {
			t.Errorf("hidden book appears for a person without access: %d", rec.Code)
		}
	}
	// A refused book is left out for everyone.
	streamExec(t, a, `UPDATE books SET refused_reason = 'drm' WHERE id = ?`, book.ID)
	if body := a.do(http.MethodGet, "/offline?books="+book.ID, nil, nil, owner).Body.String(); strings.Contains(body, "data-ts-play=") {
		t.Error("a refused book has a play control")
	}
	// Bad input is a 400 error page; 200 ids are allowed, 201 are not.
	ids := func(n int) string {
		s := make([]string, n)
		for i := range s {
			s[i] = "bk" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		}
		return strings.Join(s, ",")
	}
	for path, want := range map[string]int{
		"/offline?books=" + ids(200): 200, "/offline?books=" + ids(201): 400, "/offline?books=a%20b": 400,
		"/offline?books=a,,b": 400, "/offline?books=../x": 400, "/offline?books=" + strings.Repeat("a", 65): 400,
	} {
		if rec := a.do(http.MethodGet, path, nil, nil, owner); rec.Code != want {
			t.Errorf("GET %.60s: %d, want %d", path, rec.Code, want)
		}
	}
	// Anonymous visitors are sent to sign in; other methods are refused.
	if rec := offlineAGet(a, http.MethodGet, "/offline", nil); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("anonymous: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := a.do(http.MethodPut, "/offline", nil, nil, owner); rec.Code != 405 && rec.Code != 403 {
		t.Errorf("PUT: %d", rec.Code)
	}
}

// offlineAGrant gives the user a grant on the book's library with the download flag as given.
func offlineAGrant(t *testing.T, a *testApp, userID, libraryID string, canDownload int) {
	t.Helper()
	streamExec(t, a, `DELETE FROM grants WHERE user_id = ?`, userID)
	streamExec(t, a, `INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
		VALUES (?, ?, 'library', ?, ?, 0, 1, 1)`, "gr_"+userID, userID, libraryID, canDownload)
}

func TestStreamDownload(t *testing.T) {
	a, owner, member, _, book, files, _ := streamEnv(t)
	id := files[0].ID
	rng := map[string]string{"Range": "bytes=0-99"}
	code, h, body := streamGet(a, http.MethodGet, "/stream/"+id+"?download=1", rng, owner)
	if code != http.StatusPartialContent || len(body) != 100 || h.Get("Cache-Control") != "private, no-store" || h.Get("ETag") == "" {
		t.Fatalf("owner: %d %d bytes cache %q", code, len(body), h.Get("Cache-Control"))
	}
	if _, h, _ := streamGet(a, http.MethodGet, "/stream/"+id, rng, owner); h.Get("Cache-Control") != "private, no-cache" {
		t.Errorf("plain stream cache %q", h.Get("Cache-Control"))
	}
	for _, v := range []string{"2", "", "true", "1,1"} {
		code, _, body := streamGet(a, http.MethodGet, "/stream/"+id+"?download="+v, nil, owner)
		if code != 400 || string(body) != "Bad download value.\n" {
			t.Errorf("download=%q: %d %q", v, code, body)
		}
	}
	// A member who may listen but not download: 403 with the exact words; streaming still works.
	mid := sweepUserID(t, a, member)
	offlineAGrant(t, a, mid, book.LibraryID, 0)
	code, _, body = streamGet(a, http.MethodGet, "/stream/"+id+"?download=1", rng, member)
	if code != http.StatusForbidden || string(body) != "Downloads of this book are turned off.\n" {
		t.Errorf("no download right: %d %q", code, body)
	}
	if code, _, _ := streamGet(a, http.MethodGet, "/stream/"+id, rng, member); code != http.StatusPartialContent {
		t.Errorf("listening without the download right: %d", code)
	}
	// The same through the API alias, with a device token.
	tok := offlineATok(t, a, mid)
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/stream/"+id+"?download=1", tok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("API without the right: %d", rec.Code)
	}
	offlineAGrant(t, a, mid, book.LibraryID, 1)
	code, h, body = streamGet(a, http.MethodGet, "/stream/"+id+"?download=1", rng, member)
	if code != http.StatusPartialContent || len(body) != 100 || h.Get("Cache-Control") != "private, no-store" {
		t.Errorf("with the right: %d %d", code, len(body))
	}
	rec := deviceBearerReq(a, http.MethodGet, "/api/v1/stream/"+id+"?download=1", tok, func(r *http.Request) { r.Header.Set("Range", "bytes=0-99") })
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("API with the right: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/stream/"+id+"?download=2", tok, nil); rec.Code != 400 {
		t.Errorf("API download=2: %d", rec.Code)
	}
	// compat with download: the copy is not ready, so 503 with Retry-After, as without download.
	streamExec(t, a, `UPDATE audio_files SET container = 'ogg', codec = 'opus' WHERE id = ?`, id)
	code, h, _ = streamGet(a, http.MethodGet, "/stream/"+id+"?compat=1&download=1", nil, owner)
	if code != http.StatusServiceUnavailable || h.Get("Retry-After") != "3" {
		t.Errorf("compat+download: %d retry %q", code, h.Get("Retry-After"))
	}
}

// offlineATok issues a device token for a user.
func offlineATok(t *testing.T, a *testApp, userID string) string {
	t.Helper()
	var tok string
	err := a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(a.st, nil).Issue(context.Background(), tx, userID, "offline test", auth.KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestLogoutClearsSiteData(t *testing.T) {
	a := newTestApp(t)
	for _, role := range []users.Role{users.RoleOwner, users.RoleMember} {
		ck := a.as(t, role)
		tok := a.tokenFor(t, ck)
		rec := a.do(http.MethodPost, "/logout", nil, func(r *http.Request) { r.Header.Set("X-CSRF-Token", tok) }, ck)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?signed_out=1" ||
			rec.Header().Get("Clear-Site-Data") != `"storage"` {
			t.Errorf("%s: %d location %q clear %q", role, rec.Code, rec.Header().Get("Location"), rec.Header().Get("Clear-Site-Data"))
		}
	}
	// A refused logout (no CSRF token) clears nothing.
	ck := a.owner(t)
	if rec := a.do(http.MethodPost, "/logout", nil, nil, ck); rec.Header().Get("Clear-Site-Data") != "" {
		t.Error("Clear-Site-Data sent for a refused logout")
	}
}
