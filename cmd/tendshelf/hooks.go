package main

import (
	"flag"
	"log/slog"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
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
	return svc.Register(d.runner)
}

// mediaFlags holds the command-line flags of the media jobs.
type mediaFlags struct{}

// addMediaFlags declares the media jobs' flags before the command line is parsed.
func addMediaFlags(fs *flag.FlagSet) *mediaFlags { return &mediaFlags{} }

// registerMediaJobs is the hook for the media job kinds (covers, waveforms and the like).
func registerMediaJobs(d deps, mf *mediaFlags) error { return nil }
