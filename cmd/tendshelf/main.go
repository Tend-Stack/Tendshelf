// Command tendshelf runs the Tendshelf audiobook server.
//
// It opens the database in the data folder, starts the job runner with the library scan and
// probe jobs, and serves the web interface and /healthz on 127.0.0.1:8095 by
// default. See docs/architecture.md and docs/roadmap.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const defaultAddr = "127.0.0.1:8095"

// serving is called once start-up is finished and the listener is bound; tests replace it to
// wait for readiness instead of guessing from files on disk.
var serving = func(net.Addr) {}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tendshelf:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "reset-password" {
		return runResetPassword(ctx, args[1:], os.Stdout)
	}
	fs := flag.NewFlagSet("tendshelf", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  tendshelf [flags]\n  %s\n\nFlags:\n", resetUsage[len("usage: "):])
		fs.PrintDefaults()
	}
	addr := fs.String("addr", envOr("TENDSHELF_ADDR", defaultAddr), "listen address (host:port)")
	dataFlag := fs.String("data", envOr("TENDSHELF_DATA", ""), "data folder (database, backups); default is per user")
	ffprobe := fs.String("ffprobe", envOr("TENDSHELF_FFPROBE", ""), "ffprobe program name or absolute path; default searches PATH")
	ffmpeg := fs.String("ffmpeg", envOr("TENDSHELF_FFMPEG", ""), "ffmpeg program name or absolute path; default searches PATH")
	proxyFlag := fs.String("trusted-proxy", envOr("TENDSHELF_TRUSTED_PROXY", ""), "comma-separated IPs or CIDR ranges of reverse proxies whose X-Forwarded-For and X-Forwarded-Proto are trusted; default none")
	mf := addMediaFlags(fs)
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	proxies, err := auth.ParseProxies(*proxyFlag)
	if err != nil {
		return fmt.Errorf("-trusted-proxy: %w", err)
	}

	dataDir := *dataFlag
	if dataDir == "" {
		d, err := defaultDataDir()
		if err != nil {
			return err
		}
		dataDir = d
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	logger.Info("starting", "version", version, "data", dataDir)

	st, err := db.Open(ctx, dataDir)
	if err != nil {
		return describeOpenError(err, dataDir)
	}
	defer st.Close()
	runner, err := jobs.New(st, jobs.Options{Logger: logger})
	if err != nil {
		return err
	}
	// Job kinds are registered here by the features that own them.
	tools := locateTools(media.Config{FFprobe: *ffprobe, FFmpeg: *ffmpeg}, logger)
	lib := library.New(st)
	sc := scan.New(lib, tools, runner, logger)
	d := deps{st: st, lib: lib, scanner: sc, runner: runner, tools: tools, dataDir: dataDir, log: logger}
	if err := registerDetails(d); err != nil {
		return err
	}
	compat, peaks, err := registerMediaJobs(d, mf)
	if err != nil {
		return err
	}
	if err := sc.Register(runner); err != nil {
		return err
	}
	setupCode := auth.NewSetupCode()
	handler, err := web.NewHandler(ctx, web.Options{Version: version, Store: st, SetupCode: setupCode, TrustedProxies: proxies,
		Library: lib, DataDir: dataDir, Jobs: runner, Compat: compat, Peaks: peaks})
	if err != nil {
		return err
	}
	// Bind before the runner starts: a busy port fails here with nothing to stop, and once serving
	// is called, start-up (migrations, registration, runner) is really finished.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	if err := runner.Start(ctx); err != nil {
		ln.Close()
		return err
	}
	// Files scanned before waveforms existed (or while they were off) get their peaks in the
	// background; a failure here costs only the waveform, never the start.
	if n, err := peaks.Backfill(ctx); err != nil {
		logger.Warn("waveforms: backfill failed", "err", err)
	} else if n > 0 {
		logger.Info("waveforms: queued missing peaks", "files", n)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: audio streams are long-lived. Per-handler deadlines
		// are set with http.ResponseController where needed.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	logger.Info("listening", "addr", "http://"+ln.Addr().String(), "version", version)
	if hasOwner, err := users.New(st, users.Options{}).OwnerExists(ctx); err == nil && !hasOwner {
		// The one place a secret is logged on purpose: the owner needs it, and only the person who
		// can read this log can open the setup page.
		logger.Info("first run: open this link to create the owner account", "url", setupURL(ln.Addr(), setupCode))
	}
	serving(ln.Addr())
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
