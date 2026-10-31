// Command tendshelf runs the Tendshelf audiobook server.
//
// This is the P0 skeleton: it serves a placeholder page and /healthz on
// 127.0.0.1:8095 by default. See docs/architecture.md and docs/roadmap.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const defaultAddr = "127.0.0.1:8095"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tendshelf:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("tendshelf", flag.ContinueOnError)
	addr := fs.String("addr", envOr("TENDSHELF_ADDR", defaultAddr), "listen address (host:port)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := &http.Server{
		Addr:              *addr,
		Handler:           web.NewHandler(web.Options{Version: version}),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: audio streams are long-lived. Per-handler deadlines
		// are set with http.ResponseController where needed.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", "http://"+*addr, "version", version)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
