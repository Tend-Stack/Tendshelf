package share

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/token"
)

// Errors the callers match on (decision 0031). The web layer answers all of them with one plain 404.
var (
	ErrFeedUnknown  = errors.New("share: no such podcast feed")
	ErrFeedEnded    = errors.New("share: this podcast feed was turned off")
	ErrTooManyFeeds = errors.New("share: too many podcast feeds")
)

// Reasons Lookup gives for a feed that does not answer. Only the first two count as a guess.
const (
	FeedMalformed = "malformed"
	FeedUnknown   = "unknown"
	FeedEnded     = "ended"
)

const (
	maxLiveFeeds   = 50
	maxFeedName    = 64
	feedPruneAfter = 30 * 24 * time.Hour
	feedTouchEvery = time.Hour
)

// Feed is one row of podcast_feeds: a private RSS address of one person for one book or one series.
type Feed struct {
	ID, UserID, Name, BookID, SeriesID string
	CreatedAt, LastUsedAt, RevokedAt   int64
}

// Target names what the feed carries, as written in audit rows: "book:<id>" or "series:<id>".
func (f Feed) Target() string {
	if f.BookID != "" {
		return "book:" + f.BookID
	}
	return "series:" + f.SeriesID
}

// FeedRow is a live feed with the name of its target, for Your devices. TargetName is empty when the
// book or series is gone.
type FeedRow struct {
	Feed
	TargetName string
}

// Feeds reads and writes podcast_feeds. It decides nothing about access: the web layer re-checks the
// owner's rights on every request.
type Feeds struct {
	st  *db.Store
	now func() time.Time
}

// NewFeeds returns the feeds repository; a nil now means time.Now.
func NewFeeds(st *db.Store, now func() time.Time) *Feeds {
	if now == nil {
		now = time.Now
	}
	return &Feeds{st: st, now: now}
}

// Create makes a feed for userID and returns the raw token, shown once. Exactly one of bookID and
// seriesID is set. The caller has checked the person's rights and writes the audit row.
func (r *Feeds) Create(ctx context.Context, tx *sql.Tx, userID, name, bookID, seriesID string) (string, Feed, error) {
	if (bookID == "") == (seriesID == "") {
		return "", Feed{}, ErrFeedUnknown
	}
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxFeedName {
		name = string([]rune(name)[:maxFeedName])
	}
	nowMs := r.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM podcast_feeds WHERE user_id = ? AND revoked_at IS NOT NULL AND revoked_at < ?`,
		userID, nowMs-feedPruneAfter.Milliseconds()); err != nil {
		return "", Feed{}, err
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM podcast_feeds WHERE user_id = ? AND revoked_at IS NULL`, userID).Scan(&live); err != nil {
		return "", Feed{}, err
	}
	if live >= maxLiveFeeds {
		return "", Feed{}, ErrTooManyFeeds
	}
	tok, hash := token.New()
	f := Feed{ID: db.NewID("fd"), UserID: userID, Name: name, BookID: bookID, SeriesID: seriesID, CreatedAt: nowMs}
	if _, err := tx.ExecContext(ctx, `INSERT INTO podcast_feeds (id, token_hash, user_id, name, book_id, series_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, f.ID, hash[:], userID, name, nullIf(bookID), nullIf(seriesID), nowMs); err != nil {
		return "", Feed{}, err
	}
	return tok, f, nil
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Lookup finds the feed a token names. The reason is one of FeedMalformed, FeedUnknown or FeedEnded
// whenever the error is not nil; ended feeds are returned too, so callers can tell them from guesses.
func (r *Feeds) Lookup(ctx context.Context, tok string) (Feed, string, error) {
	hash, ok := token.Hash(tok)
	if !ok {
		return Feed{}, FeedMalformed, ErrFeedUnknown
	}
	var f Feed
	var book, series sql.NullString
	var used, revoked sql.NullInt64
	err := r.st.Reader().QueryRowContext(ctx, `SELECT id, user_id, name, book_id, series_id, created_at, last_used_at, revoked_at
		FROM podcast_feeds WHERE token_hash = ?`, hash[:]).Scan(&f.ID, &f.UserID, &f.Name, &book, &series, &f.CreatedAt, &used, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return Feed{}, FeedUnknown, ErrFeedUnknown
	}
	if err != nil {
		return Feed{}, "", err
	}
	f.BookID, f.SeriesID = book.String, series.String
	f.LastUsedAt, f.RevokedAt = used.Int64, revoked.Int64
	if revoked.Valid {
		return f, FeedEnded, ErrFeedEnded
	}
	return f, "", nil
}

// Touch records that an app used the feed, at most once an hour. It writes only when the row read by
// Lookup is stale.
func (r *Feeds) Touch(ctx context.Context, f Feed) {
	nowMs := r.now().UnixMilli()
	cut := nowMs - feedTouchEvery.Milliseconds()
	if f.LastUsedAt != 0 && f.LastUsedAt >= cut {
		return
	}
	_ = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE podcast_feeds SET last_used_at = ? WHERE id = ? AND (last_used_at IS NULL OR last_used_at < ?)`,
			nowMs, f.ID, cut)
		return err
	})
}

// Revoke turns off one live feed of userID; any other id is ErrFeedUnknown.
func (r *Feeds) Revoke(ctx context.Context, tx *sql.Tx, userID, id string) (Feed, error) {
	var f Feed
	var book, series sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id, user_id, name, book_id, series_id, created_at FROM podcast_feeds
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, id, userID).Scan(&f.ID, &f.UserID, &f.Name, &book, &series, &f.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Feed{}, ErrFeedUnknown
	}
	if err != nil {
		return Feed{}, err
	}
	f.BookID, f.SeriesID = book.String, series.String
	f.RevokedAt = r.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `UPDATE podcast_feeds SET revoked_at = ? WHERE id = ?`, f.RevokedAt, id); err != nil {
		return Feed{}, err
	}
	return f, nil
}

// Own lists the live feeds of userID, most recently used first.
func (r *Feeds) Own(ctx context.Context, userID string) ([]FeedRow, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT f.id, f.user_id, f.name, f.book_id, f.series_id, f.created_at, f.last_used_at,
		COALESCE(b.title, s.name, '')
		FROM podcast_feeds f LEFT JOIN books b ON b.id = f.book_id LEFT JOIN series s ON s.id = f.series_id
		WHERE f.user_id = ? AND f.revoked_at IS NULL
		ORDER BY COALESCE(f.last_used_at, f.created_at) DESC, f.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeedRow
	for rows.Next() {
		var fr FeedRow
		var book, series sql.NullString
		var used sql.NullInt64
		if err := rows.Scan(&fr.ID, &fr.UserID, &fr.Name, &book, &series, &fr.CreatedAt, &used, &fr.TargetName); err != nil {
			return nil, err
		}
		fr.BookID, fr.SeriesID, fr.LastUsedAt = book.String, series.String, used.Int64
		out = append(out, fr)
	}
	return out, rows.Err()
}
