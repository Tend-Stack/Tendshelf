// Package library is the repository for libraries, folders, books, audio files and chapters: the
// reads and writes every other service uses. No HTTP and no ffmpeg here.
//
// Reads go through the query-only reader pool; writes go through Update, which runs one
// transaction on the single writer. The package imports only internal/db. Times are Unix
// milliseconds, durations are milliseconds, IDs come from db.NewID (prefixes lib, lf, bk, af).
//
// Unknown values are stored as NULL (numbers, optional text) or an empty string (required text); the Go
// types use the zero value for "unknown": 0 for PublishedYear and MissingSince, "" for CoverID,
// RefusedReason and SHA256, nil for ProbeJSON.
//
// People, series, labels and metadata_values are written in details.go (P1a-3); covers in
// covers.go (P1a-4).
package library

import (
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

var (
	// ErrNotFound means the row does not exist.
	ErrNotFound = errors.New("library: not found")
	// ErrInvalid means a value failed validation; the wrapped text names the field.
	ErrInvalid = errors.New("library: invalid value")
	// ErrFolderOverlap means a folder equals, contains or sits inside an existing folder.
	ErrFolderOverlap = errors.New("library: folder overlaps an existing folder")
	// ErrConflict means a unique value (a path) is already used by another row.
	ErrConflict = errors.New("library: already exists")
)

// Why a book is refused. A refused book is listed but never played.
const (
	RefusedDRM         = "drm"
	RefusedUnsupported = "unsupported"
	RefusedDamaged     = "damaged"
	RefusedNoAudio     = "no_audio"
)

// Where a chapter list came from.
const (
	ChapterFile    = "file"
	ChapterFiles   = "files"
	ChapterSidecar = "sidecar"
	ChapterOnline  = "online"
	ChapterUser    = "user"
)

// Library is a named set of folders scanned together.
type Library struct {
	ID              string
	Name            string
	ScanIntervalMin int // 0 = scan only on request
	CreatedAt       int64
	AutoLookupAt    int64 // when "look up details online for new books" was turned on; 0 = off
	UploadFolderID  string // the folder that receives uploads (decision 0015); "" = uploads off
}

// Folder is one directory the owner added to a library.
type Folder struct {
	ID        string
	LibraryID string
	Path      string // absolute and cleaned
	CreatedAt int64
}

// Book is one listenable work: a folder of parts or a single file.
type Book struct {
	ID, LibraryID, FolderID, RootPath                            string
	Title, SortTitle, Subtitle, Description, Publisher, Language string
	ISBN, ASIN                                                   string
	PublishedYear                                                int
	Abridged, Dramatized, Explicit                               bool
	DurationMs                                                   int64
	CoverID                                                      string
	RefusedReason                                                string
	AddedAt, UpdatedAt                                           int64
	MissingSince                                                 int64 // 0 = present
}

// AudioFile is one file of a book. OffsetMs places it on the book's timeline.
type AudioFile struct {
	ID, BookID, Path        string
	Idx                     int
	Size, MtimeNs           int64
	QuickHash, SHA256       string
	Container, Codec        string
	Bitrate                 int64
	Channels, SampleRate    int
	DurationMs, OffsetMs    int64
	Encrypted               bool
	ProbeJSON               []byte
	ProbeError              string // media error code when the file could not be read; "" = none
	MissingSince, UpdatedAt int64
}

// Chapter is one entry of a book's chapter list, on the book's timeline.
type Chapter struct {
	Idx            int
	StartMs, EndMs int64
	Title, Source  string
}

// Repo reads and writes the library tables.
type Repo struct {
	st  *db.Store
	now func() time.Time
}

// New returns a Repo on st.
func New(st *db.Store) *Repo { return &Repo{st: st, now: time.Now} }

func (r *Repo) nowMs() int64 { return r.now().UnixMilli() }
