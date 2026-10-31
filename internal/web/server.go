// Package web serves Tendshelf's server-rendered pages, HTMX fragments and
// static assets.
//
// P0 skeleton: a placeholder page and /healthz using only the standard
// library. templ components, the embedded static tree (with vendored htmx)
// and the CSRF/session middleware arrive with the first dependency package;
// see docs/architecture.md.
package web

import (
	"encoding/json"
	"html/template"
	"net/http"
)

// Options configures the handler.
type Options struct {
	Version string
}

// NewHandler returns the root HTTP handler.
func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = placeholderPage.Execute(w, opts)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": opts.Version,
		})
	})
	return securityHeaders(mux)
}

// contentSecurityPolicy matches docs/architecture.md ("Auth and sharing").
const contentSecurityPolicy = "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; " +
	"script-src 'self'; style-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

var placeholderPage = template.Must(template.New("placeholder").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Tendshelf</title>
</head>
<body>
<main>
<h1>Tendshelf</h1>
<p>Your audiobook server is running. The library and player are not built yet.</p>
<p>Version {{.Version}}</p>
</main>
</body>
</html>
`))
