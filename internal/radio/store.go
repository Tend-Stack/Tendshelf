// Package radio keeps stations, each person's favourites and recent plays (decision 0038, point 10), and
// relays nothing itself: the live stream is opened through internal/metadata/online/podnet by the web layer.
// A station is keyed by its stream address; an OndaCast slug is only an optional hint, so a saved station
// plays with OndaCast unreachable. Favourites and plays belong to one person and are always read with
// that person's id in the SQL.
package radio

import (
	"context"
	"database/sql"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// MaxFavourites is the number of stations one person may keep (decision 0038, point 11).
const MaxFavourites = 100

// MaxRecent is the most recent plays listed.
const MaxRecent = 20

var (
	// ErrNotFound is an unknown station, or one the person has neither saved nor played.
	ErrNotFound = errors.New("radio: not found")
	// ErrTooMany means the person already keeps MaxFavourites stations.
	ErrTooMany = errors.New("radio: too many stations")
	// ErrBadURL means an address that can not be a stream.
	ErrBadURL = errors.New("radio: bad address")
)

// Station is one stored station. StreamURL stays on the server: no page or API answer carries it.
type Station struct {
	ID         string
	StreamURL  string
	Name       string
	SiteURL    string
	ArtURL     string
	ArtPath    string // the kept picture, relative to the data folder; empty until it is fetched
	Codec      string
	Format     string // "icy" (a continuous stream) or "hls" (an HLS playlist the server turns into one stream)
	Bitrate    int
	Country    string
	Genre      string
	Slug       string // the OndaCast slug, a hint only
	CheckedAt  int64
	FailCode   string
	Favourite  bool  // set by the person's lists
	AddedAt    int64 // favourite since
	LastPlayed int64 // set by Recent
}

// ArtSHA is the hash in the kept picture's file name (<sha>.jpg, .png or .webp), or "" when there is none.
func (x Station) ArtSHA() string {
	base := path.Base(x.ArtPath)
	sha := strings.TrimSuffix(base, path.Ext(base))
	if x.ArtPath == "" || len(sha) != 64 {
		return ""
	}
	return sha
}

// Input is what Save stores for a station.
type Input struct {
	StreamURL string
	Name      string
	SiteURL   string
	ArtURL    string
	Codec     string
	Format    string // empty means "icy"
	Country   string
	Genre     string
	Slug      string
}

// Store reads and writes radio rows.
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

const stationCols = `s.id, s.stream_url, s.name, s.site_url, s.art_url, s.art_path, s.codec, s.format, coalesce(s.bitrate_kbps,0), s.country, s.genre,
	coalesce(s.ondacast_slug,''), coalesce(s.checked_at,0), s.fail_code`

func scanStation(sc interface{ Scan(...any) error }, extra ...any) (Station, error) {
	var x Station
	dst := append([]any{&x.ID, &x.StreamURL, &x.Name, &x.SiteURL, &x.ArtURL, &x.ArtPath, &x.Codec, &x.Format, &x.Bitrate, &x.Country, &x.Genre, &x.Slug, &x.CheckedAt, &x.FailCode}, extra...)
	err := sc.Scan(dst...)
	return x, err
}

// Save stores the station (one row per stream address) and puts it among the person's favourites. Saving a
// station the person already has changes nothing but fills a missing name or hint. It writes an audit entry
// naming the station id, never its address.
func (s *Store) Save(ctx context.Context, userID string, in Input) (Station, bool, error) {
	return s.put(ctx, userID, in, true)
}

// Remember stores the station like Save but leaves it out of the favourites: the row exists and the person has
// a play record for it (zero plays until the relay notes one), so Get, the player and Recent reach it and
// Save on the Recent row can keep it later. It is how Play works on a search result that was never saved.
func (s *Store) Remember(ctx context.Context, userID string, in Input) (Station, error) {
	st, _, err := s.put(ctx, userID, in, false)
	return st, err
}

func (s *Store) put(ctx context.Context, userID string, in Input, favourite bool) (Station, bool, error) {
	norm, err := podcast.NormalizeFeedURL(in.StreamURL)
	if err != nil {
		return Station{}, false, ErrBadURL
	}
	name := CleanTitle(in.Name)
	if name == "" {
		name = "Radio station"
	}
	if len(in.Slug) > 200 {
		in.Slug = ""
	}
	format := "icy"
	if in.Format == "hls" {
		format = "hls"
	}
	now := s.nowMs()
	var id string
	created := false
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT id FROM radio_stations WHERE stream_url = ?`, norm).Scan(&id)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			id = db.NewID("rs")
			_, err = tx.ExecContext(ctx, `INSERT INTO radio_stations(id, stream_url, name, site_url, art_url, codec, format, country, genre, ondacast_slug, checked_at, created_at, updated_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, id, norm, name, clip(in.SiteURL, 2048), clip(in.ArtURL, 2048), clip(in.Codec, 32), format,
				clip(CleanTitle(in.Country), 64), clip(CleanTitle(in.Genre), 64), nullIf(in.Slug), now, now, now)
		case err == nil && in.Slug != "":
			_, err = tx.ExecContext(ctx, `UPDATE radio_stations SET ondacast_slug = ? WHERE id = ? AND ondacast_slug IS NULL`, in.Slug, id)
		}
		if err != nil {
			return err
		}
		if !favourite {
			_, err = tx.ExecContext(ctx, `INSERT INTO radio_plays(user_id, station_id, last_played_at, plays) VALUES (?,?,?,0)
				ON CONFLICT(user_id, station_id) DO NOTHING`, userID, id, now)
			return err
		}
		var err2 error
		created, err2 = addFavourite(ctx, tx, userID, id, now)
		return err2
	})
	if db.IsForeignKeyViolation(err) {
		return Station{}, false, ErrNotFound
	}
	if err != nil {
		return Station{}, false, err
	}
	st, err := s.Get(ctx, userID, id)
	return st, created, err
}

