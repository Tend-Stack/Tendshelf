// Package static embeds the web assets (CSS, JS, vendored htmx, fonts, images) and serves them
// under content-hashed names, so every URL can be cached as immutable.
//
// Hashing and compression happen once at startup in Load. CSS source files stay pristine copies of
// the design handoff: the cascade-layer wrapping, the system-theme blocks and the font URL rewrite
// are applied at load time. Text assets (CSS, JS, SVG, TXT) are also stored gzipped and served to
// clients that accept it, with a separate ETag per encoding. Range requests are ignored: the files
// are small and immutable, so the answer is always the whole body.
package static

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

//go:embed css js vendor fonts img
var files embed.FS

// Asset is one served file.
type Asset struct {
	Name   string // source path under the static tree, e.g. "css/tokens.css"
	Hashed string // served path, e.g. "css/tokens.3fa1c09b2e.css"
	Body   []byte
	Type   string // Content-Type
	// Gzip is the compressed Body; nil when the type is not compressible or gzip is not smaller.
	Gzip []byte
	// ETag and GzipETag are the strong validators of the identity and the gzip body.
	ETag, GzipETag string
}

// Set is the loaded, hashed asset collection.
type Set struct {
	byName   map[string]*Asset
	byHashed map[string]*Asset
}

// layerOrder is the cascade layer order declared at the top of tokens.css.
const layerOrder = "@layer tokens, components, utilities, motion, pages;"

// layerOf maps a CSS source file to its cascade layer. An unknown CSS file makes Load fail.
func layerOf(name string) (string, bool) {
	switch base := path.Base(name); {
	case base == "tokens.css", base == "fonts.css":
		return "tokens", true
	case base == "bundle.css":
		return "components", true
	case base == "utilities.css":
		return "utilities", true
	case base == "motion.css":
		return "motion", true
	case strings.HasPrefix(base, "pages-") && strings.HasSuffix(base, ".css"), base == "player.css", base == "player-fit.css", base == "now-panel.css":
		return "pages", true
	}
	return "", false
}

var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".woff2": "font/woff2",
	".svg":   "image/svg+xml",
	".txt":   "text/plain; charset=utf-8",
}

// compressible lists the types worth gzipping. Fonts and images are already compressed.
var compressible = map[string]bool{".css": true, ".js": true, ".svg": true, ".txt": true}

// gzipBody compresses b the same way every time: no file name and a zero modification time, so two
// starts of the server (and two builds) produce identical bytes.
func gzipBody(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	loadOnce sync.Once
	loaded   *Set
	loadErr  error
)

// Load reads the embedded tree and builds the hashed set. The tree is embedded and a Set is
// read-only once built, so it is built once per process and shared.
func Load() (*Set, error) {
	loadOnce.Do(func() { loaded, loadErr = load(files) })
	return loaded, loadErr
}

func load(fsys fs.FS) (*Set, error) {
	raw := map[string][]byte{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		raw[p] = b
		return nil
	})
	if err != nil {
		return nil, err
	}

	s := &Set{byName: map[string]*Asset{}, byHashed: map[string]*Asset{}}
	var cssNames []string
	for name, body := range raw {
		ext := path.Ext(name)
		typ, ok := contentTypes[ext]
		if !ok {
			return nil, fmt.Errorf("static: no content type for %s", name)
		}
		if ext == ".css" {
			if _, ok := layerOf(name); !ok {
				return nil, fmt.Errorf("static: CSS file %s has no cascade layer", name)
			}
			cssNames = append(cssNames, name)
			continue
		}
		if err := s.add(name, body, typ); err != nil {
			return nil, err
		}
	}

	// Fonts are hashed first, then the CSS that refers to them.
	sort.Strings(cssNames)
	for _, name := range cssNames {
		layer, _ := layerOf(name)
		body, err := buildCSS(name, layer, raw[name], s)
		if err != nil {
			return nil, err
		}
		if err := s.add(name, body, contentTypes[".css"]); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Set) add(name string, body []byte, typ string) error {
	sum := sha256.Sum256(body)
	ext := path.Ext(name)
	hash := hex.EncodeToString(sum[:])[:10]
	hashed := strings.TrimSuffix(name, ext) + "." + hash + ext
	a := &Asset{Name: name, Hashed: hashed, Body: body, Type: typ, ETag: `"` + hash + `"`, GzipETag: `"` + hash + `-gz"`}
	if compressible[ext] {
		gz, err := gzipBody(body)
		if err != nil {
			return fmt.Errorf("static: compress %s: %w", name, err)
		}
		if len(gz) < len(body) {
			a.Gzip = gz
		}
	}
	s.byName[name] = a
	s.byHashed[hashed] = a
	return nil
}

var fontURL = regexp.MustCompile(`url\("/static/(fonts/[^"]+)"\)`)

