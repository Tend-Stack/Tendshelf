package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Protocol is the only protocol version this client speaks (runtimes/align/worker.py).
const Protocol = "tendshelf-emissions-v1"

// Limits shared with the worker.
const (
	MaxSamples      = 544000 // 34 s at 16 kHz
	minSamples      = 400
	maxHeader       = 64 << 10
	maxResponseData = 4 << 20
	maxLogProb      = 1e-3
	frameMs         = 20
	stopGrace       = 5 * time.Second
)

// Errors. ErrWorkerCrashed is uncertain (the same window may succeed again); the others are definite.
var (
	ErrWorkerCrashed   = errors.New("alignment worker stopped unexpectedly")
	ErrProtocol        = errors.New("alignment worker broke the protocol")
	ErrTimeout         = errors.New("alignment worker timed out")
	ErrIdentity        = errors.New("alignment worker identity does not match the checked model")
	ErrRequestTooLarge = errors.New("audio window is too large for the alignment worker")
	ErrNotUsable       = errors.New("alignment runtime is not checked or not usable")
	ErrClosed          = errors.New("alignment runtime is closed")
)

var requestID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)

// Identity is the worker's description of itself (--describe and the ready line).
type Identity struct {
	Protocol    string            `json:"protocol"`
	Model       string            `json:"model"`
	Revision    string            `json:"revision"`
	ModelSHA256 string            `json:"modelSha256"`
	Language    string            `json:"language"`
	Delimiter   string            `json:"delimiter"`
	Vocab       []string          `json:"vocab"`
	Blank       int               `json:"blank"`
	FrameMs     int               `json:"frameMs"`
	SampleRate  int               `json:"sampleRate"`
	Versions    map[string]string `json:"versions"`
}

// Request is one audio window: mono 16 kHz s16le samples.
type Request struct {
	ID            string
	FileQuickHash string
	OffsetMs      int64
	PCM           []byte
}

// Emissions are the CTC log-probabilities of one window, frame-major.
type Emissions struct {
	Frames, Vocab, FrameMs int
	LogProbs               []float32
}

// Emitter turns audio windows into emissions.
type Emitter interface {
	Identity() Identity
	Emit(context.Context, Request) (Emissions, error)
}

// Manager owns at most one worker process.
type Manager struct {
	cfg   Config
	comps *Components
	log   *slog.Logger

	mu     sync.Mutex // held for a whole request: one at a time
	p      *proc
	idle   *time.Timer
	last   time.Time
	closed bool

	cpu      atomic.Int64 // nanoseconds of exited workers
	launches atomic.Int64
}

// New returns a manager. It starts nothing.
func New(cfg Config, comps *Components, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{cfg: cfg.withDefaults(), comps: comps, log: log}
}

// CPUTime is the user plus system time of every worker that has exited.
func (m *Manager) CPUTime() time.Duration { return time.Duration(m.cpu.Load()) }

// pid is the running worker's process id, or 0 (tests).
func (m *Manager) pid() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.p == nil {
		return 0
	}
	return m.p.cmd.Process.Pid
}

// Emitter starts the worker for lang when none runs (stopping a worker of another language first) and
// returns a handle. It fails with ErrNotUsable when the component was not checked against this
// interpreter and this worker script.
func (m *Manager) Emitter(ctx context.Context, lang string) (Emitter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.ensureLocked(ctx, lang)
	if err != nil {
		return nil, err
	}
	return &emitter{m: m, lang: lang, ident: p.ident}, nil
}

// Close stops the worker. The manager cannot be used afterwards.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	if m.idle != nil {
		m.idle.Stop()
	}
	m.stopLocked()
	return nil
}

type emitter struct {
	m     *Manager
	lang  string
	ident Identity
}

func (e *emitter) Identity() Identity { return e.ident }

// Emit sends one window. A worker that idled out or failed earlier is launched again first.
func (e *emitter) Emit(ctx context.Context, req Request) (Emissions, error) {
	return e.m.emit(ctx, e.lang, req)
}

