//go:build !dev

package web

import "net/http"

// registerDev is empty in release builds: there are no /dev or /hx/dev routes. The kitchen sink
// lives in devroutes_dev.go behind the dev build tag.
func registerDev(*http.ServeMux, *server) {}
