package web

import (
	"net/http"
	"regexp"
	"time"
)

const (
	// bodyWindow is how long a request that carries a body may take to send it. A form is a few
	// kilobytes, so a client that needs longer is stalling, not uploading.
	bodyWindow = 60 * time.Second
	// bigBodyWindow is the window for the two routes that take a file in one request: the cover
	// picture and the Text-tab ebook (up to 200 MiB) on a slow link.
	bigBodyWindow = 15 * time.Minute
)

// bigBodyPath matches the routes whose single request carries a file.
var bigBodyPath = regexp.MustCompile(`^/books/[^/]+/(cover/upload|text)$`)

// WithBodyDeadline gives every request that carries a body a read deadline, so a slow POST cannot
// hold a connection open forever. It does nothing for GET and HEAD, which leaves audio streaming
// (whose response may run for hours) alone. The upload chunk handler sets its own, longer deadline
// after this one, which wins.
func WithBodyDeadline(next http.Handler) http.Handler {
	return withBodyDeadline(next, bodyWindow, bigBodyWindow)
}

func withBodyDeadline(next http.Handler, window, big time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) {
			d := window
			if r.Method == http.MethodPost && bigBodyPath.MatchString(r.URL.Path) {
				d = big
			}
			_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(d))
		}
		next.ServeHTTP(w, r)
	})
}
