package progress

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

const (
	// MaxAheadMs is how far past the server clock a client timestamp may reach before it is clamped.
	MaxAheadMs = 60_000
	// ServerDevice is the device id of explicit server-side acts (mark finished, listen again).
	ServerDevice = "server"
	// MaxNoteRunes is the longest bookmark note.
	MaxNoteRunes = 2000

	maxLabelBytes = 64
	forBooksChunk = 500
)

// MaxBookmarksPerBook is how many live bookmarks one person may keep on one book. It is a variable so tests can lower it.
var MaxBookmarksPerBook = 1000

var deviceIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

var (
	// ErrNotFound is returned for an absent row, another person's bookmark and an unknown book or user.
	ErrNotFound = errors.New("progress: not found")
	// ErrLimit is returned when a person already keeps the most bookmarks one book allows.
	ErrLimit = errors.New("progress: limit reached")
	// ErrClientIDTaken means a bookmark client id already belongs to another person or another book.
	ErrClientIDTaken = errors.New("progress: client id already used")
	// ErrInvalid is wrapped by every InvalidError.
	ErrInvalid = errors.New("progress: invalid input")
)

// InvalidError names the input field that was refused.
type InvalidError struct{ Field string }

func (e *InvalidError) Error() string { return "progress: invalid " + e.Field }

// Unwrap lets errors.Is(err, ErrInvalid) match.
func (e *InvalidError) Unwrap() error { return ErrInvalid }

// Progress is one person's place in one book. FinishedAt is 0 when the book is not finished.
type Progress struct {
	UserID, BookID string
	PositionMs     int64
	DurationMs     int64
	FinishedAt     int64
	// ClientUpdatedAt is the clamped time of the newest applied save; ServerUpdatedAt is when the server stored it.
	ClientUpdatedAt, ServerUpdatedAt int64
	// DeviceID and DeviceLabel name the device of the newest applied save. They are display and merge data only.
	DeviceID, DeviceLabel string
}

// Finished reports whether the person finished the book.
func (p Progress) Finished() bool { return p.FinishedAt != 0 }

// Update is one progress save. Speed 0 and ListenedMs 0 mean the save carries no listening-session work.
// DeviceID is display and merge data only; it never decides what the person may do.
type Update struct {
	UserID, BookID         string
	PositionMs, DurationMs int64
	Finished               bool
	ClientUpdatedAt        int64
	DeviceID, DeviceLabel  string
	// Force makes an explicit act win over a stored newer value (the person chose "continue here").
	Force      bool
	Speed      float64
	ListenedMs int64
}

// Result reports whether a save changed the stored row, and the row as stored afterwards.
type Result struct {
	Applied  bool
	Progress Progress
}

// Service reads and writes progress, sessions and bookmarks.
type Service struct {
	st  *db.Store
	now func() time.Time
}

// New returns a Service on st; a nil now means time.Now.
func New(st *db.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{st: st, now: now}
}

func (s *Service) nowMs() int64 { return s.now().UnixMilli() }

// Save stores a progress update if it is newer than the stored one, and extends or opens the listening
// session it reports, in one transaction. A save that is not newer is not an error: Applied is false and
// Progress is the stored row.
func (s *Service) Save(ctx context.Context, u Update) (Result, error) { return s.save(ctx, u, false) }

// SetFinished marks the book finished (position = duration) or unfinished (position 0, so "Listen again"
// restarts), as an explicit server act that wins over any stored value.
func (s *Service) SetFinished(ctx context.Context, userID, bookID string, finished bool, durationMs int64) (Result, error) {
	u := Update{UserID: userID, BookID: bookID, DurationMs: durationMs, Finished: finished, Force: true,
		DeviceID: ServerDevice, ClientUpdatedAt: s.nowMs()}
	if finished {
		u.PositionMs = durationMs
	}
	return s.save(ctx, u, true)
}

