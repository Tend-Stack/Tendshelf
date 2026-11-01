package web

import (
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

func getPage(t *testing.T, path string, mod func(*http.Request)) (*httptest.ResponseRecorder, string) {
	t.Helper()
	rec := send(NewHandler(Options{}), http.MethodGet, path, nil, mod)
	return rec, rec.Body.String()
}

// attrOf returns the unescaped value of attribute attr in the first tag that starts with prefix.
func attrOf(t *testing.T, rawBody, prefix, attr string) string {
	t.Helper()
	i := strings.Index(rawBody, prefix)
	if i < 0 {
		t.Fatalf("no tag %q", prefix)
	}
	tag := rawBody[i : i+strings.Index(rawBody[i:], ">")]
	m := regexp.MustCompile(`\s` + regexp.QuoteMeta(attr) + `=("([^"]*)"|'([^']*)')`).FindStringSubmatch(tag)
	if m == nil {
		t.Fatalf("tag %q has no %s", tag, attr)
	}
	return html.UnescapeString(m[2] + m[3])
}

func TestLayoutShell(t *testing.T) {
	rec, body := getPage(t, "/", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d, Cache-Control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	must := []string{
		`<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover"`,
		`<meta name="color-scheme" content="dark light"`,
		`<title>Home · Tendshelf</title>`,
		`<main id="main" class="ts-main" tabindex="-1" hx-history-elt>`,
		`<a class="ts-sr-focusable" href="#main">Skip to content</a>`,
	}
	for _, m := range must {
		if !strings.Contains(body, m) {
			t.Errorf("missing %q", m)
		}
	}
	if got := attrOf(t, body, `<meta name="htmx-config"`, "content"); got != `{"includeIndicatorCSS":false,"defaultTimeout":25000,"transitions":false}` {
		t.Errorf("htmx-config = %s", got)
	}
	if got := attrOf(t, body, `<body`, "hx-boost:inherited"); got != "target:#main select:#main swap:outerSync transition:true" {
		t.Errorf("hx-boost:inherited = %q", got)
	}
	if got := attrOf(t, body, `<body`, "hx-headers:inherited"); !regexp.MustCompile(`^\{"X-CSRF-Token":"[A-Za-z0-9_-]{43}"\}$`).MatchString(got) {
		t.Errorf("hx-headers:inherited = %s", got)
	}
	for _, id := range []string{"main", "player", "audio", "toasts", "sheet", "nav-tabbar", "nav-rail", "nav-side"} {
		if !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("missing id %s", id)
		}
	}
	for _, id := range []string{"player", "audio", "nav-tabbar", "nav-rail", "nav-side"} {
		tag := regexp.MustCompile(`<[a-z]+ [^>]*id="` + id + `"[^>]*>`).FindString(body)
		if !strings.Contains(tag, "hx-preserve") {
			t.Errorf("#%s lacks hx-preserve: %s", id, tag)
		}
	}
	// The sheet region is empty without a sheet and is swapped into, so it is not preserved.
	if !strings.Contains(body, `<div id="sheet" class="ts-sheet-region"></div>`) {
		t.Error(`want an empty <div id="sheet" class="ts-sheet-region"></div>`)
	}
	// Player, audio, toasts and sheets sit outside #main.
	mainStart, mainEnd := strings.Index(body, `<main `), strings.Index(body, `</main>`)
	for _, id := range []string{"player", "audio", "toasts", "sheet", "nav-tabbar", "nav-rail", "nav-side"} {
		if i := strings.Index(body, `id="`+id+`"`); i > mainStart && i < mainEnd {
			t.Errorf("#%s is inside #main", id)
		}
	}
	// Every asset URL is content-hashed, and each one is served.
	urls := regexp.MustCompile(`(?:href|src)="(/static/[^"]+)"`).FindAllStringSubmatch(body, -1)
	if len(urls) < 8 {
		t.Fatalf("only %d asset URLs", len(urls))
	}
	h := NewHandler(Options{})
	hashed := regexp.MustCompile(`\.[0-9a-f]{10}\.[a-z0-9]+$`)
	for _, m := range urls {
		if !hashed.MatchString(m[1]) {
			t.Errorf("asset URL %q is not hashed", m[1])
		}
		if r := send(h, http.MethodGet, m[1], nil, nil); r.Code != http.StatusOK {
			t.Errorf("%s: status %d", m[1], r.Code)
		}
	}
	// Stylesheet order.
	order := []string{"css/tokens", "css/fonts", "css/bundle", "css/utilities", "css/motion"}
	last := -1
	for _, n := range order {
		i := strings.Index(body, "/static/"+n+".")
		if i < 0 || i < last {
			t.Fatalf("stylesheet %s missing or out of order", n)
		}
		last = i
	}
	if strings.Count(body, `rel="preload"`) != 2 {
		t.Error("want exactly two font preloads")
	}
}

func TestThemeAttribute(t *testing.T) {
	cases := []struct{ cookie, want string }{
		{"dark", "dark"}, {"light", "light"}, {"sepia", "sepia"}, {"contrast", "contrast"},
		{"neon", ""}, {`<script>`, ""}, {"", ""},
	}
	for _, c := range cases {
		t.Run(c.cookie, func(t *testing.T) {
			_, body := getPage(t, "/", func(r *http.Request) {
				if c.cookie != "" {
					r.AddCookie(&http.Cookie{Name: themeCookie, Value: c.cookie})
				}
			})
			tag := regexp.MustCompile(`<html[^>]*>`).FindString(body)
			if c.want == "" {
				if strings.Contains(tag, "data-theme") {
					t.Fatalf("unexpected data-theme: %s", tag)
				}
				return
			}
			if !strings.Contains(tag, `data-theme="`+c.want+`"`) {
				t.Fatalf("html tag %s lacks data-theme=%q", tag, c.want)
			}
		})
	}
}

func TestAriaCurrentHome(t *testing.T) {
	_, body := getPage(t, "/", nil)
	links := regexp.MustCompile(`<a [^>]*data-nav="[^"]*"[^>]*>`).FindAllString(body, -1)
	if len(links) != 15 {
		t.Fatalf("%d nav links, want 15 (5 destinations in 3 navs)", len(links))
	}
	for _, l := range links {
		isHome := strings.Contains(l, `data-nav="/"`)
		if isHome != strings.Contains(l, `aria-current="page"`) {
			t.Errorf("aria-current wrong on %s", l)
		}
	}
}

func TestBoostedRequestFullPage(t *testing.T) {
	_, plain := getPage(t, "/", nil)
	_, boosted := getPage(t, "/", func(r *http.Request) {
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Boosted", "true")
	})
	norm := func(s string) string {
		return regexp.MustCompile(`X-CSRF-Token&#34;:&#34;[^&]*`).ReplaceAllString(s, "")
	}
	if !strings.Contains(boosted, "<!doctype html>") && !strings.Contains(strings.ToLower(boosted), "<!doctype html>") {
		t.Fatal("boosted request did not get a full page")
	}
	if norm(plain) != norm(boosted) {
		t.Fatal("boosted and plain responses differ")
	}
}

func TestError404Page(t *testing.T) {
	rec, raw := getPage(t, "/no/such/page", nil)
	body := html.UnescapeString(raw)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d", rec.Code)
	}
	for _, w := range []string{"This page isn't here.", "Check the address, or go back to Home.", "Go to Home", `id="main"`} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, bad := range []string{"goroutine", "panic", ".go:", "runtime."} {
		if strings.Contains(body, bad) {
			t.Errorf("404 page leaks %q", bad)
		}
	}
	if strings.Contains(body, `aria-current="page"`) {
		t.Error("no nav item is current on the 404 page")
	}
}

