// Package upload adds books by upload (decision 0015): upload sessions and the chunk store under
// <data>/uploads/incoming/, the upload.place job that builds a book in a hidden folder inside the
// library folder the owner chose and publishes it by one rename, the upload_paths ledger of every
// path it created, restart recovery, the expiry sweep and "Remove this book".
//
// The invariants (docs/plans/upload-books.md, U1):
//   - the only writes into a library folder are in place.go and remove.go, and only below the
//     chosen upload folder (every path is checked with within before use);
//   - a ledger row is written before any path is created, and nothing is overwritten, renamed over
//     or deleted unless this package created it (the ledger says so);
//   - the resume offset advances only after the chunk is synced.
package upload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// KindPlace is the job that moves a received upload into the library folder.
const KindPlace = "upload.place"

// Upload states (uploads.state).
const (
	StateReceiving = "receiving"
	StateQueued    = "queued"
	StatePlacing   = "placing"
	StatePlaced    = "placed"
	StateRefused   = "refused"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateExpired   = "expired"
)

// Config holds the limits. Zero values take the defaults in the comments.
type Config struct {
	DataDir      string
	ChunkSize    int64 // 8 MiB
	MaxFile      int64 // 20 GiB
	MaxBook      int64 // 50 GiB
	MaxFiles     int   // 1000
	MaxReceiving int   // 3 receiving uploads per person
	Margin       int64 // 1 GiB of free space kept beyond what an upload needs
	// PerPerson caps the bytes one person may hold in staging across all their unfinished uploads,
	// failed ones included (60 GiB).
	PerPerson int64
	Now       func() time.Time
}

