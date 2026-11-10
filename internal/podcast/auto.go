package podcast

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

// Server-side auto-download (decision 0038, plan P5). Off by default twice over: the owner allows it
// (podcasts.server_auto) and a follower turns it on for one show (podcast_subscriptions.server_auto).
//
// For each show with at least one such follower, the episodes to keep are the union, over those
// followers, of the newest keep_latest episodes that follower has not played. The scheduler queues a
// podcast.cache job for each of them that is not kept yet, and removes kept episodes of that show that
// nobody wants any more: every follower has played them, or they are older than the newest keep_latest
// episodes of the show. Nothing here may take the cache past the owner's limit: an episode is queued only
// while the kept bytes, plus what this pass already queued, plus its announced size, stay within
// podcasts.cache_bytes (the job itself still trims to the limit, and never stores more than the limit).

// Bounds of one pass and of retrying a failed episode.
const (
	autoQueueMax   = 20            // episodes queued in one scheduler pass
	autoRetryAfter = 6 * time.Hour // an episode whose job was queued is not queued again for this long
	autoTriedMax   = 4096          // the memo of queued episodes is dropped when it grows past this
	autoShowsMax   = 500           // shows looked at in one pass
)

type autoShow struct {
	id      string
	keepMax int
	users   []autoFollower
}

type autoFollower struct {
	userID string
	keep   int
}

// autoShows lists the shows that have a follower with server_auto on, with those followers.
func (s *Service) autoShows(ctx context.Context) ([]autoShow, error) {
	rows, err := s.dbs.Reader().QueryContext(ctx, `SELECT show_id, user_id, keep_latest FROM podcast_subscriptions
  WHERE server_auto = 1 ORDER BY show_id, user_id LIMIT ?`, autoShowsMax*50)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []autoShow
	for rows.Next() {
		var show string
		var f autoFollower
		if err := rows.Scan(&show, &f.userID, &f.keep); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].id != show {
			if len(out) >= autoShowsMax {
				break
			}
			out = append(out, autoShow{id: show})
		}
		a := &out[len(out)-1]
		a.users = append(a.users, f)
		a.keepMax = max(a.keepMax, f.keep)
	}
	return out, rows.Err()
}

// autoWanted is the set of episode ids to keep for one show: the union of each follower's newest unplayed
// episodes. Gone and video episodes are never wanted.
func (s *Service) autoWanted(ctx context.Context, a autoShow) (map[string]bool, error) {
	want := map[string]bool{}
	for _, f := range a.users {
		rows, err := s.dbs.Reader().QueryContext(ctx, `SELECT e.id FROM podcast_episodes e
  WHERE e.show_id = ? AND e.gone_at IS NULL AND lower(e.audio_type) NOT LIKE 'video/%'
    AND NOT EXISTS (SELECT 1 FROM podcast_episode_state t WHERE t.user_id = ? AND t.episode_id = e.id AND t.played = 1)
  ORDER BY e.published_at DESC, e.id LIMIT ?`, a.id, f.userID, f.keep)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			want[id] = true
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return want, nil
}

// RemoveCached deletes one kept episode's file and forgets it. An episode that is not kept is left alone.
func (s *Service) RemoveCached(ctx context.Context, episodeID string) error {
	var rel sql.NullString
	err := s.dbs.Reader().QueryRowContext(ctx, `SELECT cache_path FROM podcast_episodes WHERE id = ?`, episodeID).Scan(&rel)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !rel.Valid) {
		return nil
	}
	if err != nil {
		return err
	}
	if f := s.CacheFile(rel.String); f != "" {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_, err = s.dbs.Writer().ExecContext(ctx, `UPDATE podcast_episodes SET cache_path = NULL, cache_bytes = NULL, cached_at = NULL WHERE id = ?`, episodeID)
	return err
}

// AutoDownload is one auto-download pass. It does nothing while podcasts are off, the owner has not allowed
// server auto-download, or the cache limit is 0. It removes kept episodes nobody wants, then queues the
// missing wanted ones, newest first within each show, while they fit under the limit. It returns how many
// jobs it queued and how many files it removed.
func (s *Service) AutoDownload(ctx context.Context) (queued, removed int, err error) {
	set := s.Settings(ctx)
	if !set.Enabled || !set.ServerAuto || set.CacheBytes <= 0 {
		return 0, 0, nil
	}
	shows, err := s.autoShows(ctx)
	if err != nil {
		return 0, 0, err
	}
	type pending struct {
		id    string
		bytes int64
	}
	var todo []pending
	for _, a := range shows {
		want, err := s.autoWanted(ctx, a)
		if err != nil {
			return queued, removed, err
		}
		// Kept episodes of this show that no one wants: played by every follower, or older than the newest keepMax.
		rows, err := s.dbs.Reader().QueryContext(ctx, `SELECT e.id,
  NOT EXISTS (SELECT 1 FROM podcast_subscriptions b WHERE b.show_id = e.show_id AND NOT EXISTS
     (SELECT 1 FROM podcast_episode_state t WHERE t.user_id = b.user_id AND t.episode_id = e.id AND t.played = 1)),
  e.id IN (SELECT n.id FROM podcast_episodes n WHERE n.show_id = e.show_id ORDER BY n.published_at DESC, n.id LIMIT ?)
  FROM podcast_episodes e WHERE e.show_id = ? AND e.cache_path IS NOT NULL`, a.keepMax, a.id)
		if err != nil {
			return queued, removed, err
		}
		var drop []string
		for rows.Next() {
			var id string
			var played, recent bool
			if err := rows.Scan(&id, &played, &recent); err != nil {
				rows.Close()
				return queued, removed, err
			}
			if !want[id] && (played || !recent) {
				drop = append(drop, id)
			}
		}
		if err := rows.Close(); err != nil {
			return queued, removed, err
		}
		for _, id := range drop {
			if err := s.RemoveCached(ctx, id); err != nil {
				return queued, removed, err
			}
			removed++
		}
		// Wanted and not kept: queue them, newest first.
		prows, err := s.dbs.Reader().QueryContext(ctx, `SELECT id, COALESCE(audio_bytes,0) FROM podcast_episodes
  WHERE show_id = ? AND cache_path IS NULL ORDER BY published_at DESC, id`, a.id)
		if err != nil {
			return queued, removed, err
		}
		for prows.Next() {
			var p pending
			if err := prows.Scan(&p.id, &p.bytes); err != nil {
				prows.Close()
				return queued, removed, err
			}
			if want[p.id] {
				todo = append(todo, p)
			}
		}
		if err := prows.Close(); err != nil {
			return queued, removed, err
		}
	}
	used := s.CacheUsage(ctx)
	for _, p := range todo {
		if queued >= autoQueueMax {
			break
		}
		if s.autoTriedRecently(p.id) {
			continue
		}
		// An announced size that cannot fit is skipped; an unannounced one is assumed to fit and the job trims.
		if used+p.bytes > set.CacheBytes || p.bytes > maxEpisodeBytes {
			continue
		}
		if _, err := s.QueueCache(ctx, p.id); err != nil {
			return queued, removed, err
		}
		s.autoNoteTried(p.id)
		used += p.bytes
		queued++
	}
	return queued, removed, nil
}

func (s *Service) autoTriedRecently(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.autoTried[id]
	return ok && s.now().Sub(at) < autoRetryAfter
}

func (s *Service) autoNoteTried(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.autoTried == nil || len(s.autoTried) >= autoTriedMax {
		s.autoTried = map[string]time.Time{}
	}
	s.autoTried[id] = s.now()
}