func TestErrorPagesPlainWords(t *testing.T) {
	h := NewHandler(Options{})
	rec := send(h, http.MethodPost, "/", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	for _, w := range []string{"This form expired.", "Reload the page and try again."} {
		if !strings.Contains(html.UnescapeString(rec.Body.String()), w) {
			t.Errorf("403 page lacks %q", w)
		}
	}
	for _, c := range []struct {
		status int
		words  []string
	}{
		{500, []string{"Something went wrong on your server.", "Try again in a moment. Details are in the server log.", "Try again"}},
		{403, []string{"This form expired."}},
	} {
		rec := httptest.NewRecorder()
		(&server{assets: mustAssets(t), csrf: NewCSRF(nil)}).errorPage(rec, httptest.NewRequest(http.MethodGet, "/x", nil), c.status)
		if rec.Code != c.status {
			t.Errorf("status %d, want %d", rec.Code, c.status)
		}
		for _, w := range c.words {
			if !strings.Contains(html.UnescapeString(rec.Body.String()), w) {
				t.Errorf("%d page lacks %q", c.status, w)
			}
		}
	}
}

func TestTemplatesCSPClean(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`\sstyle=`),
		regexp.MustCompile(`<script(\s[^>]*)?>`),
		regexp.MustCompile(`\bhx-on`),
		regexp.MustCompile(`\bjs:`),
		regexp.MustCompile(`hx-trigger="[^"]*\[`),
	}
	check := func(where, text string) {
		for _, re := range banned {
			for _, m := range re.FindAllString(text, -1) {
				if re.String() == `<script(\s[^>]*)?>` && strings.Contains(m, " src=") {
					continue
				}
				t.Errorf("%s: banned construct %q (%s)", where, m, re)
			}
		}
	}
	err := filepath.WalkDir(".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".templ") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		check(p, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/missing"} {
		_, body := getPage(t, path, nil)
		check("GET "+path, body)
	}
	// The error fragments are checked too.
	h := NewHandler(Options{})
	rec := send(h, http.MethodPost, "/", nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") })
	check("403 toast", rec.Body.String())
	rec = send(h, http.MethodPost, "/", nil, nil)
	check("403 page", rec.Body.String())
}

func mustAssets(t *testing.T) *static.Set {
	t.Helper()
	a, err := static.Load()
	if err != nil {
		t.Fatal(err)
	}
	return a
}
