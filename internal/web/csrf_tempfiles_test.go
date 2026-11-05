package web

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// multipartLeftovers counts the multipart-* files the server left in the temp folder.
func multipartLeftovers(dir string) int {
	m, _ := filepath.Glob(filepath.Join(dir, "multipart-*"))
	return len(m)
}

// A refused or accepted multipart request must not leave its spooled temp files behind: net/http
// only cleans the original request, and the check parses a copy.
func TestCSRFMultipartTempFilesRemoved(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	c0 := NewCSRF(nil, nil)
	c0.Deny = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })
	h := c0.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			_, _ = w.Write([]byte(`<body hx-headers="{&#34;X-CSRF-Token&#34;:&#34;` + c0.TokenFrom(r.Context()) + `&#34;}">`))
			return
		}
		http.NotFound(w, r)
	}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A signed-in request gets the large Text-tab limit, which spools to disk past 1 MiB.
		h.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), auth.Session{UserID: "u_1"}, users.User{ID: "u_1"})))
	}))
	defer srv.Close()
	c, tok := session(t, h)
	for _, tc := range []struct{ name, token string }{{"accepted", tok}, {"refused", "nope"}} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("_csrf", tc.token)
		fw, _ := mw.CreateFormFile("file", "a.bin")
		_, _ = fw.Write(make([]byte, 5<<20))
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/books/bk_1/text", &buf)
		req.AddCookie(c)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		deadline := time.Now().Add(2 * time.Second)
		for multipartLeftovers(dir) > 0 && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if n := multipartLeftovers(dir); n != 0 {
			t.Errorf("%s upload left %d multipart temp files", tc.name, n)
		}
	}
}
