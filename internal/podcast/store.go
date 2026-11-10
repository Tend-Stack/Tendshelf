// Package podcast keeps shows, episodes, follows and listening state (decision 0038). Shows and
// episodes are stored once per server; follows, places, stars and history belong to one person and are
// always read with that person's id in the SQL. The package never dials out itself: feeds, pictures
// and audio come through internal/metadata/online/podnet.
package podcast

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// Limits from decision 0038, point 11.
const (
	MaxFollows         = 200
	MaxEpisodesPerShow = 3000
	defaultPage        = 50
	maxPage            = 200
)

var (
	// ErrNotFound is an unknown id, or a show the person does not follow.
	ErrNotFound = errors.New("podcast: not found")
	// ErrTooMany means the person already follows MaxFollows shows.
	ErrTooMany = errors.New("podcast: too many follows")
	// ErrBadURL means an address that can not be a feed (not http or https, no host, user info, odd port).
	ErrBadURL = errors.New("podcast: bad address")
	// ErrInvalid means a field is out of range; Field names it.
	ErrInvalid = errors.New("podcast: invalid input")
)

// FieldError names the field that was refused. errors.Is(err, ErrInvalid) matches it.
type FieldError struct{ Field string }

func (e *FieldError) Error() string { return "podcast: invalid " + e.Field }

// Unwrap lets errors.Is(err, ErrInvalid) match.
func (e *FieldError) Unwrap() error { return ErrInvalid }

var deviceIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// Store reads and writes podcast rows.
type Store struct {
	st  *db.Store
	now func() time.Time
}

// NewStore returns a Store. A nil now means time.Now.
func NewStore(st *db.Store, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{st: st, now: now}
}

func (s *Store) nowMs() int64 { return s.now().UnixMilli() }

