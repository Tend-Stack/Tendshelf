package progress

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Reading positions (decision 0018). A reading place is a text anchor (chapter, sentence id, offset) plus a
// short quote and a percent, never a screen page. It lives beside listening progress and never writes it:
// without sentence timing any mapping between the two would be a guess.

const (
	// ReadingSessionGapMs is the longest pause that still belongs to the same reading session.
	ReadingSessionGapMs = 30 * 60_000
	// ReadingSpeedDefault is the characters per minute assumed until three sessions teach the real one.
	ReadingSpeedDefault = 1000
	// ReadingSpeedMin and ReadingSpeedMax clamp a learned speed, so one odd session cannot make times absurd.
	ReadingSpeedMin, ReadingSpeedMax = 300, 3000

	readingSpeedSessions    = 30     // the newest sessions that count
	readingSpeedMinActiveMs = 60_000 // a session shorter than this teaches nothing
	readingSpeedLearnedFrom = 3
	maxPlaceQuoteRunes      = 64
)

// ReadingPosition is one person's place in one book's text.
type ReadingPosition struct {
	UserID, BookID, EditionID string
	ChapterIdx                int
	SID                       string
	Off                       int
	Quote                     string
	CharPos                   int
	PercentBP                 int // 0..10000, hundredths of a percent
	FinishedAt                int64
	ClientUpdatedAt           int64
	ServerUpdatedAt           int64
	DeviceID, DeviceLabel     string
}

// Finished reports whether the person finished reading the book.
func (p ReadingPosition) Finished() bool { return p.FinishedAt != 0 }

