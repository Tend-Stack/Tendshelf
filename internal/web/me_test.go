package web

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const settingsChromeLinux = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// settingsLogin signs the owner in again, as another browser, and returns its session cookie.
func settingsLogin(t *testing.T, a *testApp, ua string) *http.Cookie {
	t.Helper()
	a.owner(t)
	c, tok := a.anon(t, "/login")
	rec := a.do(http.MethodPost, "/login", url.Values{"_csrf": {tok}, "username": {"ana"}, "password": {testPassword}},
		func(r *http.Request) { r.Header.Set("User-Agent", ua) }, c)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login: %d", rec.Code)
	}
	ck := cookieNamed(rec, auth.CookieName)
	if ck == nil {
		t.Fatal("login set no session cookie")
	}
	return &http.Cookie{Name: ck.Name, Value: ck.Value}
}

// settingsPost posts form as the person holding ck with a valid token; htmx adds the htmx header.
func settingsPost(t *testing.T, a *testApp, ck *http.Cookie, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	f := url.Values{"_csrf": {a.tokenFor(t, ck)}}
	for k, v := range form {
		f[k] = v
	}
	return a.do(http.MethodPost, path, f, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

func settingsOf(t *testing.T, a *testApp) users.Settings {
	t.Helper()
	u, _, err := a.srv.users.ByUsername(context.Background(), "ana")
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.srv.users.LoadSettings(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var (
	settingsHTMLTag   = regexp.MustCompile(`<html[^>]*>`)
	settingsThemeAttr = regexp.MustCompile(`data-theme="([a-z]+)"`)
)

// settingsTheme returns the data-theme of the <html> element, "" for none.
func settingsTheme(body string) string {
	tag := settingsHTMLTag.FindString(body)
	if tag == "" {
		return "?"
	}
	if m := settingsThemeAttr.FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

func TestMeAccess(t *testing.T) {
	a := newTestApp(t)
	for _, role := range []users.Role{users.RoleOwner, users.RoleMember, users.RoleGuest} {
		ck := a.as(t, role)
		for _, p := range []string{"/me", "/me/settings", "/me/password"} {
			if rec := a.do(http.MethodGet, p, nil, nil, ck); rec.Code != http.StatusOK {
				t.Errorf("%s GET %s = %d", role, p, rec.Code)
			}
		}
	}
	rec := a.do(http.MethodGet, "/me", nil, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next=%2Fme" {
		t.Errorf("anonymous /me = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	ck := a.owner(t)
	for _, p := range []string{"/me/settings", "/me/password", "/me/sessions/0123456789abcdef/signout", "/me/sessions/signout-others"} {
		if rec := a.do(http.MethodPost, p, url.Values{"theme": {"dark"}}, nil, ck); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s without a token = %d, want 403", p, rec.Code)
		}
	}
}

func TestSettingsSaveHTMX(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	form := url.Values{"theme": {"sepia"}, "theme_auto": {"0"}, "speed": {"1.25"}, "skip_back": {"10"}, "skip_forward": {"60"},
		"auto_rewind": {"0", "1"}, "next_in_series": {"0"}, "player_tint": {"0", "1"}, "big_buttons": {"0", "1"}}
	rec := settingsPost(t, a, ck, "/me/settings", form, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "true" {
		t.Fatalf("theme change = %d, HX-Refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
	if c := cookieNamed(rec, themeCookie); c == nil || c.Value != "sepia" || c.Path != "/" || !c.HttpOnly || c.MaxAge <= 0 {
		t.Fatalf("theme cookie = %+v", c)
	}
	want := users.Settings{Theme: "sepia", Speed: 1.25, SkipBackS: 10, SkipForwardS: 60, AutoRewind: true, NextInSeries: false, PlayerTint: true, BigButtons: true, TextSize: "standard"}
	if got := settingsOf(t, a); got != want {
		t.Fatalf("stored %+v", got)
	}
	// Nothing about the theme changes: no refresh.
	form.Set("speed", "1.5")
	rec = settingsPost(t, a, ck, "/me/settings", form, true)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Refresh") != "" {
		t.Fatalf("speed change = %d, HX-Refresh %q", rec.Code, rec.Header().Get("HX-Refresh"))
	}
	// "Match my phone" wins over the tiles and drops the cookie.
	form.Set("theme_auto", "1")
	rec = settingsPost(t, a, ck, "/me/settings", form, true)
	if rec.Header().Get("HX-Refresh") != "true" {
		t.Fatal("no refresh for the move to system")
	}
	if c := cookieNamed(rec, themeCookie); c == nil || c.MaxAge >= 0 {
		t.Fatalf("system must delete the cookie: %+v", c)
	}
	if got := settingsOf(t, a); got.Theme != "system" || got.Speed != 1.5 {
		t.Fatalf("stored %+v", got)
	}
}

func TestSettingsSavePlain(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	form := url.Values{"theme": {"light"}, "theme_auto": {"0"}, "speed": {"2.00"}, "skip_back": {"30"}, "skip_forward": {"15"},
		"auto_rewind": {"0"}, "next_in_series": {"0"}, "player_tint": {"0"}, "big_buttons": {"0"}}
	rec := settingsPost(t, a, ck, "/me/settings", form, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me/settings" {
		t.Fatalf("plain save = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	want := users.Settings{Theme: "light", Speed: 2, SkipBackS: 30, SkipForwardS: 15, TextSize: "standard"}
	if got := settingsOf(t, a); got != want {
		t.Fatalf("stored %+v, want %+v (unchecked switches are false)", got, want)
	}
	// The saved values show on the page.
	body := a.do(http.MethodGet, "/me/settings", nil, nil, ck).Body.String()
	for _, frag := range []string{`value="2.00" selected`, `id="st-skipb-30" name="skip_back" value="30" checked`, `id="st-theme-light" name="theme" value="light" checked`} {
		if !strings.Contains(body, frag) {
			t.Errorf("page lacks %q", frag)
		}
	}
}

func TestSettingsInvalid422(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	for name, form := range map[string]url.Values{
		"speed too high": {"speed": {"9"}}, "speed text": {"speed": {"fast"}}, "speed step": {"speed": {"1.07"}},
		"skip": {"skip_back": {"20"}}, "theme": {"theme": {"neon"}}, "switch": {"big_buttons": {"maybe"}}, "auto": {"theme_auto": {"yes"}}, "text size": {"text_size": {"huge"}},
	} {
		rec := settingsPost(t, a, ck, "/me/settings", form, true)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "ts-toast") {
			t.Errorf("%s htmx = %d", name, rec.Code)
		}
		rec = settingsPost(t, a, ck, "/me/settings", form, false)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), html.EscapeString(msgSettingInvalid)) {
			t.Errorf("%s plain = %d", name, rec.Code)
		}
	}
	if got := settingsOf(t, a); got != users.DefaultSettings {
		t.Fatalf("invalid posts changed the settings: %+v", got)
	}
	// An unknown key is ignored.
	if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"nope": {"1"}, "speed": {"1.10"}}, true); rec.Code != http.StatusNoContent {
		t.Fatalf("unknown key = %d", rec.Code)
	}
}

func TestThemePrecedence(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	cookie := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: themeCookie, Value: v}) }
	}
	get := func(p string, mod func(*http.Request), ck *http.Cookie) string {
		rec := a.do(http.MethodGet, p, nil, mod, ck)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", p, rec.Code)
		}
		return settingsTheme(rec.Body.String())
	}
	// No setting: the cookie, else the system.
	if got := get("/me", cookie("dark"), member); got != "dark" {
		t.Errorf("member with cookie = %q", got)
	}
	if got := get("/me", nil, member); got != "" {
		t.Errorf("member without anything = %q", got)
	}
	// A saved theme beats the cookie.
	u, _, _ := a.srv.users.ByUsername(context.Background(), "ana")
	s := users.DefaultSettings
	s.Theme = "light"
	if err := a.srv.users.SaveSettings(context.Background(), u.ID, s); err != nil {
		t.Fatal(err)
	}
	if got := get("/me", cookie("dark"), owner); got != "light" {
		t.Errorf("setting vs cookie = %q", got)
	}
	// "system" means no data-theme, even with a cookie left over.
	s.Theme = "system"
	if err := a.srv.users.SaveSettings(context.Background(), u.ID, s); err != nil {
		t.Fatal(err)
	}
	if got := get("/me", cookie("dark"), owner); got != "" {
		t.Errorf("system vs cookie = %q", got)
	}
	// Signed out, the sign-in pages use the cookie.
	if got := get("/login", cookie("sepia"), nil); got != "sepia" {
		t.Errorf("signed-out cookie = %q", got)
	}
	if got := get("/login", cookie("neon"), nil); got != "" {
		t.Errorf("invalid cookie = %q", got)
	}
}

