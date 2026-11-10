package web

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// audioRange accepts one range, "bytes=N-" or "bytes=N-M". A suffix range ("bytes=-N"), several ranges
// and any other text are ignored, so the publisher is asked for the whole file.
var audioRange = regexp.MustCompile(`^bytes=(\d{1,15})-(\d{0,15})$`)

// cleanRange returns the client's Range header when it is a single acceptable range, else "", and the
// first byte it asks for.
func cleanRange(h string) (string, int64) {
	m := audioRange.FindStringSubmatch(h)
	if m == nil {
		return "", 0
	}
	first, _ := strconv.ParseInt(m[1], 10, 64)
	if m[2] != "" {
		if last, _ := strconv.ParseInt(m[2], 10, 64); last < first {
			return "", 0
		}
	}
	return h, first
}

// getEpisodeAudio serves one episode's audio, GET /podcasts/audio/{episodeID} and
// GET /api/v1/podcasts/audio/{id} (decision 0038, point 5). Order: podcasts on, share.Can, and only then
// the cache or the publisher. A kept file is served from disk with ranges; otherwise the publisher's
// answer is streamed through with one Range passed along. Nothing the publisher sent is shown to the person
// except the few safe headers copied below.
func (s *server) getEpisodeAudio(w http.ResponseWriter, r *http.Request) {
	download, ok := downloadParam(w, r)
	if !ok {
		return
	}
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	if strings.HasPrefix(strings.ToLower(ep.AudioType), "video/") {
		s.plainError(w, r, http.StatusUnsupportedMediaType, podErrVideo)
		return
	}
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cache-Control", "private, no-store")
	if download {
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": episodeFileName(ep)}))
	}
	if file := s.podcasts.CacheFile(ep.CachePath); file != "" {
		if f, err := os.Open(file); err == nil {
			defer f.Close()
			if fi, err := f.Stat(); err == nil && fi.Mode().IsRegular() {
				h.Set("Content-Type", audioMime(ep))
				http.ServeContent(w, r, "", time.Time{}, f)
				return
			}
		}
	}
	u := currentUser(r)
	release, ok := s.podcasts.AcquireStream(u.ID)
	if !ok {
		h.Set("Retry-After", "5")
		h.Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		io.WriteString(w, podAudioBusy)
		return
	}
	defer release()
	rng, first := cleanRange(r.Header.Get("Range"))
	up, err := s.podcasts.Net.OpenAudio(r.Context(), ep.AudioURL, rng)
	if err != nil {
		var pe *podnet.Error
		if errors.As(err, &pe) && pe.Code == "not_found" {
			s.plainError(w, r, http.StatusNotFound, podAudioMissing)
			return
		}
		s.plainError(w, r, http.StatusBadGateway, podAudioUnavailable)
		return
	}
	defer up.Body.Close()
	if ct := strings.ToLower(up.Header.Get("Content-Type")); strings.HasPrefix(ct, "text/html") || strings.HasPrefix(ct, "application/xhtml") {
		s.plainError(w, r, http.StatusBadGateway, podAudioUnavailable)
		return
	}
	// The publisher's type is passed on only when it is audio; anything else (an SVG, a script, XML) is
	// answered as the feed's audio type, so the publisher cannot serve active content from this origin.
	for _, k := range []string{"Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := up.Header.Get(k); v != "" {
			h.Set(k, v)
		}
	}
	if ct := up.Header.Get("Content-Type"); ct != "" && podcast.AllowedAudioType(ct) {
		h.Set("Content-Type", ct)
	} else {
		h.Set("Content-Type", audioMime(ep))
	}
	// A publisher that ignores Range sends the whole file. Seeking will not work until the server keeps its own
	// copy, so queue that once (never twice: the job is deduplicated by episode).
	if rng != "" && first > 0 && up.Status == http.StatusOK {
		if set := s.podcasts.Settings(r.Context()); set.CacheBytes > 0 && ep.AudioBytes <= set.CacheBytes {
			if _, err := s.podcasts.QueueCache(r.Context(), ep.ID); err != nil {
				s.podcasts.Log.Warn("podcasts: could not queue a cache job", "err", err)
			}
		}
	}
	w.WriteHeader(up.Status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, up.Body)
}

