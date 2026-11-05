package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/app"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
)

// shellQuery marks the first page of every session so the server sets the ts_shell cookie.
const shellQuery = "/?shell=desktop"

// missingToolsWords is the one native warning when ffprobe or ffmpeg is not installed.
const missingToolsWords = "Tendshelf needs ffmpeg to read audio files. On Ubuntu: sudo apt install ffmpeg. Until then, books are listed but won't play."

// UI is what a session needs from the window. The real one wraps the Wails window; tests use a fake.
type UI interface {
	// Load opens a page: an absolute http(s) URL, or a path on the picker's own origin.
	Load(url string)
	// Warn shows one native message.
	Warn(message string)
}

// libraryError is a failed start in the words the person sees; the cause goes to the log.
type libraryError struct {
	words string
	cause error
}

func (e *libraryError) Error() string { return e.words + ": " + e.cause.Error() }
func (e *libraryError) Unwrap() error { return e.cause }
func (e *libraryError) Words() string { return e.words }

// libraryWords turns a failed app.Run into plain words.
func libraryWords(err error) error {
	switch {
	case errors.Is(err, db.ErrSchemaTooNew):
		return &libraryError{"This library was made by a newer Tendshelf. Update the app to open it.", err}
	case errors.Is(err, db.ErrNotTendshelf):
		return &libraryError{"That folder holds a file that isn't a Tendshelf library, so the app left it alone.", err}
	}
	return &libraryError{"Couldn't open your library. Please try again.", err}
}

// Session is the desktopshell.Starter of the window. One session runs at a time: personal mode is
// the library served on loopback inside this process, connect mode is just a loaded server.
type Session struct {
	dirs    desktopshell.Dirs
	version string
	ui      UI

	// Seams for tests.
	run      func(context.Context, app.Config) error
	check    func(context.Context, string) error
	listen   func(savedPort int) (net.Listener, error)
	stopWait time.Duration
	readyMax time.Duration

	root   context.Context // cancelled by Stop, so a start in progress gives up too
	cancel context.CancelFunc

	op  sync.Mutex // one start, switch or stop at a time
	cur *running

	mu     sync.Mutex // guards st and origin; State and Origin never wait for a start
	st     desktopshell.State
	origin string
}

type running struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// NewSession builds the session for the folders and version of this run.
func NewSession(dirs desktopshell.Dirs, version string, ui UI) *Session {
	root, cancel := context.WithCancel(context.Background())
	return &Session{
		dirs: dirs, version: version, ui: ui,
		run:      app.Run,
		check:    desktopshell.CheckServer,
		listen:   listenLoopback,
		stopWait: 15 * time.Second,
		readyMax: 60 * time.Second,
		root:     root, cancel: cancel,
	}
}

// listenLoopback binds the saved port on loopback (localStorage is keyed by origin, so the port
// should stay), or any free one when it is 0 or taken.
func listenLoopback(saved int) (net.Listener, error) {
	if saved > 0 {
		if ln, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", saved)); err == nil {
			return ln, nil
		}
	}
	return net.Listen("tcp4", "127.0.0.1:0")
}

// State reports progress to the start page.
func (s *Session) State() desktopshell.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// Origin is the web origin the window is meant to be showing, or "" while it shows the picker.
func (s *Session) Origin() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.origin
}

func (s *Session) set(st desktopshell.State, origin string) {
	s.mu.Lock()
	s.st, s.origin = st, origin
	s.mu.Unlock()
}

func (s *Session) fail(err error) {
	slog.Error("desktop session", "err", err)
	s.set(desktopshell.State{Kind: desktopshell.Failed, Words: desktopshell.Words(err)}, "")
}

// Begin runs before the window exists: with a saved mode the picker shows "Opening your library…"
// at once instead of the two choices. It reports whether Launch has work to do.
func (s *Session) Begin() bool {
	cfg, err := desktopshell.Load(s.dirs.File())
	if err != nil {
		s.fail(err)
		return false
	}
	if cfg.Mode == "" {
		return false
	}
	s.set(desktopshell.State{Kind: desktopshell.Busy}, "")
	return true
}

// Launch starts the saved mode. A failure leaves the picker up with the words.
func (s *Session) Launch() {
	cfg, err := desktopshell.Load(s.dirs.File())
	if err != nil {
		s.fail(err)
		s.ui.Load("/")
		return
	}
	switch cfg.Mode {
	case desktopshell.ModePersonal:
		err = s.StartPersonal()
	case desktopshell.ModeConnect:
		err = s.connectChecked(cfg.Server)
	}
	if err != nil {
		s.fail(err)
		s.ui.Load("/") // the picker was drawn as busy; draw it again with the words
	}
}