// ReadingForBooks returns the person's reading places in the given books, keyed by book id; books
// without one are absent. One query per chunk, and only the person's own rows.
func (s *Service) ReadingForBooks(ctx context.Context, userID string, bookIDs []string) (map[string]ReadingPosition, error) {
	out := make(map[string]ReadingPosition, len(bookIDs))
	for len(bookIDs) > 0 {
		n := min(len(bookIDs), forBooksChunk)
		chunk := bookIDs[:n]
		bookIDs = bookIDs[n:]
		args := make([]any, 0, n+1)
		args = append(args, userID)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.st.Reader().QueryContext(ctx, selectReading+` WHERE user_id = ? AND book_id IN (?`+strings.Repeat(",?", n-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			p, err := scanReading(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[p.BookID] = p
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ReadingSave is one save of a reading place.
type ReadingSave struct {
	UserID, BookID, EditionID string
	ChapterIdx                int
	SID                       string
	Off                       int
	Quote                     string
	CharPos                   int
	PercentBP                 int
	Finished                  bool
	ClientUpdatedAt           int64
	DeviceID, DeviceLabel     string
}

const selectReading = `SELECT user_id, book_id, COALESCE(edition_id,''), chapter_idx, sid, off, quote, char_pos, percent_bp,
COALESCE(finished_at,0), client_updated_at, server_updated_at, device_id, device_label FROM reading_positions`

func scanReading(r rowScanner) (ReadingPosition, error) {
	var p ReadingPosition
	err := r.Scan(&p.UserID, &p.BookID, &p.EditionID, &p.ChapterIdx, &p.SID, &p.Off, &p.Quote, &p.CharPos, &p.PercentBP,
		&p.FinishedAt, &p.ClientUpdatedAt, &p.ServerUpdatedAt, &p.DeviceID, &p.DeviceLabel)
	return p, err
}

// Reading returns the person's place in the book's text; ok is false when they have none. Only their own
// row is ever read: there is no way to ask for another person's place.
func (s *Service) Reading(ctx context.Context, userID, bookID string) (ReadingPosition, bool, error) {
	p, err := scanReading(s.st.Reader().QueryRowContext(ctx, selectReading+` WHERE user_id = ? AND book_id = ?`, userID, bookID))
	if errors.Is(err, sql.ErrNoRows) {
		return ReadingPosition{}, false, nil
	}
	return p, err == nil, err
}

// resolveBook follows a merged-away text book id to the book that took over, so a reader that was open
// when the merge happened saves its place on the right book.
func resolveBook(ctx context.Context, tx *sql.Tx, bookID string) (string, error) {
	var to string
	err := tx.QueryRowContext(ctx, `SELECT to_id FROM merged_books WHERE from_id = ?`, bookID).Scan(&to)
	if errors.Is(err, sql.ErrNoRows) {
		return bookID, nil
	}
	return to, err
}

// SaveReading stores a reading place if it is newer than the stored one (the same rule as Save): the newest
// client time wins, and a time past the server clock is clamped. A save that is not newer is not an error:
// the bool is false and the position is the stored row.
func (s *Service) SaveReading(ctx context.Context, u ReadingSave) (ReadingPosition, bool, error) {
	switch {
	case u.UserID == "":
		return ReadingPosition{}, false, &InvalidError{"user_id"}
	case u.BookID == "":
		return ReadingPosition{}, false, &InvalidError{"book_id"}
	case !deviceIDRE.MatchString(u.DeviceID):
		return ReadingPosition{}, false, &InvalidError{"device_id"}
	case u.ChapterIdx < 1:
		return ReadingPosition{}, false, &InvalidError{"chapter_idx"}
	case u.SID != "" && (len(u.SID) < 6 || len(u.SID) > 40):
		return ReadingPosition{}, false, &InvalidError{"sid"}
	case u.Off < 0:
		return ReadingPosition{}, false, &InvalidError{"off"}
	case utf8.RuneCountInString(u.Quote) > maxPlaceQuoteRunes:
		return ReadingPosition{}, false, &InvalidError{"quote"}
	case u.CharPos < 0:
		return ReadingPosition{}, false, &InvalidError{"char_pos"}
	case u.PercentBP < 0 || u.PercentBP > 10000:
		return ReadingPosition{}, false, &InvalidError{"percent_bp"}
	case u.ClientUpdatedAt <= 0:
		return ReadingPosition{}, false, &InvalidError{"client_updated_at"}
	}
	u.DeviceLabel = cleanLabel(u.DeviceLabel)
	nowMs := s.nowMs()
	c := min(u.ClientUpdatedAt, nowMs+MaxAheadMs)
	var applied bool
	var out ReadingPosition
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		bookID, err := resolveBook(ctx, tx, u.BookID)
		if err != nil {
			return err
		}
		var finishedAt, edition any
		if u.Finished {
			finishedAt = nowMs
		}
		if u.EditionID != "" {
			edition = u.EditionID
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO reading_positions(user_id, book_id, edition_id, chapter_idx, sid, off, quote, char_pos,
percent_bp, finished_at, client_updated_at, server_updated_at, device_id, device_label) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(user_id, book_id) DO UPDATE SET edition_id = excluded.edition_id, chapter_idx = excluded.chapter_idx, sid = excluded.sid,
  off = excluded.off, quote = excluded.quote, char_pos = excluded.char_pos, percent_bp = excluded.percent_bp,
  finished_at = CASE WHEN excluded.finished_at IS NULL THEN NULL ELSE COALESCE(reading_positions.finished_at, excluded.finished_at) END,
  client_updated_at = excluded.client_updated_at, server_updated_at = excluded.server_updated_at,
  device_id = excluded.device_id, device_label = excluded.device_label
WHERE excluded.client_updated_at > reading_positions.client_updated_at`,
			u.UserID, bookID, edition, u.ChapterIdx, u.SID, u.Off, u.Quote, u.CharPos, u.PercentBP, finishedAt, c, nowMs, u.DeviceID, u.DeviceLabel)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		applied = n == 1
		out, err = scanReading(tx.QueryRowContext(ctx, selectReading+` WHERE user_id = ? AND book_id = ?`, u.UserID, bookID))
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return ReadingPosition{}, false, ErrNotFound
	}
	if err != nil {
		return ReadingPosition{}, false, err
	}
	return out, applied, nil
}

// SetReadingFinished marks the book finished (100 percent) or not (back to the start, so "Read again"
// restarts), as an explicit server act that wins over any stored place.
func (s *Service) SetReadingFinished(ctx context.Context, userID, bookID string, finished bool) (ReadingPosition, error) {
	var out ReadingPosition
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		bookID, err := resolveBook(ctx, tx, bookID)
		if err != nil {
			return err
		}
		nowMs := s.nowMs()
		c := nowMs
		var stored int64
		switch err := tx.QueryRowContext(ctx, `SELECT client_updated_at FROM reading_positions WHERE user_id = ? AND book_id = ?`,
			userID, bookID).Scan(&stored); {
		case err == nil:
			c = max(nowMs, stored+1)
		case errors.Is(err, sql.ErrNoRows):
		default:
			return err
		}
		var finishedAt any
		bp := 0
		if finished {
			finishedAt, bp = nowMs, 10000
		}
		if finished {
			_, err = tx.ExecContext(ctx, `INSERT INTO reading_positions(user_id, book_id, chapter_idx, char_pos, percent_bp, finished_at,
client_updated_at, server_updated_at, device_id) VALUES (?,?,1,0,?,?,?,?,?)
ON CONFLICT(user_id, book_id) DO UPDATE SET percent_bp = excluded.percent_bp,
  finished_at = COALESCE(reading_positions.finished_at, excluded.finished_at),
  client_updated_at = excluded.client_updated_at, server_updated_at = excluded.server_updated_at, device_id = excluded.device_id`,
				userID, bookID, bp, finishedAt, c, nowMs, ServerDevice)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO reading_positions(user_id, book_id, chapter_idx, char_pos, percent_bp, finished_at,
client_updated_at, server_updated_at, device_id) VALUES (?,?,1,0,0,NULL,?,?,?)
ON CONFLICT(user_id, book_id) DO UPDATE SET chapter_idx = 1, sid = '', off = 0, quote = '', char_pos = 0, percent_bp = 0,
  finished_at = NULL, client_updated_at = excluded.client_updated_at, server_updated_at = excluded.server_updated_at,
  device_id = excluded.device_id`, userID, bookID, c, nowMs, ServerDevice)
		}
		if err != nil {
			return err
		}
		out, err = scanReading(tx.QueryRowContext(ctx, selectReading+` WHERE user_id = ? AND book_id = ?`, userID, bookID))
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return ReadingPosition{}, ErrNotFound
	}
	return out, err
}

// AddReadingTime counts characters read and time spent reading. It extends the person's newest session on
// this device when it ended within 30 minutes of now, else it opens a new one. Times are Unix milliseconds.
func (s *Service) AddReadingTime(ctx context.Context, userID, bookID, device string, chars, activeMs, now int64) error {
	switch {
	case userID == "":
		return &InvalidError{"user_id"}
	case bookID == "":
		return &InvalidError{"book_id"}
	case !deviceIDRE.MatchString(device):
		return &InvalidError{"device_id"}
	case chars < 0:
		return &InvalidError{"chars_read"}
	case activeMs < 0:
		return &InvalidError{"active_ms"}
	case now <= 0:
		return &InvalidError{"now"}
	}
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		bookID, err := resolveBook(ctx, tx, bookID)
		if err != nil {
			return err
		}
		var id string
		var endedAt int64
		err = tx.QueryRowContext(ctx, `SELECT id, ended_at FROM reading_sessions WHERE user_id = ? AND book_id = ? AND device_id = ?
ORDER BY ended_at DESC LIMIT 1`, userID, bookID, device).Scan(&id, &endedAt)
		if err == nil && now-endedAt <= ReadingSessionGapMs {
			_, err = tx.ExecContext(ctx, `UPDATE reading_sessions SET ended_at = max(ended_at, ?), chars_read = chars_read + ?,
active_ms = active_ms + ? WHERE id = ?`, now, chars, activeMs, id)
			return err
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO reading_sessions(id, user_id, book_id, device_id, started_at, ended_at, chars_read, active_ms)
VALUES (?,?,?,?,?,?,?,?)`, db.NewID("rds"), userID, bookID, device, now, now, chars, activeMs)
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return ErrNotFound
	}
	return err
}

// ReadingSpeed is the person's reading speed in characters per minute, from their newest sessions of at least a
// minute of reading. learned is false until three such sessions exist; the default is then returned.
func (s *Service) ReadingSpeed(ctx context.Context, userID string) (cpm int, learned bool) {
	var chars, ms, n int64
	err := s.st.Reader().QueryRowContext(ctx, `SELECT coalesce(sum(chars_read),0), coalesce(sum(active_ms),0), count(*) FROM (
SELECT chars_read, active_ms FROM reading_sessions WHERE user_id = ? AND active_ms >= ? ORDER BY ended_at DESC, id DESC LIMIT ?)`,
		userID, readingSpeedMinActiveMs, readingSpeedSessions).Scan(&chars, &ms, &n)
	if err != nil || n < readingSpeedLearnedFrom || ms <= 0 {
		return ReadingSpeedDefault, false // a failed read is not worth failing a page for; the default is a fair guess
	}
	return int(min(max(chars*60_000/ms, ReadingSpeedMin), ReadingSpeedMax)), true
}