// addFavourite puts a station among the person's favourites at the end (nothing changes when it is already
// there) and audits it. It refuses the 101st with ErrTooMany.
func addFavourite(ctx context.Context, tx *sql.Tx, userID, id string, now int64) (bool, error) {
	var one int
	switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM radio_favourites WHERE user_id = ? AND station_id = ?`, userID, id).Scan(&one); {
	case err == nil:
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, err
	}
	var n, pos int
	if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(max(position),0) FROM radio_favourites WHERE user_id = ?`, userID).Scan(&n, &pos); err != nil {
		return false, err
	}
	if n >= MaxFavourites {
		return false, ErrTooMany
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO radio_favourites(user_id, station_id, position, added_at) VALUES (?,?,?,?)`, userID, id, pos+1, now); err != nil {
		return false, err
	}
	return true, db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: userID, Action: "radio.add", Target: "station:" + id})
}

// Resave puts a station the person has played back among their favourites by its id, with no new look at the
// stream. An id the person never played or saved is ErrNotFound; a full list is ErrTooMany.
func (s *Store) Resave(ctx context.Context, userID, id string) (Station, bool, error) {
	if _, err := s.Get(ctx, userID, id); err != nil {
		return Station{}, false, err
	}
	created := false
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		created, err = addFavourite(ctx, tx, userID, id, s.nowMs())
		return err
	})
	if err != nil {
		return Station{}, false, err
	}
	st, err := s.Get(ctx, userID, id)
	return st, created, err
}

// Kept finds a station the person already keeps, by its OndaCast slug or its stream address, so adding it
// again answers with it at once: no probe, and a full list does not refuse a station already on it.
func (s *Store) Kept(ctx context.Context, userID, slug, streamURL string) (Station, bool) {
	var id string
	switch {
	case slug != "":
		_ = s.st.Reader().QueryRowContext(ctx, `SELECT s.id FROM radio_stations s JOIN radio_favourites f ON f.station_id = s.id
			WHERE f.user_id = ? AND s.ondacast_slug = ?`, userID, slug).Scan(&id)
	case streamURL != "":
		norm, err := podcast.NormalizeFeedURL(streamURL)
		if err != nil {
			return Station{}, false
		}
		_ = s.st.Reader().QueryRowContext(ctx, `SELECT s.id FROM radio_stations s JOIN radio_favourites f ON f.station_id = s.id
			WHERE f.user_id = ? AND s.stream_url = ?`, userID, norm).Scan(&id)
	}
	if id == "" {
		return Station{}, false
	}
	st, err := s.Get(ctx, userID, id)
	return st, err == nil
}

// Known finds a station the person can already reach (a favourite or one they played) by its OndaCast slug,
// so Play on a search result needs no new look at OndaCast or the stream.
func (s *Store) Known(ctx context.Context, userID, slug string) (Station, bool) {
	if slug == "" {
		return Station{}, false
	}
	var id string
	_ = s.st.Reader().QueryRowContext(ctx, `SELECT s.id FROM radio_stations s
		WHERE s.ondacast_slug = ?1 AND (EXISTS (SELECT 1 FROM radio_favourites f WHERE f.station_id = s.id AND f.user_id = ?2)
			OR EXISTS (SELECT 1 FROM radio_plays p WHERE p.station_id = s.id AND p.user_id = ?2)) LIMIT 1`, slug, userID).Scan(&id)
	if id == "" {
		return Station{}, false
	}
	st, err := s.Get(ctx, userID, id)
	return st, err == nil
}

// FavouriteCount is how many stations the person keeps.
func (s *Store) FavouriteCount(ctx context.Context, userID string) (int, error) {
	var n int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM radio_favourites WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// Get returns a station the person keeps (a favourite) or has played. Anyone else gets ErrNotFound, the
// same answer as an unknown id.
func (s *Store) Get(ctx context.Context, userID, id string) (Station, error) {
	row := s.st.Reader().QueryRowContext(ctx, `SELECT `+stationCols+`, coalesce(f.added_at,0)
		FROM radio_stations s LEFT JOIN radio_favourites f ON f.station_id = s.id AND f.user_id = ?1
		WHERE s.id = ?2 AND (f.user_id IS NOT NULL OR EXISTS (SELECT 1 FROM radio_plays p WHERE p.user_id = ?1 AND p.station_id = s.id))`, userID, id)
	var added int64
	x, err := scanStation(row, &added)
	if errors.Is(err, sql.ErrNoRows) {
		return Station{}, ErrNotFound
	}
	x.AddedAt, x.Favourite = added, added > 0
	return x, err
}

// Favourites lists the person's saved stations in their order.
func (s *Store) Favourites(ctx context.Context, userID string) ([]Station, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+stationCols+`, f.added_at
		FROM radio_favourites f JOIN radio_stations s ON s.id = f.station_id WHERE f.user_id = ? ORDER BY f.position, s.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Station
	for rows.Next() {
		var added int64
		x, err := scanStation(rows, &added)
		if err != nil {
			return nil, err
		}
		x.AddedAt, x.Favourite = added, true
		out = append(out, x)
	}
	return out, rows.Err()
}

