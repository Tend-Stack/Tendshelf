package static

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"testing"
)

func TestStaticGzipNegotiation(t *testing.T) {
	for _, c := range []struct {
		header string
		want   bool
	}{
		{"gzip", true},
		{"gzip, deflate, br", true},
		{"GZIP", true},
		{"x-gzip", true},
		{"*", true},
		{"gzip;q=0", false},
		{"*;q=0", false},
		{"gzip;q=0, *", false},
		{"br", false},
		{"", false},
		{"gzip;q=abc", false},
		{"deflate, gzip;q=0.5", true},
		{"br, *;q=0.1", true},
	} {
		if got := acceptsGzip(c.header); got != c.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}

func TestStaticGzipBodies(t *testing.T) {
	s := mustLoad(t)
	for _, n := range s.Names() {
		a, _ := s.Asset(n)
		switch path.Ext(n) {
		case ".css", ".js", ".svg", ".txt":
			if a.Gzip == nil {
				t.Errorf("%s: no gzip body", n)
				continue
			}
			if len(a.Gzip) >= len(a.Body) {
				t.Errorf("%s: gzip %d bytes is not smaller than %d", n, len(a.Gzip), len(a.Body))
			}
			zr, err := gzip.NewReader(bytes.NewReader(a.Gzip))
			if err != nil {
				t.Fatalf("%s: %v", n, err)
			}
			got, err := io.ReadAll(zr)
			if err != nil || !bytes.Equal(got, a.Body) {
				t.Errorf("%s: gzip does not unzip to the body (%v)", n, err)
			}
		case ".woff2":
			if a.Gzip != nil {
				t.Errorf("%s: fonts must not be compressed", n)
			}
		}
	}
	if a, ok := s.Asset("vendor/htmx-4.0.0.min.js"); !ok || a.Gzip == nil {
		t.Error("vendored htmx has no gzip body")
	}
}

func TestStaticGzipDeterministic(t *testing.T) {
	first, second := mustLoad(t), mustLoad(t)
	for _, n := range first.Names() {
		a, _ := first.Asset(n)
		b, _ := second.Asset(n)
		if !bytes.Equal(a.Gzip, b.Gzip) {
			t.Errorf("%s: two loads give different gzip bytes", n)
		}
	}
}

// staticGzGet runs one request through the handler.
func staticGzGet(s *Set, method, url string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestStaticGzipHeaders(t *testing.T) {
	s := mustLoad(t)
	a, _ := s.Asset("js/shell.js")
	hash := strings.Split(path.Base(a.Hashed), ".")[1]

	rec := staticGzGet(s, http.MethodGet, s.URL("js/shell.js"), map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("gzip answer headers: %v", rec.Header())
	}
	if got := rec.Header().Get("ETag"); got != `"`+hash+`-gz"` {
		t.Errorf("gzip ETag %q", got)
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(a.Gzip)) || rec.Body.Len() != len(a.Gzip) {
		t.Errorf("gzip length header %q, body %d, want %d", rec.Header().Get("Content-Length"), rec.Body.Len(), len(a.Gzip))
	}

	rec = staticGzGet(s, http.MethodGet, s.URL("js/shell.js"), nil)
	if rec.Header().Get("Content-Encoding") != "" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("identity answer headers: %v", rec.Header())
	}
	if got := rec.Header().Get("ETag"); got != `"`+hash+`"` {
		t.Errorf("identity ETag %q", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), a.Body) || rec.Header().Get("Content-Length") != strconv.Itoa(len(a.Body)) {
		t.Error("identity body is not the plain asset")
	}

	fonts := 0
	for _, n := range s.Names() {
		if path.Ext(n) != ".woff2" {
			continue
		}
		fonts++
		rec = staticGzGet(s, http.MethodGet, s.URL(n), map[string]string{"Accept-Encoding": "gzip"})
		if rec.Header().Get("Vary") != "" || rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s: font answer has Vary or Content-Encoding: %v", n, rec.Header())
		}
	}
	if fonts == 0 {
		t.Error("no font assets found")
	}
}

func TestStaticConditional(t *testing.T) {
	s := mustLoad(t)
	a, _ := s.Asset("css/bundle.css")
	url := s.URL("css/bundle.css")
	for _, c := range []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"gz tag with gzip", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": a.GzipETag}, http.StatusNotModified},
		{"identity tag while asking gzip", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": a.ETag}, http.StatusOK},
		{"star", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": "*"}, http.StatusNotModified},
		{"list with the tag", map[string]string{"Accept-Encoding": "gzip", "If-None-Match": `"other", ` + a.GzipETag}, http.StatusNotModified},
		{"identity tag, identity", map[string]string{"If-None-Match": a.ETag}, http.StatusNotModified},
	} {
		rec := staticGzGet(s, http.MethodGet, url, c.header)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
		if c.want == http.StatusNotModified {
			if rec.Body.Len() != 0 {
				t.Errorf("%s: 304 carries a body", c.name)
			}
			if rec.Header().Get("ETag") == "" || rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Cache-Control") == "" {
				t.Errorf("%s: 304 headers %v", c.name, rec.Header())
			}
		}
	}
}

func TestStaticHeadAndRange(t *testing.T) {
	s := mustLoad(t)
	a, _ := s.Asset("css/bundle.css")
	url := s.URL("css/bundle.css")

	rec := staticGzGet(s, http.MethodHead, url, map[string]string{"Accept-Encoding": "gzip"})
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, body %d bytes", rec.Code, rec.Body.Len())
	}
	if rec.Header().Get("Content-Length") != strconv.Itoa(len(a.Gzip)) || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("HEAD headers %v", rec.Header())
	}

	rec = staticGzGet(s, http.MethodGet, url, map[string]string{"Range": "bytes=0-9"})
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), a.Body) {
		t.Errorf("Range: status %d, body %d bytes, want 200 and the full %d", rec.Code, rec.Body.Len(), len(a.Body))
	}
	if rec.Header().Get("Content-Range") != "" || rec.Header().Get("Accept-Ranges") != "" {
		t.Errorf("Range answer carries range headers: %v", rec.Header())
	}
}
