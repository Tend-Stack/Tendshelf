package runtime

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

const (
	waitDelay      = 2 * time.Second
	describeOutCap = 1 << 20
	stderrCap      = 64 << 10
)

// limitedBuffer keeps the first max bytes and discards the rest; Write always reports success so the
// child never blocks on a full pipe.
type limitedBuffer struct {
	mu        sync.Mutex
	max       int
	buf       bytes.Buffer
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := b.max - b.buf.Len()
	if room < len(p) {
		b.truncated = true
		if room > 0 {
			b.buf.Write(p[:room])
		}
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

func (b *limitedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

func (b *limitedBuffer) wasTruncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

// ring keeps the last max bytes written to it (the end of stderr is the useful part).
type ring struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.b = append(r.b, p...)
	if len(r.b) > r.max {
		r.b = append([]byte(nil), r.b[len(r.b)-r.max:]...)
	}
	return len(p), nil
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(bytes.TrimSpace(r.b))
}

// childEnv is the whole environment of a worker: an allowlist, never the server's own.
func childEnv(cfg Config, nonce string) ([]string, error) {
	home := filepath.Join(cfg.WorkDir, "home")
	tmp := filepath.Join(cfg.WorkDir, "tmp")
	hf := filepath.Join(cfg.WorkDir, "hf-home")
	for _, d := range []string{cfg.WorkDir, home, tmp, hf} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	n := strconv.Itoa(cfg.Threads)
	env := []string{"LC_ALL=C"}
	if nonce != "" {
		env = append(env, "TENDSHELF_ALIGN_NONCE="+nonce)
	}
	env = append(env,
		"OMP_NUM_THREADS="+n,
		"MKL_NUM_THREADS="+n,
		"HF_HUB_OFFLINE=1",
		"TRANSFORMERS_OFFLINE=1",
		"HF_HOME="+hf,
		"HOME="+home,
		"TMPDIR="+tmp,
	)
	return append(env, cfg.Env...), nil
}

// workerArgs builds the fixed argument list. mode is "--describe" or "--serve".
func workerArgs(cfg Config, script, mode, lang string) []string {
	return []string{"-I", "-B", "-u", script, mode,
		"--model-dir", filepath.Join(cfg.ModelsDir, lang),
		"--language", lang,
		"--threads", strconv.Itoa(cfg.Threads),
		"--device", "cpu"}
}

// writeScript writes the embedded worker to <WorkDir>/worker/worker-<sha12>.py (0600) and returns its path.
func writeScript(cfg Config) (string, error) {
	dir := filepath.Join(cfg.WorkDir, "worker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "worker-"+cfg.workerSHA()[:12]+".py")
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, cfg.Script) {
		return path, nil
	}
	tmp, err := os.CreateTemp(dir, ".worker-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(cfg.Script)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(tmp.Name())
		if werr != nil {
			return "", werr
		}
		return "", cerr
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

// runOnce runs the worker to completion (describe) under ctx: no shell, rebuilt environment, stdin
// /dev/null, capped output, lowered priority, killed with its group on expiry.
func runOnce(ctx context.Context, cfg Config, python string, args []string) (stdout, stderr []byte, capped bool, err error) {
	env, err := childEnv(cfg, "")
	if err != nil {
		return nil, nil, false, err
	}
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Stdin = nil
	cmd.Env = env
	cmd.Dir = cfg.WorkDir
	cmd.WaitDelay = waitDelay
	setAttrs(cmd)
	cmd.Cancel = func() error { killGroup(cmd); return nil }
	out := &limitedBuffer{max: describeOutCap}
	errb := &limitedBuffer{max: stderrCap}
	cmd.Stdout, cmd.Stderr = out, errb
	err = cmd.Start()
	if err == nil {
		media.LowerPriority(cmd.Process.Pid)
		err = cmd.Wait()
	}
	return out.bytes(), errb.bytes(), out.wasTruncated(), err
}