// Recent lists the stations the person played last, newest first.
func (s *Store) Recent(ctx context.Context, userID string, limit int) ([]Station, error) {
	if limit < 1 || limit > MaxRecent {
		limit = MaxRecent
	}
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+stationCols+`, p.last_played_at, coalesce(f.added_at,0)
		FROM radio_plays p JOIN radio_stations s ON s.id = p.station_id
		LEFT JOIN radio_favourites f ON f.station_id = s.id AND f.user_id = p.user_id
		WHERE p.user_id = ? ORDER BY p.last_played_at DESC, s.id LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Station
	for rows.Next() {
		var played, added int64
		x, err := scanStation(rows, &played, &added)
		if err != nil {
			return nil, err
		}
		x.LastPlayed, x.AddedAt, x.Favourite = played, added, added > 0
		out = append(out, x)
	}
	return out, rows.Err()
}

// Remove takes a station out of the person's favourites. Their play history keeps it playable.
func (s *Store) Remove(ctx context.Context, userID, id string) error {
	now := s.nowMs()
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `DELETE FROM radio_favourites WHERE user_id = ? AND station_id = ?`, userID, id)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: userID, Action: "radio.remove", Target: "station:" + id})
	})
}

// NotePlay records that the person started the station.
func (s *Store) NotePlay(ctx context.Context, userID, id string) error {
	now := s.nowMs()
	_, err := s.st.Writer().ExecContext(ctx, `INSERT INTO radio_plays(user_id, station_id, last_played_at, plays) VALUES (?,?,?,1)
		ON CONFLICT(user_id, station_id) DO UPDATE SET last_played_at = excluded.last_played_at, plays = plays + 1`, userID, id, now)
	return err
}

// SetChecked stores the result of the last look at the stream: the codec on success, a failure code else.
func (s *Store) SetChecked(ctx context.Context, id, codec, failCode string) error {
	_, err := s.st.Writer().ExecContext(ctx, `UPDATE radio_stations SET checked_at = ?, fail_code = ?, codec = CASE WHEN ? <> '' THEN ? ELSE codec END, updated_at = ? WHERE id = ?`,
		s.nowMs(), failCode, codec, codec, s.nowMs(), id)
	return err
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return ""
	}
	return s
}
