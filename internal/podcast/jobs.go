package podcast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
)

// Register adds the five job kinds to the runner. Every handler first checks that podcasts are on:
// with them off a queued job ends at once and nothing is fetched.
func (s *Service) Register() error {
	for _, k := range []jobs.Kind{
		{Name: KindRefresh, Concurrency: 2, MaxAttempts: 1, Handler: s.handleRefresh},
		{Name: KindArt, Concurrency: 2, MaxAttempts: 2, Handler: s.handleArt},
		{Name: KindCache, Concurrency: 1, MaxAttempts: 2, Handler: s.handleCache},
		{Name: KindImport, Concurrency: 1, MaxAttempts: 1, Handler: s.handleImport},
		{Name: KindPrune, Concurrency: 1, MaxAttempts: 1, Handler: s.handlePrune},
	} {
		if err := s.Runner.Register(k); err != nil {
			return err
		}
	}
	return nil
}

// errOff ends a job for good when podcasts are turned off.
func errOff() error {
	return &jobs.Error{Code: "podcasts_off", Text: TextOff, Permanent: true}
}

// jobFail turns a podnet failure into a permanent job failure in plain words.
func jobFail(err error) error {
	return &jobs.Error{Code: netCode(err), Text: errText(err), Permanent: true, Err: err}
}

// handleRefresh fetches one show's feed with a conditional GET and stores what it finds.
func (s *Service) handleRefresh(ctx context.Context, t *jobs.Task) (any, error) {
	var p showPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.ShowID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: TextShowGone, Permanent: true}
	}
	set := s.Settings(ctx)
	if !set.Enabled {
		return nil, errOff()
	}
	show, err := s.ShowByID(ctx, p.ShowID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil // unfollowed and pruned while it waited
	}
	if err != nil {
		return nil, err
	}
	next := int64(0)
	if iv := set.Interval(); iv > 0 {
		next = s.nowMs() + iv.Milliseconds()
	}
	res, err := s.Net.Feed(ctx, show.FeedURL, show.ETag, show.LastModified)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil && ctx.Err() != nil {
			return nil, cause
		}
		if mErr := s.MarkFailed(ctx, show.ID, netCode(err)); mErr != nil {
			s.Log.Warn("podcasts: could not record a failed refresh", "err", mErr)
		}
		return nil, jobFail(err)
	}
	meta := FetchMeta{ETag: res.ETag, LastModified: res.LastModified, NextRefreshAt: next}
	if res.Moved {
		if to, err := NormalizeFeedURL(res.FinalURL); err == nil {
			meta.MovedTo = to
		}
	}
	if res.NotModified {
		if meta.ETag == "" {
			meta.ETag = show.ETag
		}
		if meta.LastModified == "" {
			meta.LastModified = show.LastModified
		}
		return map[string]any{"added": 0, "not_modified": true}, s.MarkNotModified(ctx, show.ID, meta)
	}
	feed, err := rss.Parse(res.Body, res.FinalURL)
	if err != nil {
		_ = s.MarkFailed(ctx, show.ID, "not_a_feed")
		return nil, &jobs.Error{Code: "not_a_feed", Text: TextNotFeed, Permanent: true}
	}
	added, err := s.ApplyFeed(ctx, show.ID, feed, meta)
	if err != nil {
		return nil, err
	}
	if feed.ImageURL != "" && (feed.ImageURL != show.ArtURL || show.ArtPath == "") {
		if _, err := s.enqueueOnce(ctx, KindArt, "show_id", show.ID, showPayload{show.ID}, ""); err != nil {
			s.Log.Warn("podcasts: could not queue artwork", "err", err)
		}
	}
	return map[string]any{"added": added}, nil
}

type artPayload struct {
	ShowID    string `json:"show_id,omitempty"`
	StationID string `json:"station_id,omitempty"`
}

// handleArt downloads a show's (or station's) picture into <data>/cache/podcast-art/<sha>.<ext> and
// makes the usual thumbnails beside the book covers' own.
func (s *Service) handleArt(ctx context.Context, t *jobs.Task) (any, error) {
	var p artPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || (p.ShowID == "") == (p.StationID == "") {
		return nil, &jobs.Error{Code: "bad_payload", Text: TextShowGone, Permanent: true}
	}
	set := s.Settings(ctx)
	if !set.Enabled && !(p.StationID != "" && set.RadioEnabled) {
		return nil, errOff()
	}
	table, id := "podcast_shows", p.ShowID
	if p.StationID != "" {
		table, id = "radio_stations", p.StationID
	}
	var artURL string
	if err := s.dbs.Reader().QueryRowContext(ctx, `SELECT art_url FROM `+table+` WHERE id = ?`, id).Scan(&artURL); err != nil {
		return nil, nil // gone while it waited
	}
	if artURL == "" {
		return nil, nil
	}
	body, mime, err := s.Net.Picture(ctx, artURL)
	if err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, &jobs.Error{Code: netCode(err), Text: errText(err), Permanent: netCode(err) != "offline" && netCode(err) != "slow", Err: err}
	}
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	name := sha + map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[mime]
	if err := os.MkdirAll(s.ArtDir(), 0o700); err != nil {
		return nil, err
	}
	dst := filepath.Join(s.ArtDir(), name)
	if _, err := os.Stat(dst); err != nil {
		tmp := filepath.Join(s.ArtDir(), ".part-"+sha[:16])
		if err := os.WriteFile(tmp, body, 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(tmp, dst); err != nil {
			os.Remove(tmp)
			return nil, err
		}
	}
	if s.Thumb != nil {
		for _, box := range mediajobs.ThumbSizes {
			th := mediajobs.ThumbPath(s.DataDir, sha, box)
			if _, err := os.Stat(th); err == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(th), 0o700); err != nil {
				return nil, err
			}
			tmp := filepath.Join(filepath.Dir(th), ".tmp-pod-"+sha[:16]+".jpg")
			if err := s.Thumb(ctx, dst, tmp, box); err != nil {
				os.Remove(tmp)
				s.Log.Warn("podcasts: thumbnail failed", "err", err) // the original still serves
				break
			}
			if err := os.Rename(tmp, th); err != nil {
				os.Remove(tmp)
				break
			}
		}
	}
	rel := filepath.ToSlash(filepath.Join("cache", "podcast-art", name))
	if p.ShowID != "" {
		return nil, s.SetArt(ctx, id, rel, sha)
	}
	_, err = s.dbs.Writer().ExecContext(ctx, `UPDATE radio_stations SET art_path = ?, updated_at = ? WHERE id = ?`, rel, s.nowMs(), id)
	return nil, err
}

