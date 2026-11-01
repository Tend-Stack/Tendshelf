// Package media runs ffprobe and reads audio files without changing them.
//
// The runner is deliberately small and strict (AGENTS.md, "Audio files are the user's"):
//   - Library files are only read: stat, open read-only, or handed to ffprobe. Nothing here
//     writes, renames or deletes a library file.
//   - A copy-protected extension (aax, aaxc, aa, m4p) is refused before any open or exec, and
//     encrypted content found by ffprobe is reported as error code "drm" wrapping ErrProtected.
//   - Programs run with a fixed argument list (never a shell), the input as "file:" plus an
//     absolute path, -protocol_whitelist file,pipe, a deadline, an environment of exactly
//     LC_ALL=C plus Tools.Env, and capped output. ffmpeg also gets -nostdin; ffprobe has no such
//     option, so it reads /dev/null as stdin instead.
//
// Not handled here: an MP4 box walk for DRM (the extension, ffprobe's codec tag and its
// "Encryption info" side data are the signals; revisit with a real sample), a check of the ffprobe
// version, and killing a process group (ffprobe starts no child processes).
//
// The package imports no other Tendshelf package. Cover, thumbnail and remux code will join it
// in later slices.
package media

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Output and time limits for one ffprobe run. A slow NAS can need many seconds to open a file.
const (
	defaultProbeTimeout = 30 * time.Second
	waitDelay           = 2 * time.Second
	stdoutCap           = 4 << 20
	stderrCap           = 64 << 10
)

// Config holds the owner's settings: a program name, an absolute path, or "" to search PATH.
type Config struct {
	FFprobe string
	FFmpeg  string
}

// Tools are the located programs and their run settings.
type Tools struct {
	FFprobe      string        // absolute path or a name; "" means not available
	FFmpeg       string        // same
	ProbeTimeout time.Duration // 0 means 30 seconds
	// FFmpegTimeout, if set, replaces the deadline of every ffmpeg operation (tests use it).
	FFmpegTimeout time.Duration
	Env           []string // extra KEY=VALUE entries after LC_ALL=C (tests use this)
}

// Locate finds ffprobe and ffmpeg. A missing tool leaves its field empty and is reported as a
// *NotFoundError (matching ErrNotFound); both errors are joined. The tool that was found is
// still returned, so the interface can say exactly which one is missing.
func Locate(cfg Config) (Tools, error) {
	var t Tools
	var errs []error
	var err error
	if t.FFprobe, err = locateOne("ffprobe", cfg.FFprobe); err != nil {
		errs = append(errs, err)
	}
	if t.FFmpeg, err = locateOne("ffmpeg", cfg.FFmpeg); err != nil {
		errs = append(errs, err)
	}
	return t, errors.Join(errs...)
}

func locateOne(name, value string) (string, error) {
	switch {
	case value == "":
		return lookPath(name, "PATH", name)
	case !strings.ContainsRune(value, filepath.Separator):
		return lookPath(name, "PATH", value)
	}
	if !filepath.IsAbs(value) {
		return "", &NotFoundError{Tool: name, Tried: value + " (must be an absolute path)"}
	}
	fi, err := os.Stat(value)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o111 == 0 {
		return "", &NotFoundError{Tool: name, Tried: value}
	}
	return value, nil
}

func lookPath(tool, where, program string) (string, error) {
	p, err := exec.LookPath(program)
	if err != nil { // includes exec.ErrDot: a program found in the working directory does not count
		return "", &NotFoundError{Tool: tool, Tried: where}
	}
	return p, nil
}

// limitedBuffer keeps the first max bytes and discards the rest. Write always reports success so
// the child keeps draining its pipe instead of blocking.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - b.buf.Len()
	if room > len(p) {
		room = len(p)
	}
	if room > 0 {
		b.buf.Write(p[:room])
	}
	if room < len(p) {
		b.truncated = true
	}
	return len(p), nil
}

func (b *limitedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func (b *limitedBuffer) wasTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// runResult is what one finished (or killed) child left behind.
type runResult struct {
	stdout, stderr []byte
	capped         bool // stdout went over its cap and was cut
	err            error
}

// run executes bin with args under ctx. stdin is /dev/null, the environment is LC_ALL=C plus
// extraEnv, and both output streams are capped.
func run(ctx context.Context, bin string, args []string, extraEnv []string) runResult {
	return runCapped(ctx, bin, args, extraEnv, stdoutCap)
}

// runCapped is run with its own stdout cap, for ffmpeg writing a picture to a pipe.
func runCapped(ctx context.Context, bin string, args []string, extraEnv []string, outCap int) runResult {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = nil // /dev/null
	cmd.Env = append([]string{"LC_ALL=C"}, extraEnv...)
	cmd.WaitDelay = waitDelay
	out := &limitedBuffer{max: outCap}
	errb := &limitedBuffer{max: stderrCap}
	cmd.Stdout, cmd.Stderr = out, errb
	err := cmd.Run()
	return runResult{stdout: out.bytes(), stderr: errb.bytes(), capped: out.wasTruncated(), err: err}
}

func describe(r runResult, fallback error) string {
	if s := strings.TrimSpace(string(r.stderr)); s != "" {
		return s
	}
	if fallback != nil {
		return fallback.Error()
	}
	return "no output"
}
