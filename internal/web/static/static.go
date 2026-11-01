// Package static embeds the web assets (CSS, JS, vendored htmx, fonts, images) and serves them
// under content-hashed names, so every URL can be cached as immutable.
//
// Hashing happens once at startup in Load. CSS source files stay pristine copies of the design
// handoff: the cascade-layer wrapping, the system-theme blocks and the font URL rewrite are
// applied at load time.
package static

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
)

//go:embed css js vendor fonts img
var files embed.FS

// Asset is one served file.
type Asset struct {
	Name   string // source path under the static tree, e.g. "css/tokens.css"
	Hashed string // served path, e.g. "css/tokens.3fa1c09b2e.css"
	Body   []byte
	Type   string // Content-Type
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
	case strings.HasPrefix(base, "pages-") && strings.HasSuffix(base, ".css"), base == "player.css":
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

// Load reads the embedded tree and builds the hashed set.
func Load() (*Set, error) {
	return load(files)
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
		s.add(name, body, typ)
	}

	// Fonts are hashed first, then the CSS that refers to them.
	sort.Strings(cssNames)
	for _, name := range cssNames {
		layer, _ := layerOf(name)
		body, err := buildCSS(name, layer, raw[name], s)
		if err != nil {
			return nil, err
		}
		s.add(name, body, contentTypes[".css"])
	}
	return s, nil
}

func (s *Set) add(name string, body []byte, typ string) {
	sum := sha256.Sum256(body)
	ext := path.Ext(name)
	hashed := strings.TrimSuffix(name, ext) + "." + hex.EncodeToString(sum[:])[:10] + ext
	a := &Asset{Name: name, Hashed: hashed, Body: body, Type: typ}
	s.byName[name] = a
	s.byHashed[hashed] = a
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

// Handler serves GET /static/{path...} by hashed name only.
func (s *Set) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.byHashed[strings.TrimPrefix(r.URL.Path, "/static/")]
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", a.Type)
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		_, _ = w.Write(a.Body)
	})
}
