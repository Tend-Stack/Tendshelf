package web

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// rsetPost posts the reading settings form as a person; htmx asks for the 204 answer.
func rsetPost(t *testing.T, l *rdrLab, ck *http.Cookie, htmx bool, kv ...string) (code int, location, body string) {
	form := url.Values{"_csrf": {l.a.tokenFor(t, ck)}}
	for i := 0; i+1 < len(kv); i += 2 {
		form.Add(kv[i], kv[i+1])
	}
	rec := l.a.do(http.MethodPost, readerSettingsRoute, form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
	return rec.Code, rec.Header().Get("Location"), rec.Body.String()
}

func (l *rdrLab) rsetStored(t *testing.T, ck *http.Cookie) users.ReaderSettings {
	t.Helper()
	rs, err := l.a.srv.users.LoadReader(context.Background(), sweepUserID(t, l.a, ck))
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

func TestReaderSettingsRoute(t *testing.T) {
	l := rdrSeed(t)
	// htmx: 204, and the change is stored; theme goes to ui.theme.
	if code, _, _ := rsetPost(t, l, l.owner, true, "size", "22", "theme", "sepia"); code != http.StatusNoContent {
		t.Fatalf("htmx post: %d", code)
	}
	rs := l.rsetStored(t, l.owner)
	if rs.Size != 22 || rs.Font != "serif" {
		t.Errorf("stored %+v", rs)
	}
	set, err := l.a.srv.users.LoadSettings(context.Background(), l.ownerID)
	if err != nil || set.Theme != "sepia" {
		t.Errorf("theme %q, %v", set.Theme, err)
	}
	// Fields are merged over the stored value; keys the reader no longer has are ignored.
	rsetPost(t, l, l.owner, true, "font", "dyslexic", "word", "0", "view", "focus")
	rs = l.rsetStored(t, l.owner)
	if rs.Size != 22 || rs.Font != "dyslexic" {
		t.Errorf("merged %+v", rs)
	}
	// Plain post: 303 to next when it is a book path, else to /.
	for next, want := range map[string]string{
		"/books/abc/read?ch=2":  "/books/abc/read?ch=2",
		"//evil.example":        "/",
		"/books//evil":          "/",
		"https://evil.example/": "/",
		"/me":                   "/",
		"":                      "/",
	} {
		code, loc, _ := rsetPost(t, l, l.owner, false, "leading", "airy", "next", next)
		if code != http.StatusSeeOther || loc != want {
			t.Errorf("next %q: %d %q, want %q", next, code, loc, want)
		}
	}
	// Invalid values: 400 and nothing changes. htmx gets the toast words.
	before := l.rsetStored(t, l.owner)
	for _, kv := range [][]string{{"size", "19"}, {"size", "big"}, {"font", "comic"}, {"leading", "x"}, {"theme", "pink"}} {
		code, _, body := rsetPost(t, l, l.owner, true, kv...)
		if code != http.StatusBadRequest || !strings.Contains(body, readerSettingsInvalid) {
			t.Errorf("%v: %d %q", kv, code, body)
		}
		if code, _, _ := rsetPost(t, l, l.owner, false, kv...); code != http.StatusBadRequest {
			t.Errorf("plain %v: %d", kv, code)
		}
	}
	if got := l.rsetStored(t, l.owner); got != before {
		t.Errorf("refused posts changed the row: %+v", got)
	}
	// Another person is not affected.
	if got := l.rsetStored(t, l.reader); got != users.DefaultReader {
		t.Errorf("second person has %+v", got)
	}
	// Someone not signed in gets nothing saved.
	form := url.Values{"size": {"26"}}
	if rec := l.a.do(http.MethodPost, readerSettingsRoute, form, nil); rec.Code < 300 && rec.Code != 0 && rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous post: %d", rec.Code)
	}
	if got := l.rsetStored(t, l.owner); got != before {
		t.Errorf("anonymous post changed the row: %+v", got)
	}
}

func TestReaderSettingsPage(t *testing.T) {
	l := rdrSeed(t)
	rsetPost(t, l, l.owner, true, "font", "sans", "size", "18", "leading", "airy")
	body := l.get(l.owner, l.readPath(""), false).Body.String()
	for _, want := range []string{`ts-reader--size-18`, `ts-reader--leading-airy`, `ts-reader--sans`,
		`href="/books/` + l.book.ID + `/read?sheet=reading"`, `aria-label="Reading settings"`} {
		if !strings.Contains(body, want) && !strings.Contains(body, strings.ReplaceAll(want, "&amp;", "&")) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(body, "ts-reader--dyslexic") {
		t.Error("dyslexic class without the choice")
	}
	rsetPost(t, l, l.owner, true, "font", "dyslexic")
	if !strings.Contains(l.get(l.owner, l.readPath(""), false).Body.String(), "ts-reader--dyslexic") {
		t.Error("dyslexic class missing")
	}
}

func TestReaderSettingsSheet(t *testing.T) {
	l := rdrSeed(t)
	rsetPost(t, l, l.owner, true, "size", "20", "theme", "light")
	sheet := l.get(l.owner, l.readPath("sheet=reading"), false)
	if sheet.Code != http.StatusOK {
		t.Fatalf("sheet: %d", sheet.Code)
	}
	body := sheet.Body.String()
	for _, want := range []string{`role="dialog"`, `Reading settings`, `data-ts-reader-settings`, `hx-post="/me/reader-settings"`, `hx-trigger="change"`, `hx-sync="this:queue last"`,
		`name="next"`, `<noscript>`, `OpenDyslexic`, `Sepia`, `Contrast`, `role="radiogroup"`, `value="light" checked`, `id="rs-size-22"`} {
		if !strings.Contains(body, want) {
			t.Errorf("sheet misses %q", want)
		}
	}
	if strings.Contains(body, ` style="`) {
		t.Error("inline style in the sheet")
	}
	for _, gone := range []string{`Underline each word`, `Follow the voice`, `Return automatically`, `Focus line`, `Highlight</legend>`, `data-ts-no-word`} {
		if strings.Contains(body, gone) {
			t.Errorf("sheet still has %q", gone)
		}
	}
	// 26 px joins the sizes only in large text mode.
	if strings.Contains(body, `id="rs-size-26"`) {
		t.Error("26 px offered in standard text")
	}
	if err := l.a.srv.users.SaveSettings(context.Background(), l.ownerID, users.Settings{Theme: "light", Speed: 1, SkipBackS: 15, SkipForwardS: 30, TextSize: "large", Nav: "full"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(l.get(l.owner, l.readPath("sheet=reading"), false).Body.String(), `id="rs-size-26"`) {
		t.Error("26 px missing in large text")
	}
	// The sheet needs the right to read the text, like the page.
	if rec := l.get(l.noRead, l.readPath("sheet=reading"), false); rec.Code != http.StatusNotFound {
		t.Errorf("no read right: %d", rec.Code)
	}
}

func TestReaderFontVendored(t *testing.T) {
	notices, err := os.ReadFile("../../NOTICES.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, sum := range map[string]string{
		"OpenDyslexic-Regular.woff2": "f007004af3cda5d8076e57c943f8cc8d00a0da25988b1ae1048683d60e7cac1a",
		"OpenDyslexic-Bold.woff2":    "dd9fa9c7991113b0dddefe9506a30ad26b48e302b7a8fb91719a4726e8fde85b",
		"OpenDyslexic-Italic.woff2":  "eb6a1bacf7e7c87a08116af4cd00a82064fbe61647b9a0d4d70a339890268f88",
		"OFL-OpenDyslexic.txt":       "caafcccfb70fc72458fcbda812ec8f0a06cb300cbabb87eabbb30b946124394b",
	} {
		raw, err := os.ReadFile("static/fonts/" + name)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != sum {
			t.Errorf("%s SHA-256 %s, want %s", name, got, sum)
		}
		if !strings.Contains(string(notices), sum) || !strings.Contains(string(notices), name) {
			t.Errorf("NOTICES.md does not name %s with its SHA-256", name)
		}
	}
	ofl, _ := os.ReadFile("static/fonts/OFL-OpenDyslexic.txt")
	if !strings.Contains(string(ofl), "SIL OPEN FONT LICENSE Version 1.1") {
		t.Error("the OFL text is not there")
	}
	css, err := os.ReadFile("static/css/pages-reader.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Regular", "Bold", "Italic"} {
		if !strings.Contains(string(css), "/static/fonts/OpenDyslexic-"+f+".woff2") {
			t.Errorf("@font-face for %s missing", f)
		}
	}
	// Loaded only when chosen: the layout never preloads it.
	layout, _ := os.ReadFile("components/layout.templ")
	if strings.Contains(string(layout), "OpenDyslexic") {
		t.Error("the layout mentions OpenDyslexic")
	}
}

func TestFeelReaderVolumeKeys(t *testing.T) {
	l := rdrSeed(t)
	sheet := func(extra ...*http.Cookie) string {
		return l.a.do(http.MethodGet, l.readPath("sheet=reading"), nil, nil, append([]*http.Cookie{l.owner}, extra...)...).Body.String()
	}
	if b := sheet(a1Android); !strings.Contains(b, `name="volume_keys"`) || !strings.Contains(b, "Turn pages with the volume buttons") {
		t.Error("the sheet in the app lacks the switch")
	}
	if b := sheet(); strings.Contains(b, `name="volume_keys"`) || strings.Contains(b, "volume buttons") {
		t.Error("the sheet in a browser shows the switch")
	}
	if code, _, _ := rsetPost(t, l, l.owner, true, "volume_keys", "0", "volume_keys", "1"); code != http.StatusNoContent || !l.rsetStored(t, l.owner).VolumeKeys {
		t.Fatalf("save on = %d", code)
	}
	if b := l.get(l.owner, l.readPath(""), false).Body.String(); !strings.Contains(b, `data-volume-keys="true"`) {
		t.Error("the reader root lacks data-volume-keys=true")
	}
	if b := sheet(a1Android); !strings.Contains(b, `<details class="ts-readset__more" open`) {
		t.Error("More stays closed with the switch on")
	}
	// Signed out, nothing is saved.
	rec := l.a.do(http.MethodPost, readerSettingsRoute, url.Values{"volume_keys": {"0"}}, nil)
	if rec.Code == http.StatusNoContent || !l.rsetStored(t, l.owner).VolumeKeys {
		t.Errorf("signed-out post = %d, stored %v", rec.Code, l.rsetStored(t, l.owner).VolumeKeys)
	}
}