func (c *Config) defaults() {
	if c.ChunkSize <= 0 {
		c.ChunkSize = 8 << 20
	}
	if c.MaxFile <= 0 {
		c.MaxFile = 20 << 30
	}
	if c.MaxBook <= 0 {
		c.MaxBook = 50 << 30
	}
	if c.MaxFiles <= 0 {
		c.MaxFiles = 1000
	}
	if c.MaxReceiving <= 0 {
		c.MaxReceiving = 3
	}
	if c.PerPerson <= 0 {
		c.PerPerson = 60 << 30
	}
	if c.Margin < 0 {
		c.Margin = 0
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Error is a refusal with the HTTP status, a stable code and a plain-words message.
type Error struct {
	Status  int
	Code    string
	Message string
	Field   string // the offending path, when there is one
	Offset  int64  // the server's offset, for wrong_offset
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func fail(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

var errNotFound = fail(404, "not_found", "That upload does not exist.")

// Service owns the uploads.
type Service struct {
	st    *db.Store
	repo  *library.Repo
	tools media.Tools
	enq   scan.Enqueuer
	log   *slog.Logger
	cfg   Config

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per-upload chunk locks

	// Seams for tests.
	free    func(dir string) (uint64, error)
	writeAt func(f *os.File, b []byte, off int64) error
	crash   func(point string) error // returns an error to simulate a crash at a named point
}

// New returns the service. tools may lack ffprobe: placement then fails with ffprobe_missing.
func New(cfg Config, st *db.Store, repo *library.Repo, tools media.Tools, enq scan.Enqueuer, log *slog.Logger) *Service {
	cfg.defaults()
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, repo: repo, tools: tools, enq: enq, log: log, cfg: cfg,
		locks: map[string]*sync.Mutex{},
		free:  media.FreeBytes,
		writeAt: func(f *os.File, b []byte, off int64) error {
			_, err := f.WriteAt(b, off)
			return err
		},
		crash: func(string) error { return nil },
	}
}

// Register adds the upload.place job kind.
func (s *Service) Register(r *jobs.Runner) error {
	return r.Register(jobs.Kind{Name: KindPlace, Concurrency: 1, MaxAttempts: 3, Handler: s.handlePlace})
}

func (s *Service) nowMs() int64 { return s.cfg.Now().UnixMilli() }

func (s *Service) lock(id string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.locks[id]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	return m
}

func (s *Service) forget(id string) {
	s.mu.Lock()
	delete(s.locks, id)
	s.mu.Unlock()
}

// incomingDir is <data>/uploads/incoming, the parent of every staging folder.
func (s *Service) incomingDir() string { return filepath.Join(s.cfg.DataDir, "uploads", "incoming") }

var idRe = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// stagingDir is the staging folder of one upload. An id that is not plain letters, digits and
// underscores yields "", so a crafted id can never name another folder.
func (s *Service) stagingDir(uploadID string) string {
	if !idRe.MatchString(uploadID) {
		return ""
	}
	return filepath.Join(s.incomingDir(), uploadID)
}

func (s *Service) partPath(uploadID, fileID string) string {
	d := s.stagingDir(uploadID)
	if d == "" || !idRe.MatchString(fileID) {
		return ""
	}
	return filepath.Join(d, fileID+".part")
}

// removeStaging deletes one upload's staging folder. It is one of the two RemoveAll calls the
// package makes (the other is the hidden build folder), and only after Lstat proves the path is a
// real directory directly inside the incoming folder.
func (s *Service) removeStaging(uploadID string) {
	d := s.stagingDir(uploadID)
	if d == "" {
		return
	}
	fi, err := os.Lstat(d)
	if err != nil || !fi.IsDir() {
		return
	}
	if err := os.RemoveAll(d); err != nil {
		s.log.Warn("upload: cannot remove staging", "upload", uploadID, "err", err)
	}
}

// uploadRow is one uploads row.
type uploadRow struct {
	ID, LibraryID, FolderID, CreatedBy, Label, State string
	BytesTotal, BytesReceived                        int64
	JobID, ScanJobID, BookPath, TempPath             string
	RefusedFile, ErrorCode                           string
	Skipped                                          int
	CreatedAt, UpdatedAt                             int64
}

const uploadCols = `id, library_id, folder_id, created_by, label, state, bytes_total, bytes_received,
  COALESCE(job_id,''), COALESCE(scan_job_id,''), COALESCE(book_path,''), COALESCE(temp_path,''),
  COALESCE(refused_file,''), COALESCE(error_code,''), skipped, created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanUpload(r rowScanner) (uploadRow, error) {
	var u uploadRow
	err := r.Scan(&u.ID, &u.LibraryID, &u.FolderID, &u.CreatedBy, &u.Label, &u.State, &u.BytesTotal, &u.BytesReceived,
		&u.JobID, &u.ScanJobID, &u.BookPath, &u.TempPath, &u.RefusedFile, &u.ErrorCode, &u.Skipped, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// fileRow is one upload_files row.
type fileRow struct {
	ID, UploadID, RelPath, SHA256 string
	Idx                           int
	Size, ModifiedMs              int64
	BytesReceived                 int64
}

func scanFile(r rowScanner) (fileRow, error) {
	var f fileRow
	err := r.Scan(&f.ID, &f.UploadID, &f.Idx, &f.RelPath, &f.Size, &f.ModifiedMs, &f.SHA256, &f.BytesReceived)
	return f, err
}

const fileCols = `id, upload_id, idx, rel_path, size, modified_ms, COALESCE(sha256,''), bytes_received`

func (s *Service) loadUpload(ctx context.Context, id string) (uploadRow, error) {
	u, err := scanUpload(s.st.Reader().QueryRowContext(ctx, `SELECT `+uploadCols+` FROM uploads WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return u, errNotFound
	}
	return u, err
}

// loadOwn is loadUpload for a request: only the person who created the upload may see it.
func (s *Service) loadOwn(ctx context.Context, userID, id string) (uploadRow, error) {
	u, err := s.loadUpload(ctx, id)
	if err != nil {
		return u, err
	}
	if u.CreatedBy != userID {
		return uploadRow{}, errNotFound
	}
	return u, nil
}

func (s *Service) loadFiles(ctx context.Context, uploadID string) ([]fileRow, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM upload_files WHERE upload_id = ? ORDER BY idx`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fileRow
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// guard is a job's fence check; nil is allowed (requests have no fence).
type guard func(context.Context, *sql.Tx) error

// write runs fn in one write transaction that starts with g.
func (s *Service) write(ctx context.Context, g guard, fn func(tx *sql.Tx) error) error {
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if g != nil {
			if err := g(ctx, tx); err != nil {
				return err
			}
		}
		return fn(tx)
	})
}

// setState moves an upload to a state when it is in one of from (any state when from is empty).
func (s *Service) setState(ctx context.Context, g guard, id, to, errorCode string, from ...string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error {
		q := `UPDATE uploads SET state = ?, error_code = ?, updated_at = ? WHERE id = ?`
		args := []any{to, nullIfEmpty(errorCode), s.nowMs(), id}
		if len(from) > 0 {
			q += ` AND state IN (?` + repeat(len(from)-1) + `)`
			for _, f := range from {
				args = append(args, f)
			}
		}
		res, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fail(409, "wrong_state", "That upload is not in a state that allows this.")
		}
		return nil
	})
}

func repeat(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += ",?"
	}
	return s
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) audit(ctx context.Context, tx *sql.Tx, user, action, target string, detail any) error {
	return db.Audit(ctx, tx, db.AuditEntry{At: s.nowMs(), UserID: user, Action: action, Target: target, Detail: detail})
}

// FileState is one file of an upload as the client needs it.
type FileState struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
}

// View is an upload as the routes show it.
type View struct {
	ID        string      `json:"id"`
	State     string      `json:"state,omitempty"`
	ChunkSize int64       `json:"chunk_size"`
	Files     []FileState `json:"files"`
	Skipped   int         `json:"skipped"`
	JobID     string      `json:"job_id,omitempty"`
	Label     string      `json:"label,omitempty"`
	BookPath  string      `json:"book_path,omitempty"`
	Refused   string      `json:"refused_file,omitempty"`
	ErrorCode string      `json:"error_code,omitempty"`
	// ScanJobID is the scoped import after placement; the page reads it, the uploader does not.
	ScanJobID string `json:"scan_job_id,omitempty"`
	LibraryID string `json:"library_id,omitempty"`
}

func (s *Service) view(u uploadRow, files []fileRow) View {
	v := View{ID: u.ID, State: u.State, ChunkSize: s.cfg.ChunkSize, Skipped: u.Skipped, JobID: u.JobID,
		Label: u.Label, BookPath: u.BookPath, Refused: u.RefusedFile, ErrorCode: u.ErrorCode, ScanJobID: u.ScanJobID, LibraryID: u.LibraryID, Files: []FileState{}}
	for _, f := range files {
		v.Files = append(v.Files, FileState{ID: f.ID, Path: f.RelPath, Size: f.Size, Offset: f.BytesReceived})
	}
	return v
}

// Get returns one upload of the person, with the server's offsets (resume reads them).
func (s *Service) Get(ctx context.Context, userID, id string) (View, error) {
	u, err := s.loadOwn(ctx, userID, id)
	if err != nil {
		return View{}, err
	}
	files, err := s.loadFiles(ctx, id)
	if err != nil {
		return View{}, err
	}
	return s.view(u, files), nil
}

// LibraryOf returns the library an upload of the person belongs to, for the route's authorization.
func (s *Service) LibraryOf(ctx context.Context, userID, id string) (string, error) {
	u, err := s.loadOwn(ctx, userID, id)
	return u.LibraryID, err
}

// ChunkSize is the largest piece the server accepts.
func (s *Service) ChunkSize() int64 { return s.cfg.ChunkSize }

// Cancel ends a receiving or failed upload and deletes its staged bytes.
func (s *Service) Cancel(ctx context.Context, userID, id string) error {
	if _, err := s.loadOwn(ctx, userID, id); err != nil {
		return err
	}
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	if err := s.setState(ctx, nil, id, StateCancelled, "", StateReceiving, StateFailed); err != nil {
		return fail(409, "not_cancellable", "That upload can no longer be cancelled.")
	}
	s.removeStaging(id)
	s.forget(id)
	return nil
}

// Finish starts the upload.place job once every byte has arrived. It also restarts a failed upload
// whose staged bytes are still there. It returns the job's id.
func (s *Service) Finish(ctx context.Context, userID, id string) (string, error) {
	if _, err := s.loadOwn(ctx, userID, id); err != nil {
		return "", err
	}
	l := s.lock(id)
	l.Lock()
	defer l.Unlock()
	u, err := s.loadOwn(ctx, userID, id) // read again under the lock: a cancel may have come first
	if err != nil {
		return "", err
	}
	files, err := s.loadFiles(ctx, id)
	if err != nil {
		return "", err
	}
	if u.State != StateReceiving && u.State != StateFailed {
		return "", fail(409, "not_receiving", "That upload is not waiting for files.")
	}
	for _, f := range files {
		if f.BytesReceived != f.Size {
			return "", fail(409, "incomplete", "Not every file has arrived yet.")
		}
		if u.State == StateFailed {
			if fi, err := os.Stat(s.partPath(id, f.ID)); err != nil || fi.Size() != f.Size {
				return "", fail(409, "incomplete", "The files were removed from the server. Choose them again.")
			}
		}
	}
	if err := s.setState(ctx, nil, id, StateQueued, "", u.State); err != nil {
		return "", err
	}
	job, err := s.enq.Enqueue(ctx, KindPlace, placePayload{UploadID: id}, jobs.EnqueueOptions{CreatedBy: userID})
	if err != nil {
		_ = s.setState(ctx, nil, id, u.State, u.ErrorCode, StateQueued)
		return "", fmt.Errorf("queue placement: %w", err)
	}
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE uploads SET job_id = ? WHERE id = ?`, job, id)
		return err
	})
	return job, err
}

type placePayload struct {
	UploadID string `json:"upload_id"`
}
