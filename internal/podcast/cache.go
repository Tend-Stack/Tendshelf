package podcast

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxEpisodeBytes is the biggest episode the server keeps: 1 GiB (decision 0038, point 4).
const maxEpisodeBytes = 1 << 30

var (
	errVideo = errors.New("podcast: video")
	nameRE   = regexp.MustCompile(`^[a-z]{2}_[0-9A-Za-z]{1,40}$`)
)

// safeName reports whether id is fit to be a file or folder name: generated ids only, never a path.
func safeName(id string) bool { return nameRE.MatchString(id) }

// episodeExt picks the file extension from the feed's type; anything unknown is "bin".
func episodeExt(audioType string) string {
	mt, _, err := mime.ParseMediaType(audioType)
	if err != nil {
		return "bin"
	}
	switch strings.ToLower(mt) {
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/mp4", "audio/x-m4a", "audio/m4a":
		return "m4a"
	case "audio/ogg":
		return "ogg"
	case "audio/opus":
		return "opus"
	case "audio/aac", "audio/aacp":
		return "aac"
	}
	return "bin"
}

// CacheFile resolves an episode's stored path to a file inside the cache folder, or "" when the
// episode is not kept or the stored path points anywhere else.
func (s *Service) CacheFile(rel string) string {
	if rel == "" {
		return ""
	}
	p := filepath.Join(s.DataDir, filepath.FromSlash(rel))
	r, err := filepath.Rel(s.CacheDir(), p)
	if err != nil || r == "." || !filepath.IsLocal(r) {
		return ""
	}
	return p
}