func TestPasswordChangeFlow(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	other := settingsLogin(t, a, settingsChromeLinux)
	const next = "a brand new passphrase"
	post := func(form url.Values) *httptest.ResponseRecorder {
		return settingsPost(t, a, owner, "/me/password", form, false)
	}
	if rec := post(url.Values{"current": {"not it at all"}, "password": {next}}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), html.EscapeString(msgNotCurrentPassword)) {
		t.Fatalf("wrong current = %d", rec.Code)
	}
	if rec := post(url.Values{"current": {testPassword}, "password": {"short"}}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), html.EscapeString(fieldMessage("password", "too_short"))) {
		t.Fatalf("short = %d", rec.Code)
	}
	if rec := post(url.Values{"current": {testPassword}, "password": {"ana"}}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("same as username = %d", rec.Code)
	}
	rec := post(url.Values{"current": {testPassword}, "password": {next}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me?changed=password" {
		t.Fatalf("change = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	fresh := cookieNamed(rec, auth.CookieName)
	if fresh == nil || fresh.Value == owner.Value {
		t.Fatalf("the session cookie was not rotated: %+v", fresh)
	}
	body := a.do(http.MethodGet, "/me?changed=password", nil, nil, fresh).Body.String()
	if !strings.Contains(body, html.EscapeString(msgPasswordChanged)) {
		t.Fatal("no banner after the change")
	}
	for name, old := range map[string]*http.Cookie{"other device": other, "old cookie": owner} {
		rec := a.do(http.MethodGet, "/me", nil, nil, old)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
			t.Errorf("%s still signed in: %d", name, rec.Code)
		}
	}
	// The new password signs in; the old one does not.
	c, tok := a.anon(t, "/login")
	if rec := a.do(http.MethodPost, "/login", url.Values{"_csrf": {tok}, "username": {"ana"}, "password": {next}}, nil, c); rec.Code != http.StatusSeeOther {
		t.Fatalf("new password refused: %d", rec.Code)
	}
	if rec := a.do(http.MethodPost, "/login", url.Values{"_csrf": {tok}, "username": {"ana"}, "password": {testPassword}}, nil, c); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("old password accepted: %d", rec.Code)
	}
}