// NormalizeFeedURL turns what a person typed into the stored form: http or https only, lower-case
// scheme and host, default port and fragment removed, no user info. It does not check the host: the
// guarded client does that, after DNS, at every request.
func NormalizeFeedURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 8 || len(raw) > 2048 {
		return "", ErrBadURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || u.Opaque != "" {
		return "", ErrBadURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrBadURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return "", ErrBadURL
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || !(n == 80 || n == 443 || n >= 1024 && n <= 65535) {
			return "", ErrBadURL
		}
		if n == 80 && u.Scheme == "http" || n == 443 && u.Scheme == "https" {
			port = ""
		}
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	u.Fragment, u.RawFragment = "", ""
	out := u.String()
	if len(out) < 8 || len(out) > 2048 {
		return "", ErrBadURL
	}
	return out, nil
}

// ShowSettings are one person's choices for one show.
type ShowSettings struct {
	Speed      *float64 // nil means the person's usual speed
	SkipIntroS int
	SkipOutroS int
	ServerAuto bool
	KeepLatest int
}

// Valid reports whether every value is inside the table's CHECK ranges.
func (s ShowSettings) valid() string {
	switch {
	case s.Speed != nil && (*s.Speed < 0.5 || *s.Speed > 3.5):
		return "speed"
	case s.SkipIntroS < 0 || s.SkipIntroS > 600:
		return "skip_intro_s"
	case s.SkipOutroS < 0 || s.SkipOutroS > 600:
		return "skip_outro_s"
	case s.KeepLatest < 1 || s.KeepLatest > 50:
		return "keep_latest"
	}
	return ""
}

// ShowRow is a show, with the signed-in person's follow when there is one. Times are Unix ms, 0 when unset.
type ShowRow struct {
	ID, FeedURL, Title, Author, NotesHTML, SiteURL, Language string
	ArtURL, ArtPath, ArtSHA, OndaCastSlug                    string
	ETag, LastModified, FailCode                             string
	FetchedAt, OKAt, NextRefreshAt, CreatedAt                int64
	FailCount                                                int
	// The person's follow (zero when read without a person).
	FollowedAt, SeenAt int64
	Settings           ShowSettings
	NewCount           int
}

const showCols = `s.id, s.feed_url, s.title, s.author, s.notes_html, s.site_url, s.language, s.art_url, s.art_path, s.art_sha,
	COALESCE(s.ondacast_slug,''), s.etag, s.last_modified, s.fail_code, COALESCE(s.fetched_at,0), COALESCE(s.ok_at,0),
	COALESCE(s.next_refresh_at,0), s.created_at, s.fail_count`

type scanner interface{ Scan(dest ...any) error }

func scanShow(r scanner, withSub bool) (ShowRow, error) {
	var v ShowRow
	dst := []any{&v.ID, &v.FeedURL, &v.Title, &v.Author, &v.NotesHTML, &v.SiteURL, &v.Language, &v.ArtURL, &v.ArtPath, &v.ArtSHA,
		&v.OndaCastSlug, &v.ETag, &v.LastModified, &v.FailCode, &v.FetchedAt, &v.OKAt, &v.NextRefreshAt, &v.CreatedAt, &v.FailCount}
	var speed sql.NullFloat64
	var auto int
	if withSub {
		dst = append(dst, &v.FollowedAt, &v.SeenAt, &speed, &v.Settings.SkipIntroS, &v.Settings.SkipOutroS, &auto, &v.Settings.KeepLatest, &v.NewCount)
	}
	if err := r.Scan(dst...); err != nil {
		return ShowRow{}, err
	}
	if speed.Valid {
		f := speed.Float64
		v.Settings.Speed = &f
	}
	v.Settings.ServerAuto = auto == 1
	return v, nil
}

// subCols follow showCols for a show joined to its subscription (alias b) for the person.
const subCols = `, b.followed_at, b.seen_at, b.speed, b.skip_intro_s, b.skip_outro_s, b.server_auto, b.keep_latest,
	(SELECT count(*) FROM podcast_episodes e LEFT JOIN podcast_episode_state t ON t.episode_id = e.id AND t.user_id = b.user_id
		WHERE e.show_id = s.id AND e.gone_at IS NULL AND e.published_at > b.seen_at AND COALESCE(t.played,0) = 0)`

// Follow makes userID follow the show at feedURL, creating the show row if the server has not seen it.
// slug is an optional OndaCast hint. created is true when the person was not following it yet; the
// caller queues a refresh when the show has never been fetched (ShowRow.FetchedAt == 0). A person who
// already follows MaxFollows other shows gets ErrTooMany.
func (s *Store) Follow(ctx context.Context, userID, feedURL, slug string) (showID string, created bool, err error) {
	norm, err := NormalizeFeedURL(feedURL)
	if err != nil {
		return "", false, err
	}
	if len(slug) > 200 {
		slug = ""
	}
	now := s.nowMs()
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id FROM podcast_shows WHERE feed_url = ?`, norm).Scan(&showID)
		if errors.Is(err, sql.ErrNoRows) {
			showID = db.NewID("ps")
			_, err = tx.ExecContext(ctx, `INSERT INTO podcast_shows(id, feed_url, ondacast_slug, created_at, updated_at) VALUES (?,?,?,?,?)`,
				showID, norm, nullIf(slug), now, now)
		} else if err == nil && slug != "" {
			_, err = tx.ExecContext(ctx, `UPDATE podcast_shows SET ondacast_slug = ? WHERE id = ? AND ondacast_slug IS NULL`, slug, showID)
		}
		if err != nil {
			return err
		}
		var one int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM podcast_subscriptions WHERE user_id = ? AND show_id = ?`, userID, showID).Scan(&one); {
		case err == nil:
			return nil // already following
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM podcast_subscriptions WHERE user_id = ?`, userID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxFollows {
			return ErrTooMany
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO podcast_subscriptions(user_id, show_id, followed_at, seen_at) VALUES (?,?,?,?)`,
			userID, showID, now, now); err != nil {
			return err
		}
		created = true
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: userID, Action: "podcast.follow", Target: "show:" + showID})
	})
	if db.IsForeignKeyViolation(err) {
		return "", false, ErrNotFound
	}
	if err != nil {
		return "", false, err
	}
	return showID, created, nil
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Unfollow removes the person's follow. Their places, stars and history stay (they keep the episodes
// playable); the show goes at the next prune when nobody keeps anything of it.
func (s *Store) Unfollow(ctx context.Context, userID, showID string) error {
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `DELETE FROM podcast_subscriptions WHERE user_id = ? AND show_id = ?`, userID, showID)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: s.nowMs(), UserID: userID, Action: "podcast.unfollow", Target: "show:" + showID})
	})
}