func errText(err error) string {
	var cerr interface{ Text() string }
	if errors.As(err, &cerr) {
		return cerr.Text()
	}
	return "That didn't work."
}

// handleCache keeps one episode's file on the server.
func (s *Service) handleCache(ctx context.Context, t *jobs.Task) (any, error) {
	var p struct {
		EpisodeID string `json:"episode_id"`
	}
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.EpisodeID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: TextEpisodeGone, Permanent: true}
	}
	set := s.Settings(ctx)
	if !set.Enabled {
		return nil, errOff()
	}
	if set.CacheBytes == 0 {
		return nil, &jobs.Error{Code: "cache_off", Text: TextCacheOff, Permanent: true}
	}
	err := s.PutCache(ctx, p.EpisodeID, set.CacheBytes)
	switch {
	case err == nil, errors.Is(err, ErrNotFound):
		return nil, nil
	case ctx.Err() != nil:
		return nil, context.Cause(ctx)
	case errors.Is(err, errVideo):
		return nil, &jobs.Error{Code: "video", Text: TextVideo, Permanent: true}
	}
	var pe interface{ Text() string }
	if errors.As(err, &pe) {
		return nil, &jobs.Error{Code: netCode(err), Text: pe.Text(), Permanent: netCode(err) != "offline" && netCode(err) != "slow", Err: err}
	}
	return nil, err
}

type importPayload struct {
	UserID string   `json:"user_id"`
	URLs   []string `json:"urls"`
}

// handleImport follows every address of an OPML file for one person and queues a refresh for each
// show that is new to the server.
func (s *Service) handleImport(ctx context.Context, t *jobs.Task) (any, error) {
	var p importPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.UserID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: TextBadAddress, Permanent: true}
	}
	if !s.Enabled(ctx) {
		return nil, errOff()
	}
	var followed, skipped, tooMany int
	for _, raw := range p.URLs {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		id, created, err := s.Follow(ctx, p.UserID, raw, "")
		switch {
		case errors.Is(err, ErrTooMany):
			tooMany++
			continue
		case err != nil:
			skipped++
			continue
		}
		if created {
			followed++
		}
		if sh, err := s.ShowByID(ctx, id); err == nil && sh.FetchedAt == 0 {
			_, _ = s.QueueRefresh(ctx, id, p.UserID)
		}
		t.Progress(map[string]int{"done": followed + skipped + tooMany, "of": len(p.URLs)})
	}
	return map[string]int{"followed": followed, "skipped": skipped, "too_many": tooMany}, nil
}

// handlePrune removes shows nobody keeps, their files, leftover half-written files, and trims the cache
// to the owner's limit.
func (s *Service) handlePrune(ctx context.Context, t *jobs.Task) (any, error) {
	set := s.Settings(ctx)
	if !set.Enabled {
		return nil, errOff()
	}
	orphans, err := s.Orphans(ctx)
	if err != nil {
		return nil, err
	}
	for _, id := range orphans {
		if safeName(id) {
			_ = os.RemoveAll(filepath.Join(s.CacheDir(), id))
		}
		if err := s.DeleteShow(ctx, id); err != nil {
			return nil, err
		}
	}
	s.sweepArt(ctx)
	s.sweepPartial()
	evicted, err := s.Evict(ctx, set.CacheBytes)
	if err != nil {
		return nil, err
	}
	return map[string]int{"shows_removed": len(orphans), "episodes_evicted": evicted}, nil
}

// sweepArt removes stored pictures no show or station points at.
func (s *Service) sweepArt(ctx context.Context) {
	entries, err := os.ReadDir(s.ArtDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		rel := "cache/podcast-art/" + name
		var n int
		if s.dbs.Reader().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM podcast_shows WHERE art_path = ?1) + (SELECT count(*) FROM radio_stations WHERE art_path = ?1)`, rel).Scan(&n) == nil && n == 0 {
			_ = os.Remove(filepath.Join(s.ArtDir(), name))
		}
	}
}
