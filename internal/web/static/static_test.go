package static

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

const htmxSHA256 = "e484d9171a9db30a39c8f16e3d709d4137f3211c659f8e6125816635033d593f"

var hashedName = regexp.MustCompile(`\.[0-9a-f]{10}\.[a-z0-9]+$`)

func mustLoad(t *testing.T) *Set {
	t.Helper()
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestLoadHashesAndTypes(t *testing.T) {
	s := mustLoad(t)
	want := map[string]string{
		".css": "text/css; charset=utf-8", ".js": "text/javascript; charset=utf-8",
		".woff2": "font/woff2", ".svg": "image/svg+xml", ".txt": "text/plain; charset=utf-8",
		".png": "image/png",
	}
	names := s.Names()
	if len(names) < 19 {
		t.Fatalf("only %d assets: %v", len(names), names)
	}
	for _, n := range names {
		a, _ := s.Asset(n)
		if !hashedName.MatchString(a.Hashed) {
			t.Errorf("%s: hashed name %q has no 10-hex hash", n, a.Hashed)
		}
		ext := n[strings.LastIndex(n, "."):]
		if a.Type != want[ext] {
			t.Errorf("%s: type %q, want %q", n, a.Type, want[ext])
		}
		if got := s.URL(n); got != "/static/"+a.Hashed {
			t.Errorf("%s: URL %q", n, got)
		}
		sum := sha256.Sum256(a.Body)
		if !strings.Contains(a.Hashed, hex.EncodeToString(sum[:])[:10]) {
			t.Errorf("%s: hash does not match the final body", n)
		}
	}
}

func TestURLPanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("URL of an unknown asset must panic")
		}
	}()
	mustLoad(t).URL("css/nope.css")
}

func TestStaticImmutable(t *testing.T) {
	s := mustLoad(t)
	for _, n := range s.Names() {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, s.URL(n), nil))
		a, _ := s.Asset(n)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d", n, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("%s: Cache-Control %q", n, got)
		}
		if rec.Header().Get("Content-Type") != a.Type {
			t.Errorf("%s: Content-Type %q", n, rec.Header().Get("Content-Type"))
		}
		if rec.Body.Len() != len(a.Body) {
			t.Errorf("%s: body length %d, want %d", n, rec.Body.Len(), len(a.Body))
		}
	}
}

func TestStaticUnhashed404(t *testing.T) {
	s := mustLoad(t)
	for _, p := range []string{"/static/css/tokens.css", "/static/css/tokens.0000000000.css", "/static/", "/static/../go.mod", "/static/nope.js"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: a 404 must not be cached", p)
		}
	}
}

func bodyOf(t *testing.T, s *Set, name string) string {
	t.Helper()
	a, ok := s.Asset(name)
	if !ok {
		t.Fatalf("no asset %s", name)
	}
	return string(a.Body)
}

func TestCSSLayerWrap(t *testing.T) {
	s := mustLoad(t)
	tokens := bodyOf(t, s, "css/tokens.css")
	if !strings.HasPrefix(tokens, "@layer tokens, components, utilities, motion, pages;\n@layer tokens {\n") {
		t.Fatalf("tokens.css starts with %.80q", tokens)
	}
	if !strings.HasPrefix(bodyOf(t, s, "css/bundle.css"), "@layer components {\n") {
		t.Fatal("bundle.css is not wrapped in the components layer")
	}
	if !strings.HasPrefix(bodyOf(t, s, "css/fonts.css"), "@layer tokens {\n") {
		t.Fatal("fonts.css is not wrapped in the tokens layer")
	}
}

func TestSourcesHaveNoLayer(t *testing.T) {
	for _, n := range []string{"css/tokens.css", "css/bundle.css", "css/fonts.css", "css/utilities.css", "css/motion.css"} {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "@layer") {
			t.Errorf("source %s must not contain @layer", n)
		}
	}
}

func TestSystemThemeBlocks(t *testing.T) {
	s := mustLoad(t)
	src, err := os.ReadFile("css/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	out := bodyOf(t, s, "css/tokens.css")
	for theme, query := range map[string]string{"light": "prefers-color-scheme: light", "contrast": "prefers-contrast: more"} {
		want, ok := themeBody(string(src), theme)
		if !ok || strings.TrimSpace(want) == "" {
			t.Fatalf("no %s block in the source", theme)
		}
		block := "@media (" + query + "){:root:not([data-theme]){" + want + "}}"
		if !strings.Contains(out, block) {
			t.Errorf("generated %s system block missing or different", theme)
		}
		// The source block itself is still present for an explicit data-theme.
		if !strings.Contains(out, `[data-theme="`+theme+`"] {`+want+`}`) {
			t.Errorf("explicit %s block missing", theme)
		}
	}
}

func TestFontURLsRewritten(t *testing.T) {
	s := mustLoad(t)
	css := bodyOf(t, s, "css/fonts.css")
	if strings.Contains(css, `url("/static/fonts/Fraunces-Variable.woff2")`) {
		t.Fatal("unhashed font URL left in fonts.css")
	}
	urls := regexp.MustCompile(`url\("([^"]+)"\)`).FindAllStringSubmatch(css, -1)
	if len(urls) != 8 {
		t.Fatalf("fonts.css refers to %d fonts, want 8", len(urls))
	}
	for _, m := range urls {
		if !hashedName.MatchString(m[1]) || !strings.HasPrefix(m[1], "/static/fonts/") {
			t.Errorf("font URL %q is not hashed", m[1])
		}
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, m[1], nil))
		if rec.Code != http.StatusOK {
			t.Errorf("font URL %q: status %d", m[1], rec.Code)
		}
	}
}

func TestFontBudget(t *testing.T) {
	s := mustLoad(t)
	total, preload := 0, 0
	for _, n := range s.Names() {
		if strings.HasSuffix(n, ".woff2") {
			a, _ := s.Asset(n)
			total += len(a.Body)
		}
	}
	for _, n := range []string{"fonts/Fraunces-Variable.woff2", "fonts/FiraSans-Regular.woff2"} {
		a, _ := s.Asset(n)
		preload += len(a.Body)
	}
	if total > 1_300_000 || preload > 400_000 {
		t.Fatalf("fonts %d B (max 1.3 MB), preloaded %d B (max 400 KB)", total, preload)
	}
}

func TestHTMXChecksum(t *testing.T) {
	b, err := os.ReadFile("vendor/htmx-4.0.0.min.js")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != htmxSHA256 {
		t.Fatalf("htmx sha256 = %s, want %s", got, htmxSHA256)
	}
}

func TestUnknownCSSFails(t *testing.T) {
	fsys := fstest.MapFS{"css/mystery.css": {Data: []byte("a{}")}}
	if _, err := load(fsys); err == nil || !strings.Contains(err.Error(), "mystery.css") {
		t.Fatalf("err = %v, want an error naming mystery.css", err)
	}
}

func TestMissingFontFails(t *testing.T) {
	fsys := fstest.MapFS{"css/fonts.css": {Data: []byte(`a{src:url("/static/fonts/Nope.woff2")}`)}}
	if _, err := load(fsys); err == nil {
		t.Fatal("a font URL with no file must fail Load")
	}
}