func TestPasswordChangeRateLimit(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	var rec *httptest.ResponseRecorder
	for i := 0; i < 11; i++ {
		rec = settingsPost(t, a, ck, "/me/password", url.Values{"current": {"wrong wrong wrong"}, "password": {"another new password"}}, false)
	}
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("after 11 tries = %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "Too many tries") {
		t.Fatal("no limit message")
	}
}

// settingsDeviceID is the public id of the session behind a cookie.
func settingsDeviceID(t *testing.T, ck *http.Cookie) string {
	t.Helper()
	h, ok := auth.HashToken(ck.Value)
	if !ok {
		t.Fatal("bad token")
	}
	return auth.PublicID(h)
}

func TestSessionSignOut(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	d2 := settingsLogin(t, a, settingsChromeLinux)
	d3 := settingsLogin(t, a, "curl/8")

	page := a.do(http.MethodGet, "/me", nil, nil, owner).Body.String()
	id2 := settingsDeviceID(t, d2)
	for _, frag := range []string{"/me/sessions/" + id2 + "/signout", `aria-label="Sign out Chrome on Linux"`, "This device", "Sign out everywhere else", "A web browser"} {
		if !strings.Contains(page, frag) {
			t.Errorf("Profile lacks %q", frag)
		}
	}
	if strings.Contains(page, "/me/sessions/"+settingsDeviceID(t, owner)+"/signout") {
		t.Error("the current device has a device sign-out form")
	}
	if strings.Contains(page, settingsDeviceID(t, member)) || strings.Contains(page, d2.Value) {
		t.Error("the page shows another person's id or a raw token")
	}

	// Another person's session id, and a made-up one, are not found.
	for _, sid := range []string{settingsDeviceID(t, member), "0123456789abcdef", "nothex"} {
		if rec := settingsPost(t, a, owner, "/me/sessions/"+sid+"/signout", nil, false); rec.Code != http.StatusNotFound {
			t.Errorf("sid %s = %d, want 404", sid, rec.Code)
		}
	}
	if rec := a.do(http.MethodGet, "/me", nil, nil, member); rec.Code != http.StatusOK {
		t.Fatal("the other person was signed out")
	}

	// Another device.
	if rec := settingsPost(t, a, owner, "/me/sessions/"+id2+"/signout", nil, false); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me" {
		t.Fatalf("sign out device = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := a.do(http.MethodGet, "/me", nil, nil, d2); rec.Code != http.StatusSeeOther {
		t.Fatal("device 2 still signed in")
	}
	if rec := a.do(http.MethodGet, "/me", nil, nil, owner); rec.Code != http.StatusOK {
		t.Fatal("the current session was ended by signing out another")
	}

	// Everywhere else keeps this one.
	d4 := settingsLogin(t, a, settingsChromeLinux)
	if rec := settingsPost(t, a, owner, "/me/sessions/signout-others", nil, false); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me" {
		t.Fatalf("sign out others = %d", rec.Code)
	}
	for name, ck := range map[string]*http.Cookie{"d3": d3, "d4": d4} {
		if rec := a.do(http.MethodGet, "/me", nil, nil, ck); rec.Code != http.StatusSeeOther {
			t.Errorf("%s still signed in", name)
		}
	}
	if rec := a.do(http.MethodGet, "/me", nil, nil, owner); rec.Code != http.StatusOK {
		t.Fatal("signing out others ended this session")
	}

	// The current device's own id is a sign-out, like /logout.
	rec := settingsPost(t, a, owner, "/me/sessions/"+settingsDeviceID(t, owner)+"/signout", nil, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("sign out this device = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if c := cookieNamed(rec, auth.CookieName); c == nil || c.MaxAge >= 0 {
		t.Fatal("the session cookie was not cleared")
	}
	if rec := a.do(http.MethodGet, "/me", nil, nil, owner); rec.Code != http.StatusSeeOther {
		t.Fatal("the current session survives its own sign-out")
	}
}