func (s *Service) save(ctx context.Context, u Update, internal bool) (Result, error) {
	switch {
	case u.UserID == "":
		return Result{}, &InvalidError{"user_id"}
	case u.BookID == "":
		return Result{}, &InvalidError{"book_id"}
	case !(internal && u.DeviceID == ServerDevice) && !deviceIDRE.MatchString(u.DeviceID):
		return Result{}, &InvalidError{"device_id"}
	case u.PositionMs < 0:
		return Result{}, &InvalidError{"position_ms"}
	case u.DurationMs < 0:
		return Result{}, &InvalidError{"duration_ms"}
	case u.ClientUpdatedAt <= 0:
		return Result{}, &InvalidError{"client_updated_at"}
	case u.ListenedMs < 0:
		return Result{}, &InvalidError{"listened_ms"}
	case u.ListenedMs > 0 && !(u.Speed >= 0.5 && u.Speed <= 3.0):
		return Result{}, &InvalidError{"speed"}
	}
	if u.DurationMs > 0 && u.PositionMs > u.DurationMs {
		u.PositionMs = u.DurationMs
	}
	u.DeviceLabel = cleanLabel(u.DeviceLabel)
	nowMs := s.nowMs()
	c := min(u.ClientUpdatedAt, nowMs+MaxAheadMs)

	var res Result
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if u.Force {
			var stored int64
			switch err := tx.QueryRowContext(ctx, `SELECT client_updated_at FROM progress WHERE user_id = ? AND book_id = ?`,
				u.UserID, u.BookID).Scan(&stored); {
			case err == nil:
				c = max(c, nowMs, stored+1)
			case errors.Is(err, sql.ErrNoRows):
				c = max(c, nowMs)
			default:
				return err
			}
		}
		var finishedAt any
		if u.Finished {
			finishedAt = nowMs
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
VALUES (?,?,?,?,?,?,?,?,?)
ON CONFLICT(user_id, book_id) DO UPDATE SET position_ms = excluded.position_ms, duration_ms = excluded.duration_ms,
  finished_at = CASE WHEN excluded.finished_at IS NULL THEN NULL ELSE COALESCE(progress.finished_at, excluded.finished_at) END,
  client_updated_at = excluded.client_updated_at, server_updated_at = excluded.server_updated_at,
  device_id = excluded.device_id, device_label = excluded.device_label
WHERE excluded.client_updated_at > progress.client_updated_at`,
			u.UserID, u.BookID, u.PositionMs, u.DurationMs, finishedAt, c, nowMs, u.DeviceID, u.DeviceLabel)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		res.Applied = n == 1
		if err := recordSession(ctx, tx, u, c); err != nil {
			return err
		}
		res.Progress, err = scanProgress(tx.QueryRowContext(ctx, selectProgress+` WHERE user_id = ? AND book_id = ?`, u.UserID, u.BookID))
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return Result{}, ErrNotFound
	}
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

const selectProgress = `SELECT user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label FROM progress`

type rowScanner interface{ Scan(dest ...any) error }

func scanProgress(r rowScanner) (Progress, error) {
	var p Progress
	var fin sql.NullInt64
	err := r.Scan(&p.UserID, &p.BookID, &p.PositionMs, &p.DurationMs, &fin, &p.ClientUpdatedAt, &p.ServerUpdatedAt, &p.DeviceID, &p.DeviceLabel)
	p.FinishedAt = fin.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, ErrNotFound
	}
	return p, err
}

// Get returns the person's progress on one book, or ErrNotFound.
func (s *Service) Get(ctx context.Context, userID, bookID string) (Progress, error) {
	return scanProgress(s.st.Reader().QueryRowContext(ctx, selectProgress+` WHERE user_id = ? AND book_id = ?`, userID, bookID))
}

// ForBooks returns the person's progress on the given books, keyed by book id; books without progress are absent.
func (s *Service) ForBooks(ctx context.Context, userID string, bookIDs []string) (map[string]Progress, error) {
	out := make(map[string]Progress, len(bookIDs))
	for len(bookIDs) > 0 {
		n := min(len(bookIDs), forBooksChunk)
		chunk := bookIDs[:n]
		bookIDs = bookIDs[n:]
		args := make([]any, 0, n+1)
		args = append(args, userID)
		for _, id := range chunk {
			args = append(args, id)
		}
		rows, err := s.st.Reader().QueryContext(ctx, selectProgress+` WHERE user_id = ? AND book_id IN (?`+strings.Repeat(",?", n-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			p, err := scanProgress(rows)
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

// cleanLabel drops control characters, trims space and cuts to 64 bytes on a rune boundary.
func cleanLabel(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "")))
	if len(s) > maxLabelBytes {
		s = s[:maxLabelBytes]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

func roundPct(v float64) int64 { return int64(math.Round(v * 100)) }
