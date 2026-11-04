package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

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
// shows and starts them; main runs the schedule check once the runner is going.
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

// mediaFlags holds the command-line flags of the media jobs.
type mediaFlags struct {
	compatGiB *int
	waveforms *bool
}

// addMediaFlags declares the media jobs' flags before the command line is parsed.
func addMediaFlags(fs *flag.FlagSet) *mediaFlags {
	return &mediaFlags{
		compatGiB: fs.Int("compat-cache-gib", envOrInt("TENDSHELF_COMPAT_CACHE_GIB", 5),
			"size cap of the compatibility-copy cache in GiB (at least 1)"),
		waveforms: fs.Bool("waveforms", envOrBool("TENDSHELF_WAVEFORMS", true),
			"draw waveforms behind the scrubber (decodes each audio file once in the background)"),
	}
}

// envOrInt returns the integer in the environment variable key, or def if it is unset or not a
// whole number.
func envOrInt(key string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return n
	}
	return def
}

// envOrBool returns the boolean in the environment variable key (anything strconv.ParseBool reads),
// or def if it is unset or not a boolean.
func envOrBool(key string, def bool) bool {
	if b, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return b
	}
	return def
}

// registerMediaJobs is the hook for the media job kinds: cover pictures with thumbnails and
// accent colours, compatibility copies and waveform peaks. Covers and peaks follow the scanner's
// settled books. It returns the compatibility and waveform services so the web handler can request
// and serve them, plus the covers service for online cover adoption and the write-details service.
func registerMediaJobs(d deps, mf *mediaFlags) (*mediajobs.Compat, *mediajobs.Peaks, *mediajobs.Covers, *mediajobs.Writes, error) {
	if *mf.compatGiB < 1 {
		return nil, nil, nil, nil, fmt.Errorf("-compat-cache-gib must be at least 1, got %d", *mf.compatGiB)
	}
	covers := mediajobs.NewCovers(d.lib, d.tools, d.dataDir, d.runner, d.log)
	compat := mediajobs.NewCompat(d.lib, d.tools, mediajobs.CompatDir(d.dataDir), int64(*mf.compatGiB)<<30, d.runner, d.log)
	peaks := mediajobs.NewPeaks(d.lib, d.tools, mediajobs.PeaksDir(d.dataDir), d.runner, *mf.waveforms, d.log)
	d.scanner.OnBookSettled(covers.Follower())
	d.scanner.OnScanFinished(covers.Finisher())
	d.scanner.OnBookSettled(peaks.Follower())
	writes := mediajobs.NewWrites(d.lib, d.tools, d.dataDir, d.runner, d.runner, d.log)
	// After files were swapped or restored the library is scanned again: the same two calls as the
	// admin Scan button (internal/web startScan), joining a scan that is already waiting.
	writes.OnChanged = func(ctx context.Context, libraryID string) {
		if _, ok, err := d.runner.ActiveFor(ctx, scan.KindScan, "library_id", libraryID); err != nil || ok {
			return
		}
		if _, err := d.runner.Enqueue(ctx, scan.KindScan, scan.ScanPayload{LibraryID: libraryID}, jobs.EnqueueOptions{}); err != nil {
			d.log.Warn("write details: could not queue a scan", "library", libraryID, "err", err)
		}
	}
	if err := mediajobs.Register(d.runner, covers, compat, peaks, writes); err != nil {
		return nil, nil, nil, nil, err
	}
	return compat, peaks, covers, writes, nil
}
