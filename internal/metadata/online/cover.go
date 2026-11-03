// Ports the Extractor extractor_online.py (cover_url_ok, image_kind, fetch_cover); see NOTICES.md.

package online

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// imageKind names a picture by its first bytes ("jpeg", "png", "webp"), never by name or content type.
func imageKind(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "webp"
	}
	return ""
}

func coverURLOK(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Port() == "" && isCoverHost(strings.ToLower(u.Hostname()))
}

func (c *Client) coverPath(rawURL string) string {
	if c.coverDir == "" {
		return ""
	}
	return filepath.Join(c.coverDir, cacheKey(rawURL)+".img")
}

// CachedCover reads a cover from the disk cache only. It never fetches.
func (c *Client) CachedCover(rawURL string) ([]byte, string, bool) {
	p := c.coverPath(rawURL)
	if p == "" || !coverURLOK(rawURL) {
		return nil, "", false
	}
	st, err := os.Stat(p)
	if err != nil || c.now().Sub(st.ModTime()) >= cacheTTL {
		return nil, "", false
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) > maxCoverBytes {
		return nil, "", false
	}
	kind := imageKind(b)
	return b, kind, kind != ""
}

// FetchCover returns the picture bytes and their kind ("jpeg", "png" or "webp"). The browser never
// contacts the cover site; the answer is cached on disk for seven days.
func (c *Client) FetchCover(ctx context.Context, rawURL string) ([]byte, string, error) {
	if !coverURLOK(rawURL) {
		return nil, "", &Error{Code: "refused"}
	}
	if b, kind, ok := c.CachedCover(rawURL); ok {
		return b, kind, nil
	}
	u, _ := url.Parse(rawURL)
	site := siteName(strings.ToLower(u.Hostname()))
	status, body, err := c.fetch(ctx, rawURL, "image/jpeg,image/png,image/webp", maxCoverBytes)
	if err != nil {
		return nil, "", err
	}
	if status == http.StatusTooManyRequests {
		return nil, "", &Error{Code: "busy", Site: site}
	}
	if status < 200 || status > 299 {
		return nil, "", &Error{Code: "server", Site: site}
	}
	kind := imageKind(body)
	if kind == "" {
		return nil, "", &Error{Code: "bad_answer", Site: site}
	}
	if p := c.coverPath(rawURL); p != "" {
		if err := writeAtomic(p, body); err == nil {
			_ = os.Chtimes(p, c.now(), c.now())
		}
	}
	return body, kind, nil
}

// writeAtomic writes through a temporary file and a rename, so a reader never sees half a picture.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cover-*.tmp")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(f.Name())
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}