// Shows lists the shows userID follows, A to Z, each with its count of new episodes.
func (s *Store) Shows(ctx context.Context, userID string) ([]ShowRow, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+showCols+subCols+` FROM podcast_subscriptions b
		JOIN podcast_shows s ON s.id = b.show_id WHERE b.user_id = ? ORDER BY lower(s.title), s.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShowRow
	for rows.Next() {
		v, err := scanShow(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Show returns a show userID follows, or ErrNotFound.
func (s *Store) Show(ctx context.Context, userID, showID string) (ShowRow, error) {
	v, err := scanShow(s.st.Reader().QueryRowContext(ctx, `SELECT `+showCols+subCols+` FROM podcast_subscriptions b
		JOIN podcast_shows s ON s.id = b.show_id WHERE b.user_id = ? AND b.show_id = ?`, userID, showID), true)
	if errors.Is(err, sql.ErrNoRows) {
		return ShowRow{}, ErrNotFound
	}
	return v, err
}

// ShowByID returns a show with no person attached (jobs, the art route after share checks).
func (s *Store) ShowByID(ctx context.Context, showID string) (ShowRow, error) {
	v, err := scanShow(s.st.Reader().QueryRowContext(ctx, `SELECT `+showCols+` FROM podcast_shows s WHERE s.id = ?`, showID), false)
	if errors.Is(err, sql.ErrNoRows) {
		return ShowRow{}, ErrNotFound
	}
	return v, err
}

// Follows reports whether userID follows showID.
func (s *Store) Follows(ctx context.Context, userID, showID string) bool {
	var one int
	return s.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM podcast_subscriptions WHERE user_id = ? AND show_id = ?`, userID, showID).Scan(&one) == nil
}

// Knows reports whether the person follows the show or keeps a place, star or history for one of its
// episodes. It decides who may see a show's picture.
func (s *Store) Knows(ctx context.Context, userID, showID string) bool {
	var one int
	return s.st.Reader().QueryRowContext(ctx, `SELECT 1 WHERE EXISTS (SELECT 1 FROM podcast_subscriptions WHERE user_id = ?1 AND show_id = ?2)
		OR EXISTS (SELECT 1 FROM podcast_episode_state t JOIN podcast_episodes e ON e.id = t.episode_id WHERE t.user_id = ?1 AND e.show_id = ?2)`,
		userID, showID).Scan(&one) == nil
}