func buildCSS(name, layer string, src []byte, s *Set) ([]byte, error) {
	var out strings.Builder
	if path.Base(name) == "tokens.css" {
		out.WriteString(layerOrder + "\n")
	}
	body := string(src)
	var rewriteErr error
	body = fontURL.ReplaceAllStringFunc(body, func(m string) string {
		font := fontURL.FindStringSubmatch(m)[1]
		a, ok := s.byName[font]
		if !ok {
			rewriteErr = fmt.Errorf("static: %s refers to missing %s", name, font)
			return m
		}
		return `url("/static/` + a.Hashed + `")`
	})
	if rewriteErr != nil {
		return nil, rewriteErr
	}
	fmt.Fprintf(&out, "@layer %s {\n%s\n}\n", layer, body)
	if path.Base(name) == "tokens.css" {
		light, ok := themeBody(string(src), "light")
		if !ok {
			return nil, fmt.Errorf("static: %s has no light theme block", name)
		}
		contrast, ok := themeBody(string(src), "contrast")
		if !ok {
			return nil, fmt.Errorf("static: %s has no contrast theme block", name)
		}
		// With no stored choice (no data-theme attribute) follow the system. Unlayered rules are
		// not needed: these sit in the tokens layer like the blocks they copy.
		fmt.Fprintf(&out, "@layer tokens {\n@media (prefers-color-scheme: light){:root:not([data-theme]){%s}}\n"+
			"@media (prefers-contrast: more){:root:not([data-theme]){%s}}\n}\n", light, contrast)
	}
	return []byte(out.String()), nil
}

// themeBody returns the declarations inside the `[data-theme="<name>"] { ... }` block.
func themeBody(css, name string) (string, bool) {
	start := strings.Index(css, `[data-theme="`+name+`"] {`)
	if start < 0 {
		return "", false
	}
	open := strings.Index(css[start:], "{") + start
	end := strings.Index(css[open:], "}")
	if end < 0 {
		return "", false
	}
	return css[open+1 : open+end], true
}

// URL returns the public path of a source asset name, such as "css/bundle.css". It panics on an
// unknown name so a typo fails at the first render, not as a 404 in the browser.
func (s *Set) URL(name string) string {
	a, ok := s.byName[name]
	if !ok {
		panic("static: unknown asset " + name)
	}
	return "/static/" + a.Hashed
}

// Has reports whether a source asset exists.
func (s *Set) Has(name string) bool {
	_, ok := s.byName[name]
	return ok
}

// Asset returns the asset for a source name.
func (s *Set) Asset(name string) (*Asset, bool) {
	a, ok := s.byName[name]
	return a, ok
}

// Names lists the source names, sorted.
func (s *Set) Names() []string {
	names := make([]string, 0, len(s.byName))
	for n := range s.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// acceptsGzip reports whether an Accept-Encoding header allows a gzip answer. Codings are comma
// separated, each "name[;q=value]" with a case-insensitive name. An explicit gzip (or x-gzip) entry
// decides; without one, "*" decides; otherwise the answer is no. A quality of 0 or one that does
// not parse refuses that entry.
func acceptsGzip(header string) bool {
	star, starSeen := false, false
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		if name != "gzip" && name != "x-gzip" && name != "*" {
			continue
		}
		ok := true
		for _, param := range fields[1:] {
			key, val, _ := strings.Cut(strings.TrimSpace(param), "=")
			if strings.ToLower(strings.TrimSpace(key)) != "q" {
				continue
			}
			q, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
			ok = err == nil && q > 0
		}
		if name == "*" {
			star, starSeen = ok, true
			continue
		}
		return ok
	}
	return starSeen && star
}

// matchesETag reports whether an If-None-Match header names etag (strong comparison) or is "*".
func matchesETag(header, etag string) bool {
	for _, tag := range strings.Split(header, ",") {
		if tag = strings.TrimSpace(tag); tag == "*" || tag == etag {
			return true
		}
	}
	return false
}

// Handler serves GET and HEAD /static/{path...} by hashed name only. It answers with the gzip body
// when the asset has one and the client accepts it. Range is ignored, so the answer is always 200
// with the whole body (or 304 when If-None-Match matches).
func (s *Set) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.byHashed[strings.TrimPrefix(r.URL.Path, "/static/")]
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		body, etag, gz := a.Body, a.ETag, false
		if a.Gzip != nil && acceptsGzip(r.Header.Get("Accept-Encoding")) {
			body, etag, gz = a.Gzip, a.GzipETag, true
		}
		h := w.Header()
		h.Set("Content-Type", a.Type)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		h.Set("ETag", etag)
		if a.Gzip != nil {
			h.Set("Vary", "Accept-Encoding")
		}
		if match := r.Header.Get("If-None-Match"); match != "" && matchesETag(match, etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		if gz {
			h.Set("Content-Encoding", "gzip")
		}
		h.Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodHead {
			return
		}
		_, _ = w.Write(body)
	})
}
