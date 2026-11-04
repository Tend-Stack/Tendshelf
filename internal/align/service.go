package align

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"

	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
)

// KindBook is the job kind that aligns one edition with its audiobook.
const KindBook = "align.book"

// Errors from Start and the other service calls, each with a plain meaning for the interface.
var (
	ErrEditionNotReady  = errors.New("align: the ebook is not ready or not paired with this book")
	ErrRuntimeMissing   = errors.New("align: no usable alignment runtime for this language")
	ErrAudioUnavailable = errors.New("align: the audio files are missing, unreadable or copy-protected")
	ErrNotFound         = errors.New("align: alignment not found")
	ErrNotHeld          = errors.New("align: the alignment is not waiting for a decision")
)

// Runtimes is what the service needs from the alignment runtime. A small adapter over alignrt.Components and
// alignrt.Manager implements it (wiring is in cmd/tendshelf).
type Runtimes interface {
	Usable(ctx context.Context, lang string) (alignrt.Component, bool, error)
	Emitter(ctx context.Context, lang string) (alignrt.Emitter, error)
}

// Decoder decodes audio to 16 kHz mono s16le; media.Tools implements it.
type Decoder interface {
	PCM16k(ctx context.Context, src string, fromMs, durMs int64, w io.Writer) error
}

// Deps are the service's collaborators. Params, Hash and Hook are optional (tests set them).
type Deps struct {
	Store         *db.Store
	Library       *library.Repo
	Ebooks        *ebook.Store
	Runner        *jobs.Runner
	Runtimes      Runtimes
	Decoder       Decoder
	DataDir       string
	CacheMaxBytes int64
	Log           *slog.Logger

	Params Params                                         // zero means DefaultParams
	Hash   func(path string) (string, int64, error)       // quick hash of an audio file; default media.QuickHash
	Hook   func(ctx context.Context, stage string, n int) // test seam, see job.go
}

// Service starts, watches and ends alignment jobs.
type Service struct {
	d     Deps
	store *Store
	cache Cache
}

// NewService returns a Service. Register must be called before the runner starts.
func NewService(d Deps) *Service {
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Hash == nil {
		d.Hash = media.QuickHash
	}
	return &Service{d: d, store: NewStore(d.Store), cache: Cache{Root: filepath.Join(d.DataDir, "cache", "align")}}
}

// Store gives access to the alignment rows.
func (s *Service) Store() *Store { return s.store }

func (s *Service) textDir() string   { return filepath.Join(s.d.DataDir, "text") }
func (s *Service) timingDir() string { return filepath.Join(s.d.DataDir, "align") }

// Register adds align.book to the runner. It never holds on recovery: a restart resumes from the emissions
// cache and the finished chapters.
func (s *Service) Register(r *jobs.Runner) error { return r.Register(s.bookKind()) }

func (s *Service) bookKind() jobs.Kind {
	return jobs.Kind{Name: KindBook, Concurrency: 1, MaxAttempts: 3, HoldOnRecovery: false, Handler: s.handle}
}

// payload is the job payload.
type payload struct {
	AlignmentID string `json:"alignmentId"`
}