// MarkSeen clears the person's new-episode badge for a show.
func (s *Store) MarkSeen(ctx context.Context, userID, showID string) error {
	r, err := s.st.Writer().ExecContext(ctx, `UPDATE podcast_subscriptions SET seen_at = ? WHERE user_id = ? AND show_id = ?`, s.nowMs(), userID, showID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetShowSettings stores the person's choices for a show they follow.
func (s *Store) SetShowSettings(ctx context.Context, userID, showID string, v ShowSettings) error {
	if f := v.valid(); f != "" {
		return &FieldError{f}
	}
	var speed any
	if v.Speed != nil {
		speed = *v.Speed
	}
	auto := 0
	if v.ServerAuto {
		auto = 1
	}
	r, err := s.st.Writer().ExecContext(ctx, `UPDATE podcast_subscriptions SET speed = ?, skip_intro_s = ?, skip_outro_s = ?, server_auto = ?, keep_latest = ?
		WHERE user_id = ? AND show_id = ?`, speed, v.SkipIntroS, v.SkipOutroS, auto, v.KeepLatest, userID, showID)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ShowSettings returns the person's choices for a show they follow.
func (s *Store) ShowSettings(ctx context.Context, userID, showID string) (ShowSettings, error) {
	v, err := s.Show(ctx, userID, showID)
	return v.Settings, err
}

// EpisodeState is one person's place in one episode (zero when they have none).
type EpisodeState struct {
	PositionMs                                         int64
	Played, Favourite                                  bool
	PlayedAt, FavouriteAt, LastPlayedAt, ClientUpdated int64
	UpdatedAt                                          int64
	DeviceID                                           string
}

// Episode is one episode with the asking person's state.
type Episode struct {
	ID, ShowID, ShowTitle, GUID, Title, NotesHTML, Link string
	PublishedAt, DurationMs, AudioBytes                 int64
	AudioURL, AudioType, Kind, ChaptersURL              string
	ChaptersJSON, CachePath                             string
	Season, Number                                      int
	CacheBytes, CachedAt, GoneAt, CreatedAt, UpdatedAt  int64
	State                                               EpisodeState
}

const episodeCols = `e.id, e.show_id, s.title, e.guid, e.title, e.notes_html, e.link, e.published_at, COALESCE(e.duration_ms,0),
	COALESCE(e.audio_bytes,0), e.audio_url, e.audio_type, e.kind, e.chapters_url, COALESCE(e.chapters_json,''), COALESCE(e.cache_path,''),
	COALESCE(e.season,0), COALESCE(e.number,0), COALESCE(e.cache_bytes,0), COALESCE(e.cached_at,0), COALESCE(e.gone_at,0), e.created_at, e.updated_at,
	COALESCE(t.position_ms,0), COALESCE(t.played,0), COALESCE(t.played_at,0), COALESCE(t.favourite,0), COALESCE(t.favourite_at,0),
	COALESCE(t.last_played_at,0), COALESCE(t.client_updated_at,0), COALESCE(t.updated_at,0), COALESCE(t.device_id,'')`

// episodeFrom joins an episode (e) to its show (s) and the person's state (t); the one argument is the person's id.
const episodeFrom = ` FROM podcast_episodes e JOIN podcast_shows s ON s.id = e.show_id
	LEFT JOIN podcast_episode_state t ON t.episode_id = e.id AND t.user_id = ?`

func scanEpisode(r scanner) (Episode, error) {
	var v Episode
	var played, fav int
	err := r.Scan(&v.ID, &v.ShowID, &v.ShowTitle, &v.GUID, &v.Title, &v.NotesHTML, &v.Link, &v.PublishedAt, &v.DurationMs, &v.AudioBytes,
		&v.AudioURL, &v.AudioType, &v.Kind, &v.ChaptersURL, &v.ChaptersJSON, &v.CachePath, &v.Season, &v.Number, &v.CacheBytes, &v.CachedAt,
		&v.GoneAt, &v.CreatedAt, &v.UpdatedAt, &v.State.PositionMs, &played, &v.State.PlayedAt, &fav, &v.State.FavouriteAt,
		&v.State.LastPlayedAt, &v.State.ClientUpdated, &v.State.UpdatedAt, &v.State.DeviceID)
	v.State.Played, v.State.Favourite = played == 1, fav == 1
	return v, err
}

// Episode returns one episode with userID's state. Whether the person may open it is share.Can's call.
func (s *Store) Episode(ctx context.Context, userID, episodeID string) (Episode, error) {
	v, err := scanEpisode(s.st.Reader().QueryRowContext(ctx, `SELECT `+episodeCols+episodeFrom+` WHERE e.id = ?`, userID, episodeID))
	if errors.Is(err, sql.ErrNoRows) {
		return Episode{}, ErrNotFound
	}
	return v, err
}

// EpisodeQuery is a page of a show's episodes.
type EpisodeQuery struct {
	Sort   string // "newest" (default) or "oldest"
	Filter string // "all" (default), "unplayed", "in_progress" or "played"
	Text   string // matched in the title, on this show's rows only
	Limit  int    // 0 means 50; at most 200
	After  string // the cursor a page returned
	Since  int64  // only episodes changed after this Unix ms (the app's feed); 0 means all
}

// Episodes returns one page of a show's episodes and the cursor for the next, "" at the end.
func (s *Store) Episodes(ctx context.Context, userID, showID string, q EpisodeQuery) ([]Episode, string, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = defaultPage
	}
	limit = min(limit, maxPage)
	var where []string
	args := []any{userID, showID}
	where = append(where, `e.show_id = ?`, `(e.gone_at IS NULL OR t.episode_id IS NOT NULL)`)
	switch q.Filter {
	case "", "all":
	case "unplayed":
		where = append(where, `COALESCE(t.played,0) = 0 AND COALESCE(t.position_ms,0) = 0`)
	case "in_progress":
		where = append(where, `COALESCE(t.played,0) = 0 AND COALESCE(t.position_ms,0) > 0`)
	case "played":
		where = append(where, `COALESCE(t.played,0) = 1`)
	default:
		return nil, "", &FieldError{"filter"}
	}
	if txt := strings.TrimSpace(q.Text); txt != "" {
		where = append(where, `e.title LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(txt)+"%")
	}
	if q.Since > 0 {
		where = append(where, `(e.updated_at > ? OR COALESCE(t.updated_at,0) > ?)`)
		args = append(args, q.Since, q.Since)
	}
	cmp, order := "<", "DESC"
	switch q.Sort {
	case "", "newest":
	case "oldest":
		cmp, order = ">", "ASC"
	default:
		return nil, "", &FieldError{"sort"}
	}
	if q.After != "" {
		at, id, ok := parseCursor(q.After)
		if !ok {
			return nil, "", &FieldError{"after"}
		}
		where = append(where, `(e.published_at `+cmp+` ? OR (e.published_at = ? AND e.id `+cmp+` ?))`)
		args = append(args, at, at, id)
	}
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+episodeCols+episodeFrom+` WHERE `+strings.Join(where, " AND ")+
		` ORDER BY e.published_at `+order+`, e.id `+order+` LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		v, err := scanEpisode(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[limit-1]
		next = strconv.FormatInt(last.PublishedAt, 10) + ":" + last.ID
	}
	return out, next, nil
}

func parseCursor(s string) (int64, string, bool) {
	a, id, ok := strings.Cut(s, ":")
	n, err := strconv.ParseInt(a, 10, 64)
	if !ok || err != nil || n < 0 || id == "" || len(id) > 64 {
		return 0, "", false
	}
	return n, id, true
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Progress is one save of a listening place. ClientUpdatedAt is the device's clock in Unix ms.
type Progress struct {
	PositionMs      int64
	ClientUpdatedAt int64
	DeviceID        string
	Finished        bool
}

// SaveResult is what SaveProgress did: Applied is false when a newer save already stood.
type SaveResult struct {
	Applied bool
	State   EpisodeState
}

// SaveProgress stores a place with the same newer-wins rule as a book's: the save with the later
// ClientUpdatedAt wins (clamped to a minute ahead of the server clock), and Finished marks the episode played.
func (s *Store) SaveProgress(ctx context.Context, userID, episodeID string, p Progress) (SaveResult, error) {
	switch {
	case !deviceIDRE.MatchString(p.DeviceID):
		return SaveResult{}, &FieldError{"device_id"}
	case p.PositionMs < 0:
		return SaveResult{}, &FieldError{"position_ms"}
	case p.ClientUpdatedAt <= 0:
		return SaveResult{}, &FieldError{"client_updated_at"}
	}
	now := s.nowMs()
	c := min(p.ClientUpdatedAt, now+progress.MaxAheadMs)
	var res SaveResult
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var dur sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT duration_ms FROM podcast_episodes WHERE id = ?`, episodeID).Scan(&dur); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		pos := p.PositionMs
		if dur.Valid && dur.Int64 > 0 && pos > dur.Int64 {
			pos = dur.Int64
		}
		played, playedAt := 0, any(nil)
		if p.Finished {
			played, playedAt = 1, now
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO podcast_episode_state(user_id, episode_id, position_ms, played, played_at, last_played_at, device_id, client_updated_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT(user_id, episode_id) DO UPDATE SET position_ms = excluded.position_ms, played = excluded.played,
			  played_at = CASE WHEN excluded.played = 1 THEN COALESCE(podcast_episode_state.played_at, excluded.played_at) END,
			  last_played_at = excluded.last_played_at, device_id = excluded.device_id,
			  client_updated_at = excluded.client_updated_at, updated_at = excluded.updated_at
			WHERE excluded.client_updated_at > podcast_episode_state.client_updated_at`,
			userID, episodeID, pos, played, playedAt, now, p.DeviceID, c, now)
		if err != nil {
			return err
		}
		n, _ := r.RowsAffected()
		res.Applied = n == 1
		return nil
	})
	if err != nil {
		return SaveResult{}, err
	}
	ep, err := s.Episode(ctx, userID, episodeID)
	if err != nil {
		return SaveResult{}, err
	}
	res.State = ep.State
	return res, nil
}

// bumped is the client clock value an explicit act (mark played) takes, so an older save from another
// device cannot undo it: the later of now and one past the stored value.
const bumped = `MAX(?, COALESCE((SELECT client_updated_at FROM podcast_episode_state WHERE user_id = ? AND episode_id = ?), 0) + 1)`

// SetPlayed marks an episode played (its place kept) or not played (place cleared).
func (s *Store) SetPlayed(ctx context.Context, userID, episodeID string, played bool) error {
	now := s.nowMs()
	var err error
	if played {
		_, err = s.st.Writer().ExecContext(ctx, `INSERT INTO podcast_episode_state(user_id, episode_id, played, played_at, client_updated_at, updated_at)
			VALUES (?,?,1,?,`+bumped+`,?)
			ON CONFLICT(user_id, episode_id) DO UPDATE SET played = 1, played_at = excluded.played_at, client_updated_at = excluded.client_updated_at, updated_at = excluded.updated_at`,
			userID, episodeID, now, now, userID, episodeID, now)
	} else {
		_, err = s.st.Writer().ExecContext(ctx, `INSERT INTO podcast_episode_state(user_id, episode_id, played, played_at, position_ms, client_updated_at, updated_at)
			VALUES (?,?,0,NULL,0,`+bumped+`,?)
			ON CONFLICT(user_id, episode_id) DO UPDATE SET played = 0, played_at = NULL, position_ms = 0, client_updated_at = excluded.client_updated_at, updated_at = excluded.updated_at`,
			userID, episodeID, now, userID, episodeID, now)
	}
	if db.IsForeignKeyViolation(err) {
		return ErrNotFound
	}
	return err
}

// SetFavourite stars or unstars an episode.
func (s *Store) SetFavourite(ctx context.Context, userID, episodeID string, on bool) error {
	now := s.nowMs()
	fav, at := 0, any(nil)
	if on {
		fav, at = 1, now
	}
	_, err := s.st.Writer().ExecContext(ctx, `INSERT INTO podcast_episode_state(user_id, episode_id, favourite, favourite_at, updated_at) VALUES (?,?,?,?,?)
		ON CONFLICT(user_id, episode_id) DO UPDATE SET favourite = excluded.favourite, favourite_at = excluded.favourite_at, updated_at = excluded.updated_at`,
		userID, episodeID, fav, at, now)
	if db.IsForeignKeyViolation(err) {
		return ErrNotFound
	}
	return err
}

func (s *Store) list(ctx context.Context, userID, cond, order string, limit int, extra ...any) ([]Episode, error) {
	limit = min(max(limit, 1), maxPage)
	args := append([]any{userID, userID}, extra...)
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+episodeCols+episodeFrom+` WHERE `+cond+` ORDER BY `+order+` LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		v, err := scanEpisode(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// History lists the episodes userID has played, newest first.
func (s *Store) History(ctx context.Context, userID string, limit int) ([]Episode, error) {
	return s.list(ctx, userID, `t.user_id = ? AND t.last_played_at IS NOT NULL`, `t.last_played_at DESC, e.id`, limit)
}

// Favourites lists the episodes userID starred, newest star first.
func (s *Store) Favourites(ctx context.Context, userID string, limit int) ([]Episode, error) {
	return s.list(ctx, userID, `t.user_id = ? AND t.favourite = 1`, `t.favourite_at DESC, e.id`, limit)
}

// NewEpisodes lists unplayed episodes of followed shows published after the person last looked
// (the show's seen mark) and, when since > 0, first stored on this server after since.
func (s *Store) NewEpisodes(ctx context.Context, userID string, limit int, since int64) ([]Episode, error) {
	return s.list(ctx, userID, `e.show_id IN (SELECT show_id FROM podcast_subscriptions WHERE user_id = ? AND e.published_at > seen_at)
		AND e.gone_at IS NULL AND COALESCE(t.played,0) = 0 AND e.created_at > ?`, `e.published_at DESC, e.id`, limit, since)
}

// NewTotal is the sum of the person's new-episode counts (the nav badge).
func (s *Store) NewTotal(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM podcast_subscriptions b
		JOIN podcast_episodes e ON e.show_id = b.show_id AND e.gone_at IS NULL AND e.published_at > b.seen_at
		LEFT JOIN podcast_episode_state t ON t.episode_id = e.id AND t.user_id = b.user_id
		WHERE b.user_id = ? AND COALESCE(t.played,0) = 0`, userID).Scan(&n)
	return n, err
}

// LastUnfinished is the episode the person listened to most recently and has not finished (a place
// saved, not marked played, still listed by its show). ErrNotFound when there is none.
func (s *Store) LastUnfinished(ctx context.Context, userID string) (Episode, error) {
	eps, err := s.list(ctx, userID, `t.user_id = ? AND t.played = 0 AND t.position_ms > 0 AND e.gone_at IS NULL`, `t.client_updated_at DESC, e.id`, 1)
	if err != nil {
		return Episode{}, err
	}
	if len(eps) == 0 {
		return Episode{}, ErrNotFound
	}
	return eps[0], nil
}
