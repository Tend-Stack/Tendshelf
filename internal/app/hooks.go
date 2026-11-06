package app

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/backups"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/watch"
)

// deps is what features need to register their jobs and followers at start-up.
type deps struct {
	st      *db.Store
	lib     *library.Repo
	scanner *scan.Scanner
	runner  *jobs.Runner
	tools   media.Tools
	dataDir string
	version string
	log     *slog.Logger
}

// registerDetails is the hook for the book details feature: its follower and job kinds.
// It returns the service and the online client so the web handler can serve lookups and covers.
func registerDetails(d deps) (*metadata.Service, *online.Client, error) {
	svc := metadata.New(d.lib, d.runner, d.log)
	client := online.New(online.Options{
		Cache:     metadata.NewOnlineCache(d.st),
		CoverDir:  filepath.Join(d.dataDir, "cache", "online-covers"),
		UserAgent: "Tendshelf/" + d.version,
	})
	svc.SetOnline(client, d.tools, d.dataDir)
	d.scanner.OnBookSettled(svc.Follower())
	d.scanner.OnScanFinished(svc.Finisher())
	if err := svc.Register(d.runner); err != nil {
		return nil, nil, err
	}
	return svc, client, nil
}

// registerBackups is the hook for scheduled database copies: the db.backup job kind. The web handler
// shows and starts them; Run runs the schedule check once the runner is going.
func registerBackups(d deps) (*backups.Service, error) {
	svc := backups.New(d.st, d.runner, d.dataDir, nil, d.log)
	if err := svc.Register(); err != nil {
		return nil, err
	}
	return svc, nil
}

// registerEbooks is the hook for ebooks (read-along R1): the scanner hands over the ebook files it
// finds, and the ebook.ingest job reads them into documents. Call it before scanner.Register.
func registerEbooks(d deps) (*ebook.Service, error) {
	svc := ebook.New(d.st, d.lib, d.runner, d.dataDir, d.log)
	d.scanner.OnEbooks(svc.ScanSink())
	d.scanner.OnScanFinished(svc.Finisher())
	if err := svc.Register(d.runner); err != nil {
		return nil, err
	}
	return svc, nil
}

// wireTextBooks connects ebook books (decision 0018) to the details and cover jobs: a new text book gets its
// details and cover like a scanned one; an audiobook whose tags were just read is offered to the automatic merge;
// the cover job takes a picture from a book's ebook file when the book has no other.
func wireTextBooks(details *metadata.Service, covers *mediajobs.Covers, ebooks *ebook.Service, log *slog.Logger) {
	ebooks.OnBookCreated(details.Follower())
	ebooks.OnBookCreated(covers.Follower())
	details.OnLocal(func(ctx context.Context, bookID string) {
		if err := ebooks.PairBook(ctx, bookID); err != nil {
			log.Warn("ebooks: could not merge after details were read", "book", bookID, "err", err)
		}
	})
	covers.TextCover = ebooks.CoverFor
}

// registerMediaJobs is the hook for the media job kinds: cover pictures with thumbnails and
// accent colours, compatibility copies and waveform peaks. Covers and peaks follow the scanner's
// settled books. It returns the compatibility and waveform services so the web handler can request
// and serve them, plus the covers service for online cover adoption and the write-details service.
func registerMediaJobs(d deps, compatGiB int, waveforms bool) (*mediajobs.Compat, *mediajobs.Peaks, *mediajobs.Covers, *mediajobs.Writes, error) {
	if compatGiB < 1 {
		return nil, nil, nil, nil, fmt.Errorf("-compat-cache-gib must be at least 1, got %d", compatGiB)
	}
	covers := mediajobs.NewCovers(d.lib, d.tools, d.dataDir, d.runner, d.log)
	compat := mediajobs.NewCompat(d.lib, d.tools, mediajobs.CompatDir(d.dataDir), int64(compatGiB)<<30, d.runner, d.log)
	peaks := mediajobs.NewPeaks(d.lib, d.tools, mediajobs.PeaksDir(d.dataDir), d.runner, waveforms, d.log)
	d.scanner.OnBookSettled(covers.Follower())
	d.scanner.OnScanFinished(covers.Finisher())
	d.scanner.OnBookSettled(peaks.Follower())
	writes := mediajobs.NewWrites(d.lib, d.tools, d.dataDir, d.runner, d.runner, d.log)
	// After files were swapped or restored the library is scanned again: the same two calls as the
	// admin Scan button (internal/web startScan), joining a scan that is already waiting.
	writes.OnChanged = func(ctx context.Context, libraryID string) {
		if _, err := scan.Enqueue(ctx, d.runner, "", libraryID); err != nil {
			d.log.Warn("write details: could not queue a scan", "library", libraryID, "err", err)
		}
	}
	if err := mediajobs.Register(d.runner, covers, compat, peaks, writes); err != nil {
		return nil, nil, nil, nil, err
	}
	return compat, peaks, covers, writes, nil
}

// newWatcher is the hook for watch folders and the scheduled scan (S1). Both end in scan.Enqueue, the same
// call as the admin Scan button, so there is one scanner. Run it once the runner is going.
func newWatcher(d deps) *watch.Service {
	return watch.New(watch.Config{
		Source: d.lib,
		Enqueue: func(ctx context.Context, libraryID string) error {
			_, err := scan.Enqueue(ctx, d.runner, "", libraryID)
			return err
		},
		Running: func(ctx context.Context, libraryID string) (bool, error) {
			return scan.Running(ctx, d.runner, libraryID)
		},
		LastScan: func(ctx context.Context, libraryID string) (time.Time, bool, error) {
			j, ok, err := d.runner.LatestFor(ctx, scan.KindScan, "library_id", libraryID)
			if err != nil || !ok {
				return time.Time{}, false, err
			}
			switch j.State {
			case jobs.StateQueued, jobs.StateRunning, jobs.StateHeld:
				return time.Now(), true, nil // a scan is on its way: not due
			}
			return j.UpdatedAt, true, nil
		},
		Log: d.log,
	})
}