func TestDeviceLabel(t *testing.T) {
	tests := []struct{ ua, want string }{
		{"", "A web browser"},
		{"curl/8.5", "A web browser"},
		{settingsChromeLinux, "Chrome on Linux"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0", "Edge on Windows"},
		{"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0", "Firefox on Linux"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari on iPhone"},
		{"Mozilla/5.0 (iPad; CPU OS 17_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Mobile/15E148 Safari/604.1", "Safari on iPad"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15", "Safari on Mac"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36", "Chrome on Android"},
		{"Mozilla/5.0 (Android 14; Mobile; rv:130.0) Gecko/130.0 Firefox/130.0", "Firefox on Android"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130.0 Safari/537.36", "Chrome on Mac"},
		{"SomeApp (Windows NT 10.0)", "Windows"},
		{"Chrome/130.0", "Chrome"},
	}
	for _, tc := range tests {
		if got := deviceLabel(tc.ua); got != tc.want {
			t.Errorf("deviceLabel(%q) = %q, want %q", tc.ua, got, tc.want)
		}
	}
}

func TestProfileAdminRow(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	ob := a.do(http.MethodGet, "/me", nil, nil, owner).Body.String()
	for _, frag := range []string{"Your server", `href="/admin/libraries"`, "0 folders", "Ana Reyes", "ts-role--owner", ">Owner<", "Tendshelf test · open source", `action="/logout"`, "ts-avatar"} {
		if !strings.Contains(ob, frag) {
			t.Errorf("owner Profile lacks %q", frag)
		}
	}
	mb := a.do(http.MethodGet, "/me", nil, nil, member).Body.String()
	for _, frag := range []string{"Your server", "/admin/libraries", "ts-role--owner"} {
		if strings.Contains(mb, frag) {
			t.Errorf("member Profile has %q", frag)
		}
	}
	if !strings.Contains(mb, ">Member<") {
		t.Error("member Profile lacks the role")
	}
}

func TestMeNoInlineStyles(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	for _, p := range []string{"/me", "/me/settings", "/me/password"} {
		body := a.do(http.MethodGet, p, nil, nil, ck).Body.String()
		if strings.Contains(body, ` style="`) || strings.Contains(body, "<script>") || strings.Contains(body, " hx-on") {
			t.Errorf("%s has an inline style, script or hx-on", p)
		}
		if !strings.Contains(body, `href="/static/css/pages-me.`) {
			t.Errorf("%s does not link pages-me.css", p)
		}
	}
}
