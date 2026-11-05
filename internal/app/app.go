// Package app is the Tendshelf server's start-up: it opens the data folder, registers the job
// kinds, binds the listener, serves the web interface and shuts down cleanly. The command-line
// program (cmd/tendshelf) and the desktop shell both call Run, so they start the same server.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"
	_ "time/tzdata" // zone names work in a container without zoneinfo (decision 0013)

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web"
)

// Config is everything Run needs; the caller resolves flags and environment first.
type Config struct {
	Addr           string       // listen address (host:port); ignored when Listener is set
	Listener       net.Listener // wins over Addr when set; Run closes it
	DataDir        string       // database, backups and caches
	FFprobe        string       // program name or absolute path; "" searches PATH
	FFmpeg         string       // same
	TrustedProxies auth.Proxies
	CompatCacheGiB int // size cap of the compatibility-copy cache in GiB (at least 1)
	Waveforms      bool
	Version        string
	Logger         *slog.Logger // nil logs text to standard error
	// Ready is called once, after the listener is bound and the job runner has started.
	Ready func(Info)
}

// Info is what Ready tells the caller.
type Info struct {
	Addr         net.Addr
	SetupURL     string // the first-run link with its code; empty once an owner exists
	ToolsMissing bool   // ffprobe or ffmpeg was not found
}

// Run starts the server and blocks until ctx is cancelled or serving fails. It returns after the
// HTTP server and the job runner have stopped and the database is closed.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Listener != nil {
		defer cfg.Listener.Close() // Run owns it, also on the early error returns
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	version, dataDir := cfg.Version, cfg.DataDir
	logger.Info("starting", "version", version, "data", dataDir)

	st, err := db.Open(ctx, dataDir)
	if err != nil {
		return describeOpenError(err, dataDir)
	}
	defer st.Close()
	runner, err := jobs.New(st, jobs.Options{Logger: logger, ProgressEvery: 500 * time.Millisecond}) // half a second so the lookup log keeps up with its polling
	if err != nil {
		return err
	}
	// Job kinds are registered here by the features that own them.
	tools := locateTools(media.Config{FFprobe: cfg.FFprobe, FFmpeg: cfg.FFmpeg}, logger)
	lib := library.New(st)
	sc := scan.New(lib, tools, runner, logger)
	d := deps{st: st, lib: lib, scanner: sc, runner: runner, tools: tools, dataDir: dataDir, version: version, log: logger}
	details, onlineClient, err := registerDetails(d)
	if err != nil {
		return err
	}
	compat, peaks, covers, writes, err := registerMediaJobs(d, cfg.CompatCacheGiB, cfg.Waveforms)
	if err != nil {
		return err
	}
	bk, err := registerBackups(d)
	if err != nil {
		return err
	}
	covers.SetFetcher(onlineClient.FetchCover)
	details.SetCoverFill(covers.FillOnline)
	ebooks, err := registerEbooks(d)
	if err != nil {
		return err
	}
	wireTextBooks(details, covers, ebooks, logger)
	if err := sc.Register(runner); err != nil {
		return err
	}
	// Adding books by upload (decision 0015): the staging store, the upload.place job and the sweep.
	uploads := upload.New(upload.Config{DataDir: dataDir}, st, lib, tools, runner, logger)
	if err := uploads.Register(runner); err != nil {
		return err
	}
	setupCode := auth.NewSetupCode()
	handler, err := web.NewHandler(ctx, web.Options{Version: version, Store: st, SetupCode: setupCode, TrustedProxies: cfg.TrustedProxies,
		Library: lib, DataDir: dataDir, Jobs: runner, Compat: compat, Peaks: peaks, Details: details, Covers: covers, Writes: writes, Uploads: uploads, Ebooks: ebooks, Backups: bk, MailOptions: share.MailOptions{Logger: logger}})
	if err != nil {
		return err
	}
	// Bind before the runner starts: a busy port fails here with nothing to stop, and once Ready
	// is called, start-up (migrations, registration, runner) is really finished.
	ln := cfg.Listener
	if ln == nil {
		ln, err = net.Listen("tcp", cfg.Addr)
		if err != nil {
			return err
		}
	}
	if err := runner.Start(ctx); err != nil {
		ln.Close()
		return err
	}
	// Queue the read of every ebook still pending, and retry those refused only because a parser was missing.
	if err := ebooks.EnqueuePending(ctx); err != nil {
		logger.Warn("ebooks: could not queue pending reads", "err", err)
	}
	// Settle any write-details swap that a crash left half done, then check again every 30 seconds.
	go writes.Run(ctx)
	// Scheduled database copies: the first check two minutes after start, then every 15 minutes.
	go bk.Run(ctx)
	// Expire stale uploads and clear their staging: the first sweep a minute after start, then hourly.
	go uploads.Run(ctx)
	// Files scanned before waveforms existed (or while they were off) get their peaks in the
	// background; a failure here costs only the waveform, never the start.
	if n, err := peaks.Backfill(ctx); err != nil {
		logger.Warn("waveforms: backfill failed", "err", err)
	} else if n > 0 {
		logger.Info("waveforms: queued missing peaks", "files", n)
	}

	srv := &http.Server{
		Addr:              ln.Addr().String(),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: audio streams are long-lived. Per-handler deadlines
		// are set with http.ResponseController where needed.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	logger.Info("listening", "addr", "http://"+ln.Addr().String(), "version", version)
	info := Info{Addr: ln.Addr(), ToolsMissing: tools.FFprobe == "" || tools.FFmpeg == ""}
	if hasOwner, err := users.New(st, users.Options{}).OwnerExists(ctx); err == nil && !hasOwner {
		// The one place a secret is logged on purpose: the owner needs it, and only the person who
		// can read this log can open the setup page.
		info.SetupURL = setupURL(ln.Addr(), setupCode)
		logger.Info("first run: open this link to create the owner account", "url", info.SetupURL)
	}
	if cfg.Ready != nil {
		cfg.Ready(info)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	var serveErr error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		serveErr = srv.Shutdown(shutdownCtx)
		cancel()
	}

	// Stop the runner after the server: running jobs are requeued or held.
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runner.Stop(stopCtx); err != nil {
		logger.Warn("job runner did not stop in time; unfinished jobs are recovered at the next start", "err", err)
	}
	return serveErr // st.Close runs on return
}