// audioMime is the type to answer when the publisher gave none: the feed's, else a generic audio type.
func audioMime(ep podcast.Episode) string {
	if mt, _, err := mime.ParseMediaType(ep.AudioType); err == nil && strings.HasPrefix(mt, "audio/") {
		return mt
	}
	return "application/octet-stream"
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9 ._-]+`)

// episodeFileName is a safe download name from the title.
func episodeFileName(ep podcast.Episode) string {
	name := strings.TrimSpace(unsafeFileChars.ReplaceAllString(ep.Title, ""))
	if name == "" {
		name = "episode"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	ext := ".mp3"
	switch audioMime(ep) {
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		ext = ".m4a"
	case "audio/ogg":
		ext = ".ogg"
	case "audio/opus":
		ext = ".opus"
	case "audio/aac", "audio/aacp":
		ext = ".aac"
	}
	return name + ext
}

// getPodcastArt serves a show's picture from the art cache: the smallest thumbnail that covers ?w=, else
// the original. Only people who follow the show (or keep something of it) may ask.
func (s *server) getPodcastArt(w http.ResponseWriter, r *http.Request) {
	if !s.podcastsGate(w, r) {
		return
	}
	width := 0
	if v := r.URL.Query().Get("w"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxCoverWidth {
			s.plainError(w, r, http.StatusBadRequest, "Bad width.")
			return
		}
		width = n
	}
	id := r.PathValue("showID")
	if !s.podcasts.Knows(r.Context(), currentUser(r).ID, id) {
		s.notFound(w, r)
		return
	}
	sh, err := s.podcasts.ShowByID(r.Context(), id)
	if err != nil || !sha256Hex.MatchString(sh.ArtSHA) {
		s.plainError(w, r, http.StatusNotFound, "Not found.")
		return
	}
	var cands []coverCandidate
	if width > 0 {
		thumbs := mediajobs.ThumbsDir(s.dataDir)
		for _, box := range mediajobs.ThumbSizes {
			if p := mediajobs.ThumbPath(s.dataDir, sh.ArtSHA, box); box >= width && within(thumbs, p) {
				cands = append(cands, coverCandidate{p, "image/jpeg", strconv.Itoa(box)})
			}
		}
	}
	orig := filepath.Join(s.dataDir, filepath.FromSlash(sh.ArtPath))
	if within(s.podcasts.ArtDir(), orig) {
		ct := map[string]string{".png": "image/png", ".webp": "image/webp"}[strings.ToLower(filepath.Ext(orig))]
		if ct == "" {
			ct = "image/jpeg"
		}
		cands = append(cands, coverCandidate{orig, ct, "orig"})
	}
	for _, c := range cands {
		f, err := os.Open(c.path)
		if err != nil {
			continue
		}
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			f.Close()
			continue
		}
		h := w.Header()
		h.Set("Content-Type", c.mime)
		h.Set("ETag", `"`+sh.ArtSHA+"-"+c.tag+`"`)
		h.Set("X-Content-Type-Options", "nosniff")
		if r.URL.Query().Get("v") == sh.ArtSHA[:8] {
			h.Set("Cache-Control", "private, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "private, no-cache")
		}
		http.ServeContent(w, r, "", time.Time{}, f)
		f.Close()
		return
	}
	s.plainError(w, r, http.StatusNotFound, "Not found.")
}

// getPodcastChapters serves an episode's Podcasting 2.0 chapters as JSON. The first request fetches the
// file the feed named (through the guarded client) and keeps it with the episode.
func (s *server) getPodcastChapters(w http.ResponseWriter, r *http.Request) {
	ep, ok := s.loadEpisode(w, r)
	if !ok {
		return
	}
	body := []byte(ep.ChaptersJSON)
	if len(body) > 0 {
		body = podcast.ChaptersJSON(podcast.ParseChapters(body)) // an older stored copy is cut down the same way
	}
	if len(body) == 0 {
		if ep.ChaptersURL == "" {
			s.plainError(w, r, http.StatusNotFound, "Not found.")
			return
		}
		if s.podcasts.ChapterFetchBlocked(ep.ID) {
			s.plainError(w, r, http.StatusBadGateway, podAudioUnavailable)
			return
		}
		release, ok := s.podcasts.AcquireStream(currentUser(r).ID)
		if !ok {
			w.Header().Set("Retry-After", "5")
			s.plainError(w, r, http.StatusServiceUnavailable, podAudioBusy)
			return
		}
		b, err := s.podcasts.Net.Chapters(r.Context(), ep.ChaptersURL)
		release()
		if err != nil {
			s.podcasts.NoteChapterFailure(ep.ID)
			s.plainError(w, r, http.StatusBadGateway, podAudioUnavailable)
			return
		}
		// Only start and title are kept; the rest of the publisher's file goes no further.
		body = podcast.ChaptersJSON(podcast.ParseChapters(b))
		if err := s.podcasts.SetChapters(r.Context(), ep.ID, body); err != nil {
			s.podcasts.Log.Warn("podcasts: could not keep chapters", "err", err)
		}
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "private, no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(body)
}

// episodeChapters is an episode's stored chapters, when the first ask has already fetched them.
func episodeChapters(ep podcast.Episode) []podcast.Chapter {
	if ep.ChaptersJSON == "" {
		return nil
	}
	return podcast.ParseChapters([]byte(ep.ChaptersJSON))
}