// langOf reduces a language tag to its primary subtag: en-GB becomes en.
func langOf(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

// audioSet loads a book's audio files and checks them. The key is the SHA-256 of their quick hashes in play
// order, so a replaced file changes it.
func (s *Service) audioSet(ctx context.Context, bookID string) ([]library.AudioFile, string, error) {
	files, err := s.d.Library.AudioFiles(ctx, bookID)
	if err != nil {
		return nil, "", err
	}
	if len(files) == 0 {
		return nil, "", ErrAudioUnavailable
	}
	hashes := make([]string, len(files))
	for i, f := range files {
		if f.MissingSince != 0 || f.Encrypted || f.ProbeError != "" || f.DurationMs <= 0 || len(f.QuickHash) != 64 ||
			media.Classify(f.Path) == media.KindProtected {
			return nil, "", ErrAudioUnavailable
		}
		hashes[i] = f.QuickHash
	}
	sum := sha256.Sum256([]byte(strings.Join(hashes, "\n")))
	return files, hex.EncodeToString(sum[:]), nil
}

// readyEdition returns the edition if it is the current edition of a ready ebook file paired with the book.
func (s *Service) readyEdition(ctx context.Context, editionID, bookID string) (ebook.Edition, error) {
	ed, ok, err := s.d.Ebooks.Edition(ctx, editionID)
	if err != nil {
		return ebook.Edition{}, err
	}
	if !ok {
		return ebook.Edition{}, ErrEditionNotReady
	}
	f, ok, err := s.d.Ebooks.File(ctx, ed.EbookFileID)
	if err != nil {
		return ebook.Edition{}, err
	}
	if !ok || f.State != "ready" || f.BookID != bookID {
		return ebook.Edition{}, ErrEditionNotReady
	}
	cur, ok, err := s.d.Ebooks.CurrentEdition(ctx, f.ID)
	if err != nil {
		return ebook.Edition{}, err
	}
	if !ok || cur.ID != ed.ID {
		return ebook.Edition{}, ErrEditionNotReady
	}
	return ed, nil
}

// editionLang is the language the narration is aligned in: the edition's, else the book's.
func (s *Service) editionLang(ctx context.Context, ed ebook.Edition, bookID string) string {
	if l := langOf(ed.Language); l != "" {
		return l
	}
	if b, err := s.d.Library.Book(ctx, bookID); err == nil {
		return langOf(b.Language)
	}
	return ""
}

// Start queues an alignment of the edition with the book's audio. An active one is returned as it is, and so
// is a finished one made from the same text, audio, model and recipe.
func (s *Service) Start(ctx context.Context, editionID, bookID, userID string) (Alignment, error) {
	if a, ok, err := s.store.Active(ctx, editionID); err != nil || ok {
		return a, err
	}
	ed, err := s.readyEdition(ctx, editionID, bookID)
	if err != nil {
		return Alignment{}, err
	}
	comp, usable, err := s.d.Runtimes.Usable(ctx, s.editionLang(ctx, ed, bookID))
	if err != nil {
		return Alignment{}, err
	}
	if !usable {
		return Alignment{}, ErrRuntimeMissing
	}
	_, key, err := s.audioSet(ctx, bookID)
	if err != nil {
		return Alignment{}, err
	}
	if a, ok, err := s.store.Latest(ctx, editionID); err != nil {
		return Alignment{}, err
	} else if ok && a.DocSHA256 == ed.DocSHA256 && a.AudioKey == key && a.ModelSHA256 == comp.ModelSHA256 && a.Recipe == RecipeAlign {
		return a, nil
	}
	row := Alignment{
		ID: db.NewID("al"), EditionID: editionID, BookID: bookID, Source: "model", Recipe: RecipeAlign,
		RuntimeID: comp.ID, ModelSHA256: comp.ModelSHA256, AudioKey: key, DocSHA256: ed.DocSHA256, State: StateQueued,
	}
	if err := s.store.Insert(ctx, row); err != nil {
		// A second request that lost the race for the one active slot gets the winner.
		if a, ok, aerr := s.store.Active(ctx, editionID); aerr == nil && ok {
			return a, nil
		}
		return Alignment{}, err
	}
	jobID, err := s.d.Runner.Enqueue(ctx, KindBook, payload{AlignmentID: row.ID}, jobs.EnqueueOptions{CreatedBy: userID})
	if err != nil {
		_, _ = s.store.End(ctx, row.ID, StateFailed, "enqueue_failed")
		return Alignment{}, err
	}
	// The handler may already have claimed the row (SetJob is first-wins), so read it back.
	if _, err := s.store.SetJob(ctx, row.ID, jobID); err != nil {
		return Alignment{}, err
	}
	out, _, err := s.store.Get(ctx, row.ID)
	return out, err
}

// Cancel ends an active alignment: its job is cancelled and the row becomes cancelled, so a handler that is
// still running can write nothing more. Discard is the same call for a held one.
func (s *Service) Cancel(ctx context.Context, id string) error {
	a, ok, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if a.JobID != "" {
		if err := s.d.Runner.Cancel(ctx, a.JobID); err != nil && !errors.Is(err, jobs.ErrNotFound) && !errors.Is(err, jobs.ErrAlreadyFinished) {
			return err
		}
	}
	_, err = s.store.End(ctx, id, StateCancelled, "")
	return err
}

// Discard is Cancel under the name a person's "Discard" button uses.
func (s *Service) Discard(ctx context.Context, id string) error { return s.Cancel(ctx, id) }

// Resume lets a held alignment continue; the job adopts the current audio and model when it starts.
func (s *Service) Resume(ctx context.Context, id string) error {
	a, ok, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if a.State != StateHeld || a.JobID == "" {
		return ErrNotHeld
	}
	if err := s.d.Runner.Resume(ctx, a.JobID); err != nil {
		if errors.Is(err, jobs.ErrNotHeld) {
			return ErrNotHeld
		}
		return err
	}
	return nil
}

// Status is what the Text tab shows for an edition.
type Status struct {
	Alignment
	Job      *jobs.Job
	Chapters []ChapterRow
	Stale    bool // the book's audio is no longer the audio this alignment was made from
}

// Status returns the newest alignment of the edition; ok is false when it has none.
func (s *Service) Status(ctx context.Context, editionID string) (Status, bool, error) {
	a, ok, err := s.store.Newest(ctx, editionID)
	if err != nil || !ok {
		return Status{}, false, err
	}
	st := Status{Alignment: a}
	if a.JobID != "" {
		j, err := s.d.Runner.Get(ctx, a.JobID)
		switch {
		case err == nil:
			st.Job = &j
		case !errors.Is(err, jobs.ErrNotFound):
			return Status{}, false, err
		}
	}
	if st.Chapters, err = s.store.Chapters(ctx, a.ID); err != nil {
		return Status{}, false, err
	}
	if _, key, err := s.audioSet(ctx, a.BookID); err == nil {
		st.Stale = key != a.AudioKey
	}
	return st, true, nil
}

// Reconcile brings active rows in line with their jobs after a restart: a failed job fails the row with its
// code, a cancelled, finished or missing job cancels it, and a queued job means the row is queued again.
func (s *Service) Reconcile(ctx context.Context) error {
	rows, err := s.store.ActiveAll(ctx)
	if err != nil {
		return err
	}
	for _, a := range rows {
		if a.JobID == "" {
			if _, err := s.store.End(ctx, a.ID, StateCancelled, ""); err != nil {
				return err
			}
			continue
		}
		j, err := s.d.Runner.Get(ctx, a.JobID)
		state, code := "", ""
		switch {
		case errors.Is(err, jobs.ErrNotFound):
			state = StateCancelled
		case err != nil:
			return err
		case j.State == jobs.StateFailed:
			state, code = StateFailed, j.ErrorCode
		case j.State == jobs.StateCancelled, j.State == jobs.StateDone:
			state = StateCancelled
		case j.State == jobs.StateQueued && a.State != StateQueued:
			state = StateQueued
		}
		if state == "" {
			continue
		}
		if _, err := s.store.End(ctx, a.ID, state, code); err != nil {
			return err
		}
	}
	return nil
}
