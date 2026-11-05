// Command tendshelf runs the Tendshelf audiobook server.
//
// It opens the database in the data folder, starts the job runner with the library scan and
// probe jobs, and serves the web interface and /healthz on 127.0.0.1:8095 by
// default. The start-up itself lives in internal/app. See docs/architecture.md and docs/roadmap.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/Tend-Stack/tendshelf/internal/app"
	"github.com/Tend-Stack/tendshelf/internal/auth"
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
		return app.ResetPassword(ctx, args[1:], os.Stdout)
	}
	fs := flag.NewFlagSet("tendshelf", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage:\n  tendshelf [flags]\n  %s\n\nFlags:\n", app.ResetUsage[len("usage: "):])
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
		d, err := app.DefaultDataDir()
		if err != nil {
			return err
		}
		dataDir = d
	}

	return app.Run(ctx, app.Config{
		Addr:           *addr,
		DataDir:        dataDir,
		FFprobe:        *ffprobe,
		FFmpeg:         *ffmpeg,
		TrustedProxies: proxies,
		CompatCacheGiB: *mf.compatGiB,
		Waveforms:      *mf.waveforms,
		Version:        version,
		Ready:          func(i app.Info) { serving(i.Addr) },
	})
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