func (m *Manager) emit(ctx context.Context, lang string, req Request) (Emissions, error) {
	if len(req.PCM)%2 != 0 || len(req.PCM)/2 < minSamples || !requestID.MatchString(req.ID) {
		return Emissions{}, fmt.Errorf("%w: request is malformed", ErrProtocol)
	}
	if len(req.PCM)/2 > MaxSamples {
		return Emissions{}, ErrRequestTooLarge
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, err := m.ensureLocked(ctx, lang)
	if err != nil {
		return Emissions{}, err
	}
	finish := p.watch(ctx, m.cfg.RequestTimeout)
	em, err := p.exchange(req)
	if err = finish(err); err != nil {
		m.killLocked()
		return Emissions{}, err
	}
	m.touchLocked()
	return em, nil
}

// ensureLocked returns a live worker for lang, launching one when needed.
func (m *Manager) ensureLocked(ctx context.Context, lang string) (*proc, error) {
	if m.closed {
		return nil, ErrClosed
	}
	if m.p != nil && m.p.lang == lang && m.p.alive() {
		return m.p, nil
	}
	m.stopLocked()
	python, err := locate(m.cfg)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotUsable, err)
	}
	comp, ok, err := m.comps.Usable(ctx, lang, python, m.cfg.workerSHA())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotUsable
	}
	p, err := m.launch(ctx, python, comp)
	if err != nil {
		return nil, err
	}
	m.p = p
	m.touchLocked()
	return p, nil
}

// touchLocked records use and arms the idle timer.
func (m *Manager) touchLocked() {
	m.last = time.Now()
	if m.idle == nil {
		m.idle = time.AfterFunc(m.cfg.IdleStop, m.idleFired)
	} else {
		m.idle.Reset(m.cfg.IdleStop)
	}
}

func (m *Manager) idleFired() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.p == nil {
		return
	}
	if rest := m.cfg.IdleStop - time.Since(m.last); rest > 0 {
		m.idle.Reset(rest)
		return
	}
	m.stopLocked()
}

// stopLocked ends the worker politely: stdin closed, a grace period, then the kill.
func (m *Manager) stopLocked() {
	p := m.p
	if p == nil {
		return
	}
	m.p = nil
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(stopGrace):
		killGroup(p.cmd)
		<-p.done
	}
}

// killLocked ends the worker at once; the next request launches a new one.
func (m *Manager) killLocked() {
	p := m.p
	if p == nil {
		return
	}
	m.p = nil
	killGroup(p.cmd)
	<-p.done
}

// proc is one running worker.
type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	outF   *os.File
	lang   string
	nonce  string
	ident  Identity
	stderr *ring
	done   chan struct{}
}

func (p *proc) alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// launch starts a worker and waits for its ready line.
func (m *Manager) launch(ctx context.Context, python string, comp Component) (*proc, error) {
	script, err := writeScript(m.cfg)
	if err != nil {
		return nil, err
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(raw[:])
	env, err := childEnv(m.cfg, nonce)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(python, workerArgs(m.cfg, script, "--serve", comp.Language)...)
	cmd.Env = env
	cmd.Dir = m.cfg.WorkDir
	cmd.WaitDelay = waitDelay
	setAttrs(cmd)
	errRing := &ring{max: stderrCap}
	cmd.Stderr = errRing
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		return nil, fmt.Errorf("%w: %v", ErrWorkerCrashed, err)
	}
	_ = pw.Close()
	media.LowerPriority(cmd.Process.Pid)
	m.launches.Add(1)
	p := &proc{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(pr, maxHeader), outF: pr,
		lang: comp.Language, nonce: nonce, stderr: errRing, done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		if st := cmd.ProcessState; st != nil {
			m.cpu.Add(int64(st.UserTime() + st.SystemTime()))
		}
		_ = pr.Close()
		if tail := errRing.String(); tail != "" {
			m.log.Info("alignment worker ended", "language", comp.Language, "err", err, "stderr", tail)
		}
		close(p.done)
	}()
	finish := p.watch(ctx, m.cfg.ReadyTimeout)
	ident, err := p.readReady()
	if err = finish(err); err != nil {
		killGroup(cmd)
		<-p.done
		return nil, err
	}
	if ident.Protocol != Protocol || ident.ModelSHA256 != comp.ModelSHA256 || ident.Language != comp.Language ||
		ident.Revision != comp.Revision || len(ident.Vocab) == 0 || ident.FrameMs != frameMs {
		killGroup(cmd)
		<-p.done
		return nil, ErrIdentity
	}
	p.ident = ident
	return p, nil
}

// watch arms a deadline and a context watcher that kill the worker; the returned func disarms them and
// maps the error of the I/O they interrupted.
func (p *proc) watch(ctx context.Context, d time.Duration) func(error) error {
	var timedOut, ctxHit atomic.Bool
	timer := time.AfterFunc(d, func() {
		timedOut.Store(true)
		killGroup(p.cmd)
	})
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			ctxHit.Store(true)
			killGroup(p.cmd)
		case <-stop:
		}
	}()
	return func(err error) error {
		timer.Stop()
		close(stop)
		switch {
		case err == nil:
			return nil
		case timedOut.Load():
			return ErrTimeout
		case ctxHit.Load():
			return ctx.Err()
		}
		return err
	}
}