// PutCache downloads an episode into <cache>/<showID>/<episodeID>.<ext> through a .part file and one
// rename, records it, then trims the cache to limit bytes. An episode already kept is left alone.
func (s *Service) PutCache(ctx context.Context, episodeID string, limit int64) error {
	var showID, audioURL, audioType, cached string
	var audioBytes int64
	err := s.dbs.Reader().QueryRowContext(ctx, `SELECT show_id, audio_url, audio_type, COALESCE(cache_path,''), COALESCE(audio_bytes,0) FROM podcast_episodes WHERE id = ?`, episodeID).
		Scan(&showID, &audioURL, &audioType, &cached, &audioBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !safeName(episodeID) || !safeName(showID) {
		return ErrNotFound
	}
	if f := s.CacheFile(cached); f != "" {
		if _, err := os.Stat(f); err == nil {
			return nil
		}
	}
	if strings.HasPrefix(strings.ToLower(audioType), "video/") {
		return errVideo
	}
	// The copy never goes past the owner's limit (or the per-episode ceiling); the sizes the feed and the
	// publisher announce are checked before a byte is stored.
	max := min(int64(maxEpisodeBytes), limit)
	if audioBytes > max {
		return &netErr{"too_large"}
	}
	up, err := s.Net.OpenAudio(ctx, audioURL, "")
	if err != nil {
		return err
	}
	defer up.Body.Close()
	if up.Status != 200 || !AllowedAudioType(up.Header.Get("Content-Type")) {
		return &netErr{"bad_answer"}
	}
	if cl, err := strconv.ParseInt(up.Header.Get("Content-Length"), 10, 64); err == nil && cl > max {
		return &netErr{"too_large"}
	}
	dir := filepath.Join(s.CacheDir(), showID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	part := filepath.Join(dir, ".part-"+episodeID)
	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(up.Body, max+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > max {
		err = &netErr{"too_large"}
	}
	if err == nil && n == 0 {
		err = &netErr{"bad_answer"}
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	final := filepath.Join(dir, episodeID+"."+episodeExt(audioType))
	if err := os.Rename(part, final); err != nil {
		os.Remove(part)
		return err
	}
	rel, _ := filepath.Rel(s.DataDir, final)
	now := s.nowMs()
	if _, err := s.dbs.Writer().ExecContext(ctx, `UPDATE podcast_episodes SET cache_path = ?, cache_bytes = ?, cached_at = ? WHERE id = ?`,
		filepath.ToSlash(rel), n, now, episodeID); err != nil {
		os.Remove(final)
		return err
	}
	// Chapters inside the file, best effort: a failure here never fails the download.
	if s.ProbeChapters != nil {
		var ep Episode
		ep.ID, ep.CachePath = episodeID, filepath.ToSlash(rel)
		if err := s.dbs.Reader().QueryRowContext(ctx, `SELECT chapters_url, COALESCE(chapters_json,'') FROM podcast_episodes WHERE id = ?`, episodeID).
			Scan(&ep.ChaptersURL, &ep.ChaptersJSON); err == nil {
			s.ReadFileChapters(ctx, ep)
		}
	}
	_, err = s.Evict(ctx, limit)
	return err
}

// AllowedAudioType reports whether a publisher's Content-Type may be passed on or kept: audio/*,
// application/octet-stream, or none at all (the caller then uses the feed's type). Anything else (a page,
// an SVG, a script, XML, video) is never served from this origin.
func AllowedAudioType(ct string) bool {
	if strings.TrimSpace(ct) == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	mt = strings.ToLower(mt)
	return strings.HasPrefix(mt, "audio/") || mt == "application/octet-stream"
}

// netErr is a plain-words failure with a podnet-style code, for failures the cache itself finds.
type netErr struct{ code string }

func (e *netErr) Error() string { return e.code }
func (e *netErr) Text() string {
	if e.code == "too_large" {
		return "That episode is too large to keep on this server."
	}
	return "That episode couldn't be read."
}

// Evict deletes kept episodes until the cache is within limit bytes (0 keeps nothing). Order: episodes
// of shows nobody follows, then episodes every follower has played, then the oldest first. It returns
// how many it removed.
func (s *Service) Evict(ctx context.Context, limit int64) (int, error) {
	rows, err := s.dbs.Reader().QueryContext(ctx, `SELECT e.id, e.cache_path, COALESCE(e.cache_bytes,0),
		CASE WHEN NOT EXISTS (SELECT 1 FROM podcast_subscriptions b WHERE b.show_id = e.show_id) THEN 0
		     WHEN NOT EXISTS (SELECT 1 FROM podcast_subscriptions b WHERE b.show_id = e.show_id AND NOT EXISTS
		        (SELECT 1 FROM podcast_episode_state t WHERE t.user_id = b.user_id AND t.episode_id = e.id AND t.played = 1)) THEN 1
		     ELSE 2 END AS rank
		FROM podcast_episodes e WHERE e.cache_path IS NOT NULL ORDER BY rank, COALESCE(e.cached_at,0), e.id`)
	if err != nil {
		return 0, err
	}
	type item struct {
		id, rel string
		bytes   int64
	}
	var items []item
	var total int64
	for rows.Next() {
		var it item
		var rank int
		if err := rows.Scan(&it.id, &it.rel, &it.bytes, &rank); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
		total += it.bytes
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	removed := 0
	for _, it := range items {
		if total <= limit {
			break
		}
		if f := s.CacheFile(it.rel); f != "" {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return removed, err
			}
		}
		if _, err := s.dbs.Writer().ExecContext(ctx, `UPDATE podcast_episodes SET cache_path = NULL, cache_bytes = NULL, cached_at = NULL WHERE id = ?`, it.id); err != nil {
			return removed, err
		}
		total -= it.bytes
		removed++
	}
	return removed, nil
}

// ClearCache removes every kept episode (the owner's "Remove saved episodes").
func (s *Service) ClearCache(ctx context.Context) (int, error) { return s.Evict(ctx, 0) }

// CacheUsage is the bytes kept on the server.
func (s *Service) CacheUsage(ctx context.Context) int64 {
	var n int64
	_ = s.dbs.Reader().QueryRowContext(ctx, `SELECT COALESCE(SUM(cache_bytes),0) FROM podcast_episodes WHERE cache_path IS NOT NULL`).Scan(&n)
	return n
}

// sweepPartial removes .part files older than an hour: leftovers of a crashed download.
func (s *Service) sweepPartial() {
	shows, err := os.ReadDir(s.CacheDir())
	if err != nil {
		return
	}
	for _, d := range shows {
		if !d.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(s.CacheDir(), d.Name()))
		for _, f := range files {
			if !strings.HasPrefix(f.Name(), ".part-") {
				continue
			}
			if fi, err := f.Info(); err == nil && s.now().Sub(fi.ModTime()) > time.Hour {
				os.Remove(filepath.Join(s.CacheDir(), d.Name(), f.Name()))
			}
		}
	}
}
