package metadata

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Kind is the job that refreshes one book's local details.
const Kind = "metadata.local"

// Payload is the payload of a metadata.local job.
type Payload struct {
	BookID string `json:"book_id"`
}

// Register adds the metadata.local, metadata.online, metadata.online_bulk and metadata.online_author and chapter_lookup job kinds to the runner. Its handler writes only inside a
// transaction that begins with the job's fence check.
func (s *Service) Register(r *jobs.Runner) error {
	if err := r.Register(jobs.Kind{Name: Kind, Concurrency: 1, MaxAttempts: 2, Handler: s.handle}); err != nil {
		return err
	}
	// Lookups run once: a failed one is started again by a person, never behind their back.
	if err := r.Register(jobs.Kind{Name: KindOnline, Concurrency: 1, MaxAttempts: 1, Handler: s.handleLookup}); err != nil {
		return err
	}
	// A bulk lookup resumes from its markers when interrupted, so it may run again after a failure.
	if err := r.Register(jobs.Kind{Name: KindOnlineBulk, Concurrency: 1, MaxAttempts: 3, Handler: s.handleBulk}); err != nil {
		return err
	}
	if err := r.Register(jobs.Kind{Name: KindAuthor, Concurrency: 1, MaxAttempts: 1, Handler: s.handleAuthor}); err != nil {
		return err
	}
	return r.Register(jobs.Kind{Name: KindChapterLookup, Concurrency: 1, MaxAttempts: 1, Handler: s.handleChapterLookup})
}

func (s *Service) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var p Payload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.BookID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	changed, err := s.Local(ctx, p.BookID, t.Fence)
	if err != nil {
		return nil, err
	}
	if changed {
		for _, f := range s.onLocal {
			f(ctx, p.BookID)
		}
	}
	s.maybeAuto(ctx, p.BookID)
	return map[string]bool{"changed": changed}, nil
}

// Follower returns the function to hand to the scanner's OnBookSettled: it queues one metadata.local
// job for every settled book that has none waiting. It logs its own errors; a failed enqueue does
// not stop the other books.
func (s *Service) Follower() func(context.Context, []string) {
	return func(ctx context.Context, bookIDs []string) {
		if _, err := s.enqueue(ctx, bookIDs); err != nil {
			s.log.Warn("queue details jobs", "err", err)
		}
	}
}

// Finisher returns the function to hand to the scanner's OnScanFinished. After a scan it compares
// each book's sidecar files with the signature stored when they were last read, and queues a
// metadata.local job for every book where they differ or none is stored, even when the book's
// audio files did not change. It logs its own errors and never stops at one book.
func (s *Service) Finisher() func(context.Context, string) {
	return func(ctx context.Context, libraryID string) {
		books, err := s.repo.SidecarCandidates(ctx, libraryID)
		if err != nil {
			s.log.Warn("list books for sidecar check", "library", libraryID, "err", err)
			return
		}
		var ids []string
		for _, b := range books {
			if ctx.Err() != nil {
				return
			}
			var sig string
			if b.Kind == library.BookText {
				sig = sidecarSignature(textSidecars(b.RootPath))
			} else {
				sig = sidecarSignature(findSidecars(b.RootPath, b.Single))
			}
			if !b.Checked || b.Signature != sig {
				ids = append(ids, b.ID)
			}
		}
		if _, err := s.enqueue(ctx, ids); err != nil {
			s.log.Warn("queue details jobs after sidecar check", "library", libraryID, "err", err)
		}
	}
}

// enqueue queues a job per distinct id that has none waiting and returns how many it queued. It
// tries every id and returns the first error.
func (s *Service) enqueue(ctx context.Context, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	active, err := s.jobs.ActiveKeys(ctx, Kind, "book_id")
	if err != nil {
		return 0, fmt.Errorf("active details jobs: %w", err)
	}
	var first error
	n := 0
	for _, id := range ids {
		if active[id] {
			continue
		}
		active[id] = true
		if _, err := s.jobs.Enqueue(ctx, Kind, Payload{BookID: id}, jobs.EnqueueOptions{}); err != nil {
			s.log.Warn("queue details job", "book", id, "err", err)
			if first == nil {
				first = fmt.Errorf("queue details job: %w", err)
			}
			continue
		}
		n++
	}
	return n, first
}

// EnqueueLibrary queues a details job for every book of the library and returns how many it queued.
func (s *Service) EnqueueLibrary(ctx context.Context, libraryID string) (int, error) {
	folders, err := s.repo.Folders(ctx, libraryID)
	if err != nil {
		return 0, err
	}
	if len(folders) == 0 {
		if _, err := s.repo.Library(ctx, libraryID); err != nil {
			return 0, err
		}
	}
	var ids []string
	for _, f := range folders {
		books, err := s.repo.BooksInFolder(ctx, f.ID)
		if err != nil {
			return 0, err
		}
		for _, b := range books {
			ids = append(ids, b.ID)
		}
	}
	return s.enqueue(ctx, ids)
}