// setupURL is the link the owner opens on first run, with the setup code filled in. An address
// that listens everywhere is shown as localhost.
func setupURL(a net.Addr, code string) string {
	host, port, err := net.SplitHostPort(a.String())
	if err != nil {
		return "http://localhost/setup?code=" + code
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port) + "/setup?code=" + code
}

// locateTools finds ffprobe and ffmpeg. Missing tools are not fatal: books are still listed from
// their file names, and the files that need ffprobe wait with a plain-words error.
func locateTools(cfg media.Config, log *slog.Logger) media.Tools {
	tools, err := media.Locate(cfg)
	if err != nil {
		log.Warn("audio tools not found; files will be listed but not read", "err", err)
	}
	return tools
}

// describeOpenError turns a refusal into plain words that name the file and say
// it was not changed.
func describeOpenError(err error, dataDir string) error {
	var se *db.SchemaError
	path := db.FilePath(dataDir)
	switch {
	case errors.As(err, &se) && errors.Is(err, db.ErrSchemaTooNew):
		return fmt.Errorf("the database at %s was made by a newer version of Tendshelf (schema %d; this version reads up to %d). Install the newer version or choose another -data folder. The file was not changed.", path, se.Found, se.Supported)
	case errors.Is(err, db.ErrNotTendshelf):
		return fmt.Errorf("the file at %s is not a Tendshelf database. The file was not changed.", path)
	}
	return fmt.Errorf("open the database at %s: %w", path, err)
}
