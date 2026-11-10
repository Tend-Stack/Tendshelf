package podcast

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
)

// FetchMeta is what a fetch learned besides the feed body.
type FetchMeta struct {
	ETag, LastModified string
	// MovedTo is the new feed address after permanent redirects (already normalized), "" when it did not move.
	MovedTo string
	// NextRefreshAt is when the scheduler should look again (Unix ms); 0 leaves it unset (due whenever the owner turns refresh on).
	NextRefreshAt int64
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// ApplyFeed stores a parsed feed for showID: the show's details, every episode (upserted by guid_key),
// gone marks for episodes the feed dropped, and the 3,000 newest kept (older ones nobody has state for
// are deleted). It returns how many episodes are new to the server. The show's fetch bookkeeping is
// updated in the same transaction and a failure is cleared.
func (s *Store) ApplyFeed(ctx context.Context, showID string, f rss.Feed, m FetchMeta) (added int, err error) {
	now := s.nowMs()
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := s.markFetched(ctx, tx, showID, f, true, m, now); err != nil {
			return err
		}
		have := map[string]string{}
		rows, err := tx.QueryContext(ctx, `SELECT guid_key, id FROM podcast_episodes WHERE show_id = ?`, showID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var k, id string
			if err := rows.Scan(&k, &id); err != nil {
				rows.Close()
				return err
			}
			have[k] = id
		}
		if err := rows.Close(); err != nil {
			return err
		}
		seen := make(map[string]bool, len(f.Episodes))
		for _, it := range f.Episodes {
			if it.GUIDKey == "" || it.AudioURL == "" || it.Title == "" || seen[it.GUIDKey] {
				continue
			}
			seen[it.GUIDKey] = true
			kind := it.Kind
			if kind != "trailer" && kind != "bonus" {
				kind = "full"
			}
			id, exists := have[it.GUIDKey]
			if !exists {
				id = db.NewID("pe")
				added++
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO podcast_episodes(id, show_id, guid_key, guid, title, notes_html, link, published_at, duration_ms,
					audio_url, audio_type, audio_bytes, season, number, kind, chapters_url, created_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT(show_id, guid_key) DO UPDATE SET guid = excluded.guid, title = excluded.title, notes_html = excluded.notes_html,
				  link = excluded.link, published_at = excluded.published_at, duration_ms = excluded.duration_ms, audio_url = excluded.audio_url,
				  audio_type = excluded.audio_type, audio_bytes = excluded.audio_bytes, season = excluded.season, number = excluded.number,
				  kind = excluded.kind, chapters_url = excluded.chapters_url, gone_at = NULL, updated_at = excluded.updated_at
				WHERE gone_at IS NOT NULL OR title IS NOT excluded.title OR notes_html IS NOT excluded.notes_html OR published_at IS NOT excluded.published_at
				  OR duration_ms IS NOT excluded.duration_ms OR audio_url IS NOT excluded.audio_url OR audio_type IS NOT excluded.audio_type
				  OR audio_bytes IS NOT excluded.audio_bytes OR kind IS NOT excluded.kind OR chapters_url IS NOT excluded.chapters_url
				  OR season IS NOT excluded.season OR number IS NOT excluded.number OR link IS NOT excluded.link OR guid IS NOT excluded.guid`,
				id, showID, it.GUIDKey, it.GUID, it.Title, it.NotesHTML, it.Link, it.PublishedAt, nullInt(it.DurationMs),
				it.AudioURL, it.AudioType, nullInt(it.AudioBytes), nullInt(int64(it.Season)), nullInt(int64(it.Number)), kind, it.ChaptersURL, now, now); err != nil {
				return err
			}
		}
		for k, id := range have {
			if !seen[k] {
				if _, err := tx.ExecContext(ctx, `UPDATE podcast_episodes SET gone_at = ?, updated_at = ? WHERE id = ? AND gone_at IS NULL`, now, now, id); err != nil {
					return err
				}
			}
		}
		// Keep the newest MaxEpisodesPerShow; older ones go unless somebody has a place, star or history for them.
		_, err = tx.ExecContext(ctx, `DELETE FROM podcast_episodes WHERE show_id = ? AND id IN (
			SELECT id FROM podcast_episodes WHERE show_id = ? ORDER BY published_at DESC, id DESC LIMIT -1 OFFSET ?)
			AND NOT EXISTS (SELECT 1 FROM podcast_episode_state t WHERE t.episode_id = podcast_episodes.id)`, showID, showID, MaxEpisodesPerShow)
		return err
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// MarkNotModified records a 304: the feed is current. No episode changes.
func (s *Store) MarkNotModified(ctx context.Context, showID string, m FetchMeta) error {
	now := s.nowMs()
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		return s.markFetched(ctx, tx, showID, rss.Feed{}, false, m, now)
	})
}

// markFetched writes the show row after a successful fetch. withFeed also stores the feed's details.
func (s *Store) markFetched(ctx context.Context, tx *sql.Tx, showID string, f rss.Feed, withFeed bool, m FetchMeta, now int64) error {
	if m.MovedTo != "" {
		// Only when no other show already has that address.
		var other string
		err := tx.QueryRowContext(ctx, `SELECT id FROM podcast_shows WHERE feed_url = ? AND id <> ?`, m.MovedTo, showID).Scan(&other)
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := tx.ExecContext(ctx, `UPDATE podcast_shows SET feed_url = ? WHERE id = ?`, m.MovedTo, showID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	q := `UPDATE podcast_shows SET etag = ?, last_modified = ?, fetched_at = ?, ok_at = ?, fail_code = '', fail_count = 0, next_refresh_at = ?, updated_at = ?`
	args := []any{clip(m.ETag, 512), clip(m.LastModified, 128), now, now, nullInt(m.NextRefreshAt), now}
	if withFeed {
		q += `, title = ?, author = ?, notes_html = ?, site_url = ?, language = ?, art_url = ?`
		args = append(args, clip(f.Title, 300), clip(f.Author, 300), f.Description, clip(f.Link, 2048), clip(f.Language, 35), clip(f.ImageURL, 2048))
	}
	r, err := tx.ExecContext(ctx, q+` WHERE id = ?`, append(args, showID)...)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

// MarkFailed records a failed fetch: the code, one more failure, and the next try after 2^n hours (at most 24).
func (s *Store) MarkFailed(ctx context.Context, showID, code string) error {
	now := s.nowMs()
	var n int
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT fail_count FROM podcast_shows WHERE id = ?`, showID).Scan(&n); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	n++
	wait := 24 * time.Hour
	if n < 5 {
		wait = time.Duration(1<<n) * time.Hour
	}
	_, err := s.st.Writer().ExecContext(ctx, `UPDATE podcast_shows SET fail_code = ?, fail_count = ?, fetched_at = ?, next_refresh_at = ?, updated_at = ? WHERE id = ?`,
		code, n, now, now+wait.Milliseconds(), now, showID)
	return err
}

// SetChapters stores a fetched chapters file for an episode.
func (s *Store) SetChapters(ctx context.Context, episodeID string, raw []byte) error {
	_, err := s.st.Writer().ExecContext(ctx, `UPDATE podcast_episodes SET chapters_json = ? WHERE id = ?`, string(raw), episodeID)
	return err
}

// DueShows lists followed shows the scheduler should refresh: next_refresh_at unset or reached.
func (s *Store) DueShows(ctx context.Context, limit int) ([]string, error) {
	return s.ids(ctx, `SELECT s.id FROM podcast_shows s WHERE EXISTS (SELECT 1 FROM podcast_subscriptions b WHERE b.show_id = s.id)
		AND (s.next_refresh_at IS NULL OR s.next_refresh_at <= ?) ORDER BY COALESCE(s.next_refresh_at,0), s.id LIMIT ?`, s.nowMs(), limit)
}

// StaleShows lists the person's shows last fetched over staleAfter ago (or never) whose back-off has passed.
func (s *Store) StaleShows(ctx context.Context, userID string, staleAfter time.Duration) ([]string, error) {
	now := s.nowMs()
	return s.ids(ctx, `SELECT s.id FROM podcast_subscriptions b JOIN podcast_shows s ON s.id = b.show_id WHERE b.user_id = ?
		AND (s.fetched_at IS NULL OR s.fetched_at < ?) AND (s.fail_count = 0 OR COALESCE(s.next_refresh_at,0) <= ?) ORDER BY s.id LIMIT 200`,
		userID, now-staleAfter.Milliseconds(), now)
}

// Orphans lists shows nobody follows and nobody has a place, star or history for.
func (s *Store) Orphans(ctx context.Context) ([]string, error) {
	return s.ids(ctx, `SELECT s.id FROM podcast_shows s WHERE NOT EXISTS (SELECT 1 FROM podcast_subscriptions b WHERE b.show_id = s.id)
		AND NOT EXISTS (SELECT 1 FROM podcast_episode_state t JOIN podcast_episodes e ON e.id = t.episode_id WHERE e.show_id = s.id) LIMIT 500`)
}

// DeleteShow removes a show and, by cascade, its episodes, only if nobody follows it or keeps a place
// in it at that moment (a follow that arrived after Orphans looked keeps the show). It reports whether
// a row was deleted.
func (s *Store) DeleteShow(ctx context.Context, showID string) (bool, error) {
	res, err := s.st.Writer().ExecContext(ctx, `DELETE FROM podcast_shows WHERE id = ?1
		AND NOT EXISTS (SELECT 1 FROM podcast_subscriptions b WHERE b.show_id = ?1)
		AND NOT EXISTS (SELECT 1 FROM podcast_episode_state t JOIN podcast_episodes e ON e.id = t.episode_id WHERE e.show_id = ?1)`, showID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *Store) ids(ctx context.Context, q string, args ...any) ([]string, error) {
	rows, err := s.st.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetArt records a stored picture for a show.
func (s *Store) SetArt(ctx context.Context, showID, path, sha string) error {
	_, err := s.st.Writer().ExecContext(ctx, `UPDATE podcast_shows SET art_path = ?, art_sha = ?, updated_at = ? WHERE id = ?`, path, sha, s.nowMs(), showID)
	return err
}