// readLine reads one newline-terminated line of at most maxHeader bytes.
func (p *proc) readLine() ([]byte, error) {
	line, err := p.out.ReadSlice('\n')
	switch {
	case errors.Is(err, bufio.ErrBufferFull):
		return nil, fmt.Errorf("%w: header line too long", ErrProtocol)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrWorkerCrashed, err)
	}
	return bytes.TrimRight(line, "\r\n"), nil
}

func (p *proc) readReady() (Identity, error) {
	line, err := p.readLine()
	if err != nil {
		return Identity{}, err
	}
	var r struct {
		Status   string   `json:"status"`
		Nonce    string   `json:"nonce"`
		Identity Identity `json:"identity"`
	}
	if err := json.Unmarshal(line, &r); err != nil || r.Status != "ready" {
		return Identity{}, fmt.Errorf("%w: first line is not a ready line", ErrProtocol)
	}
	if r.Nonce != p.nonce {
		return Identity{}, fmt.Errorf("%w: nonce", ErrIdentity)
	}
	return r.Identity, nil
}

// expectedFrames is the wav2vec2 frame count for a number of samples.
func expectedFrames(samples int) int { return (samples-400)/320 + 1 }

// exchange writes one request and reads and checks the response.
func (p *proc) exchange(req Request) (Emissions, error) {
	samples := len(req.PCM) / 2
	sum := sha256.Sum256(req.PCM)
	digest := hex.EncodeToString(sum[:])
	head, err := json.Marshal(map[string]any{
		"op": "emissions", "id": req.ID, "audioSha256": digest, "samples": samples,
		"fileQuickHash": req.FileQuickHash, "offsetMs": req.OffsetMs,
	})
	if err != nil {
		return Emissions{}, err
	}
	msg := make([]byte, 0, len(head)+1+len(req.PCM))
	msg = append(append(append(msg, head...), '\n'), req.PCM...)
	if _, err := p.stdin.Write(msg); err != nil {
		return Emissions{}, fmt.Errorf("%w: %v", ErrWorkerCrashed, err)
	}
	line, err := p.readLine()
	if err != nil {
		return Emissions{}, err
	}
	var r struct {
		ID          string `json:"id"`
		Nonce       string `json:"nonce"`
		AudioSHA256 string `json:"audioSha256"`
		Frames      int    `json:"frames"`
		VocabSize   int    `json:"vocabSize"`
		FrameMs     int    `json:"frameMs"`
		Bytes       int    `json:"bytes"`
		Error       string `json:"error"`
		Detail      string `json:"detail"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return Emissions{}, fmt.Errorf("%w: response header", ErrProtocol)
	}
	if r.Nonce != p.nonce {
		return Emissions{}, fmt.Errorf("%w: nonce", ErrProtocol)
	}
	if r.Error != "" {
		return Emissions{}, fmt.Errorf("%w: worker answered %q", ErrProtocol, r.Error)
	}
	vocab := len(p.ident.Vocab)
	switch {
	case r.ID != req.ID:
		return Emissions{}, fmt.Errorf("%w: id", ErrProtocol)
	case r.AudioSHA256 != digest:
		return Emissions{}, fmt.Errorf("%w: audio hash", ErrProtocol)
	case r.VocabSize != vocab:
		return Emissions{}, fmt.Errorf("%w: vocabulary size", ErrProtocol)
	case r.FrameMs != frameMs:
		return Emissions{}, fmt.Errorf("%w: frame length", ErrProtocol)
	case r.Frames < 1 || abs(r.Frames-expectedFrames(samples)) > 2:
		return Emissions{}, fmt.Errorf("%w: frame count", ErrProtocol)
	case r.Bytes != r.Frames*vocab*2 || r.Bytes > maxResponseData:
		return Emissions{}, fmt.Errorf("%w: payload size", ErrProtocol)
	}
	data := make([]byte, r.Bytes)
	if _, err := io.ReadFull(p.out, data); err != nil {
		return Emissions{}, fmt.Errorf("%w: %v", ErrWorkerCrashed, err)
	}
	lp := make([]float32, r.Frames*vocab)
	for i := range lp {
		v := halfToFloat(binary.LittleEndian.Uint16(data[2*i:]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v > maxLogProb {
			return Emissions{}, fmt.Errorf("%w: value out of range", ErrProtocol)
		}
		lp[i] = v
	}
	return Emissions{Frames: r.Frames, Vocab: vocab, FrameMs: r.FrameMs, LogProbs: lp}, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
