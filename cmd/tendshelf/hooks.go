package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
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
	log     *slog.Logger
}

// registerDetails is the hook for the book details feature: its follower and job kinds.
func registerDetails(d deps) error {
	svc := metadata.New(d.lib, d.runner, d.log)
	d.scanner.OnBookSettled(svc.Follower())
	d.scanner.OnScanFinished(svc.Finisher())
	return svc.Register(d.runner)
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
// and serve them.
func registerMediaJobs(d deps, mf *mediaFlags) (*mediajobs.Compat, *mediajobs.Peaks, error) {
	if *mf.compatGiB < 1 {
		return nil, nil, fmt.Errorf("-compat-cache-gib must be at least 1, got %d", *mf.compatGiB)
	}
	covers := mediajobs.NewCovers(d.lib, d.tools, d.dataDir, d.runner, d.log)
	compat := mediajobs.NewCompat(d.lib, d.tools, mediajobs.CompatDir(d.dataDir), int64(*mf.compatGiB)<<30, d.runner, d.log)
	peaks := mediajobs.NewPeaks(d.lib, d.tools, mediajobs.PeaksDir(d.dataDir), d.runner, *mf.waveforms, d.log)
	d.scanner.OnBookSettled(covers.Follower())
	d.scanner.OnScanFinished(covers.Finisher())
	d.scanner.OnBookSettled(peaks.Follower())
	if err := mediajobs.Register(d.runner, covers, compat, peaks); err != nil {
		return nil, nil, err
	}
	return compat, peaks, nil
}
