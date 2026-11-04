package ebook

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// Service owns the writes of the ebook tables: the scanner sink, the ingest job and uploads.
type Service struct {
	db      *db.Store
	lib     *library.Repo
	runner  *jobs.Runner
	dataDir string
	log     *slog.Logger
	store   *Store
	limits  Limits
	now     func() time.Time
	created []func(context.Context, []string) // told about new text books (OnBookCreated)
}

// New returns the ebook service. dataDir is the Tendshelf data folder: uploaded bytes live in
// <dataDir>/ebooks and documents in <dataDir>/text.
func New(st *db.Store, lib *library.Repo, runner *jobs.Runner, dataDir string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{db: st, lib: lib, runner: runner, dataDir: dataDir, log: log, store: NewStore(st),
		limits: DefaultLimits(), now: time.Now}
}

// Store returns the read side of the ebook tables.
func (s *Service) Store() *Store { return s.store }

func (s *Service) ebooksDir() string   { return filepath.Join(s.dataDir, "ebooks") }
func (s *Service) incomingDir() string { return filepath.Join(s.ebooksDir(), ".incoming") }
func (s *Service) textDir() string     { return filepath.Join(s.dataDir, "text") }

// storedPath is where an uploaded file's bytes live.
func (s *Service) storedPath(sha, ext string) string {
	return filepath.Join(s.ebooksDir(), sha+"."+ext)
}

// ScanSink is how the scanner hands over the ebooks it found.
func (s *Service) ScanSink() scan.EbookSink {
	return func(ctx context.Context, es scan.EbookScan) {
		if err := s.Reconcile(ctx, es); err != nil {
			s.log.Warn("ebooks: reconcile failed", "library", es.LibraryID, "err", err)
		}
	}
}

// Finisher catches up on pending files after every scan.
func (s *Service) Finisher() scan.Finisher {
	return func(ctx context.Context, _ string) {
		if err := s.PairPending(ctx); err != nil {
			s.log.Warn("ebooks: pairing failed", "err", err)
		}
		if err := s.ensureBooks(ctx); err != nil {
			s.log.Warn("ebooks: could not make books", "err", err)
		}
		if err := s.mergePending(ctx, "", ""); err != nil {
			s.log.Warn("ebooks: merging failed", "err", err)
		}
		if err := s.EnqueuePending(ctx); err != nil {
			s.log.Warn("ebooks: could not queue reads", "err", err)
		}
	}
}

// removeIncoming deletes upload temp files a crash left behind.
func (s *Service) removeIncoming() {
	ents, err := os.ReadDir(s.incomingDir())
	if err != nil {
		return
	}
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".part") {
			_ = os.Remove(filepath.Join(s.incomingDir(), e.Name()))
		}
	}
}