func (s *Session) connectChecked(origin string) error {
	ctx, cancel := context.WithTimeout(s.root, 8*time.Second)
	defer cancel()
	if err := s.check(ctx, origin); err != nil {
		return err
	}
	return s.Connect(origin)
}

// StartPersonal serves the library on loopback and opens it in the window. It returns once the
// library is ready or has failed.
func (s *Session) StartPersonal() error {
	s.op.Lock()
	defer s.op.Unlock()
	s.stopLocked()
	s.set(desktopshell.State{Kind: desktopshell.Busy}, "")

	cfg, err := desktopshell.Load(s.dirs.File())
	if err != nil {
		s.fail(err)
		return err
	}
	ln, err := s.listen(cfg.Port)
	if err != nil {
		err = &libraryError{"Couldn't open your library. Please try again.", err}
		s.fail(err)
		return err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	origin := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(s.root)
	r := &running{cancel: cancel, done: make(chan struct{})}
	ready := make(chan app.Info, 1)
	finished := make(chan error, 1)
	go func() {
		defer close(r.done)
		finished <- s.run(ctx, app.Config{
			Listener:       ln,
			DataDir:        s.dirs.Library,
			CompatCacheGiB: 5,
			Waveforms:      true,
			Version:        s.version,
			Ready:          func(i app.Info) { ready <- i },
		})
	}()

	select {
	case info := <-ready:
		cfg.Schema, cfg.Mode, cfg.Port = 1, desktopshell.ModePersonal, port
		if err := desktopshell.Save(s.dirs.File(), cfg); err != nil {
			slog.Warn("save the desktop settings", "err", err) // the library still opens
		}
		s.cur = r
		s.set(desktopshell.State{Kind: desktopshell.Idle}, origin)
		target := origin + shellQuery
		if info.SetupURL != "" {
			if u, err := url.Parse(info.SetupURL); err == nil {
				target += "&next=" + url.QueryEscape(u.RequestURI())
			}
		}
		s.ui.Load(target)
		if info.ToolsMissing {
			s.ui.Warn(missingToolsWords)
		}
		go s.watch(ctx, finished)
		return nil
	case err := <-finished:
		cancel()
		err = libraryWords(err)
		s.fail(err)
		return err
	case <-time.After(s.readyMax):
		cancel()
		<-r.done
		err := &libraryError{"Couldn't open your library. Please try again.", errors.New("start-up took too long")}
		s.fail(err)
		return err
	case <-s.root.Done():
		cancel()
		<-r.done
		return s.root.Err()
	}
}

// watch reports a library that stops by itself after it opened.
func (s *Session) watch(ctx context.Context, finished <-chan error) {
	err := <-finished
	if ctx.Err() != nil {
		return // we stopped it
	}
	s.fail(libraryWords(errors.Join(errors.New("the library stopped"), err)))
	s.ui.Load("/?change=1")
}

// Connect opens a server the start page already checked. It stops personal mode first, so a
// switch leaves no job running.
func (s *Session) Connect(origin string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.stopLocked()
	cfg, err := desktopshell.Load(s.dirs.File())
	if err != nil {
		return err
	}
	cfg.Schema, cfg.Mode, cfg.Server = 1, desktopshell.ModeConnect, origin
	if err := desktopshell.Save(s.dirs.File(), cfg); err != nil {
		slog.Warn("save the desktop settings", "err", err)
	}
	s.set(desktopshell.State{Kind: desktopshell.Idle}, origin)
	s.ui.Load(origin + shellQuery)
	return nil
}

// Change goes back to the picker. Personal mode keeps running until a new choice stops it.
func (s *Session) Change() {
	s.set(desktopshell.State{Kind: desktopshell.Idle}, "")
	s.ui.Load("/?change=1")
}

// Stop ends the running library, waiting up to stopWait for its jobs to stop. Safe to call twice.
func (s *Session) Stop() {
	s.cancel()
	s.op.Lock()
	defer s.op.Unlock()
	s.stopLocked()
}

func (s *Session) stopLocked() {
	r := s.cur
	s.cur = nil
	if r == nil {
		return
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(s.stopWait):
		slog.Warn("the library did not stop in time")
	}
}
