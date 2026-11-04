package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/align/runtime/fakeworker"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

const (
	rtParentSwitch = "TENDSHELF_FAKE_ALIGN_PARENT"
	rtLang         = "en"
)

// TestMain turns the test binary into the fake worker (or the parent that owns one) when asked.
func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeworker.EnvSwitch); mode != "" {
		os.Exit(fakeworker.Main(mode))
	}
	if os.Getenv(rtParentSwitch) != "" {
		os.Exit(rtParentMain())
	}
	os.Exit(m.Run())
}

// rtFiles are the fake model's files.
var rtFiles = map[string]string{"config.json": "{}", "model.safetensors": "weights"}

type rtEnv struct {
	st    *db.Store
	cfg   Config
	comps *Components
	mgr   *Manager
	man   Manifest
}

// rtModel writes the fake model files and returns the manifest that pins them.
func rtModel(t *testing.T, modelsDir string) Manifest {
	t.Helper()
	dir := filepath.Join(modelsDir, rtLang)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	man := Manifest{Format: ManifestFormat, Version: 1, Language: rtLang, Repository: fakeworker.Repository,
		Revision: fakeworker.Revision, LicenceSPDX: "Apache-2.0", LicenceURL: "https://example.test/l", SourceURL: "https://example.test/s"}
	for name, body := range rtFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(body))
		man.Files = append(man.Files, ManifestFile{Name: name, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])})
	}
	return man
}

// rtNew builds a checked environment whose worker runs in mode. extra env entries are appended.
func rtNew(t *testing.T, mode string, extra ...string) *rtEnv {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	work := t.TempDir()
	models := filepath.Join(work, "models")
	man := rtModel(t, models)
	cfg := Config{
		Python: os.Args[0], ModelsDir: models, WorkDir: filepath.Join(work, "align"),
		Script: []byte("# fake worker script\n"), Manifests: map[string]Manifest{rtLang: man},
		Threads: 2, ReadyTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second, DescribeTimeout: 5 * time.Second,
		IdleStop: time.Minute, Env: append([]string{fakeworker.EnvSwitch + "=" + mode}, extra...),
	}
	e := &rtEnv{st: st, cfg: cfg, comps: NewComponents(st), man: man}
	e.mgr = New(cfg, e.comps, nil)
	t.Cleanup(func() { e.mgr.Close() })
	return e
}

// rtCheck runs Check and returns the component.
func (e *rtEnv) check(t *testing.T) Component {
	t.Helper()
	c, err := Check(context.Background(), e.cfg, e.comps, rtLang)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// rtPCM returns n samples of silence.
func rtPCM(n int) []byte { return make([]byte, 2*n) }

func rtReq(id string, samples int) Request {
	return Request{ID: id, FileQuickHash: strings.Repeat("a", 64), OffsetMs: 1000, PCM: rtPCM(samples)}
}

// rtReady checks the component and returns an emitter.
func (e *rtEnv) emitter(t *testing.T) (Emitter, error) {
	t.Helper()
	if c := e.check(t); c.CheckError != "" {
		t.Fatalf("check: %s", c.CheckError)
	}
	return e.mgr.Emitter(context.Background(), rtLang)
}

func TestRuntimeLocate(t *testing.T) {
	good := t.TempDir()
	py := filepath.Join(good, "python")
	os.WriteFile(py, []byte("x"), 0o755)
	models := t.TempDir()
	plain := filepath.Join(good, "plain")
	os.WriteFile(plain, []byte("x"), 0o644)
	wwFile := filepath.Join(good, "ww")
	os.WriteFile(wwFile, []byte("x"), 0o755)
	os.Chmod(wwFile, 0o777)
	wwDir := t.TempDir()
	os.WriteFile(filepath.Join(wwDir, "python"), []byte("x"), 0o755)
	os.Chmod(wwDir, 0o777)
	t.Chdir(good)
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"ok", Config{Python: py, ModelsDir: models}, ""},
		{"empty", Config{ModelsDir: models}, CodePythonMissing},
		{"missing", Config{Python: filepath.Join(good, "nope"), ModelsDir: models}, CodePythonMissing},
		{"relative missing", Config{Python: "nope/python", ModelsDir: models}, CodePythonMissing},
		{"relative ok", Config{Python: "python", ModelsDir: models}, ""},
		{"not executable", Config{Python: plain, ModelsDir: models}, CodePythonUnsafe},
		{"world-writable file", Config{Python: wwFile, ModelsDir: models}, CodePythonUnsafe},
		{"world-writable dir", Config{Python: filepath.Join(wwDir, "python"), ModelsDir: models}, CodePythonUnsafe},
		{"directory", Config{Python: good, ModelsDir: models}, CodePythonUnsafe},
		{"models missing", Config{Python: py, ModelsDir: filepath.Join(good, "none")}, CodeModelsMissing},
		{"models unset", Config{Python: py}, CodeModelsMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Locate(tc.cfg)
			var ce *Error
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("err = %v", err)
			case tc.want != "" && (!errors.As(err, &ce) || ce.Code != tc.want):
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRuntimeManifest(t *testing.T) {
	h := strings.Repeat("a", 64)
	ok := `{"format":"tendshelf-emissions-model","version":1,"language":"en","repository":"r","revision":"v",` +
		`"licenceSpdx":"MIT","licenceUrl":"u","sourceUrl":"s","files":[{"name":"a.json","size":3,"sha256":"` + h + `"}]}`
	if m, err := ParseManifest([]byte(ok)); err != nil || m.Files[0].Name != "a.json" || m.Repository != "r" {
		t.Fatalf("ok: %+v %v", m, err)
	}
	bad := map[string]string{
		"format":    strings.Replace(ok, "tendshelf-emissions-model", "other", 1),
		"slash":     strings.Replace(ok, "a.json", "a/b", 1),
		"backslash": strings.Replace(ok, "a.json", `a\\b`, 1),
		"dotdot":    strings.Replace(ok, "a.json", "..a", 1),
		"short sha": strings.Replace(ok, h, "abc", 1),
		"upper sha": strings.Replace(ok, h, strings.Repeat("A", 64), 1),
		"no files":  strings.Replace(ok, `[{"name":"a.json","size":3,"sha256":"`+h+`"}]`, "[]", 1),
		"unknown":   strings.Replace(ok, `"version":1,`, `"version":1,"x":1,`, 1),
		"not json":  "{",
	}
	for name, in := range bad {
		if _, err := ParseManifest([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRuntimeModelSHA(t *testing.T) {
	a := ManifestFile{Name: "a", Size: 1, SHA256: strings.Repeat("1", 64)}
	b := ManifestFile{Name: "b", Size: 2, SHA256: strings.Repeat("2", 64)}
	want := sha256.Sum256([]byte("a\t1\t" + a.SHA256 + "\nb\t2\t" + b.SHA256 + "\n"))
	if got := ModelSHA([]ManifestFile{b, a}); got != hex.EncodeToString(want[:]) {
		t.Fatalf("sha = %s", got)
	}
	if ModelSHA([]ManifestFile{a, b}) != ModelSHA([]ManifestFile{b, a}) {
		t.Fatal("order matters")
	}
}

func TestRuntimeCheckOK(t *testing.T) {
	e := rtNew(t, "ok")
	c := e.check(t)
	if c.CheckError != "" || c.ID == "" || c.Language != rtLang || c.ModelSHA256 != ModelSHA(e.man.Files) {
		t.Fatalf("component = %+v", c)
	}
	if !strings.Contains(c.IdentityJSON, "tendshelf-emissions-v1") || c.LicenceSPDX != "Apache-2.0" {
		t.Fatalf("component = %+v", c)
	}
	py, _ := locate(e.cfg)
	if _, ok, err := e.comps.Usable(context.Background(), rtLang, py, e.cfg.workerSHA()); !ok || err != nil {
		t.Fatalf("usable = %v %v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(e.cfg.WorkDir, "worker", "worker-"+e.cfg.workerSHA()[:12]+".py")); err != nil {
		t.Fatal(err)
	}
	if again := e.check(t); again.ID != c.ID {
		t.Fatal("id changed on re-check")
	}
}

func TestRuntimeCheckMismatch(t *testing.T) {
	tests := []struct {
		name string
		edit func(dir string)
		want string
	}{
		{"missing file", func(d string) { os.Remove(filepath.Join(d, "config.json")) }, CodeModelsMissing},
		{"changed bytes", func(d string) { os.WriteFile(filepath.Join(d, "config.json"), []byte("{x"), 0o644) }, CodeModelMismatch},
		{"changed size", func(d string) { os.WriteFile(filepath.Join(d, "config.json"), []byte("{} "), 0o644) }, CodeModelMismatch},
		{"link", func(d string) {
			os.Remove(filepath.Join(d, "config.json"))
			os.Symlink(filepath.Join(d, "model.safetensors"), filepath.Join(d, "config.json"))
		}, CodeModelMismatch},
		{"folder missing", func(d string) { os.RemoveAll(d) }, CodeModelsMissing},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := rtNew(t, "ok")
			tc.edit(filepath.Join(e.cfg.ModelsDir, rtLang))
			c := e.check(t)
			if c.CheckError != tc.want {
				t.Fatalf("check_error = %q, want %q", c.CheckError, tc.want)
			}
			py, _ := locate(e.cfg)
			if _, ok, _ := e.comps.Usable(context.Background(), rtLang, py, e.cfg.workerSHA()); ok {
				t.Fatal("usable after a failed check")
			}
			if _, err := e.mgr.Emitter(context.Background(), rtLang); !errors.Is(err, ErrNotUsable) {
				t.Fatalf("emitter err = %v", err)
			}
		})
	}
}

func TestRuntimeCheckIdentity(t *testing.T) {
	e := rtNew(t, "bad-identity")
	if c := e.check(t); c.CheckError != CodeIdentityMismatch {
		t.Fatalf("check_error = %q", c.CheckError)
	}
	e = rtNew(t, "ok")
	e.cfg.Manifests[rtLang] = func() Manifest { m := e.man; m.Revision = "other"; return m }()
	if c := e.check(t); c.CheckError != CodeIdentityMismatch {
		t.Fatalf("revision: check_error = %q", c.CheckError)
	}
	e = rtNew(t, "ok")
	e.cfg.Python = "/nonexistent/python"
	if c := e.check(t); c.CheckError != CodePythonMissing {
		t.Fatalf("python: check_error = %q", c.CheckError)
	}
	e = rtNew(t, "no-ready")
	e.cfg.Env = []string{fakeworker.EnvSwitch + "=ok", "X=1"}
	e.cfg.DescribeTimeout = time.Second
	// A python that exits non-zero cannot describe.
	bad := filepath.Join(t.TempDir(), "python")
	os.WriteFile(bad, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	e.cfg.Python = bad
	if c := e.check(t); c.CheckError != CodeDescribeFailed {
		t.Fatalf("describe: check_error = %q", c.CheckError)
	}
	if got, ok, _ := e.comps.Get(context.Background(), rtLang); !ok || got.CheckError != CodeDescribeFailed {
		t.Fatalf("row = %+v", got)
	}
}

func TestRuntimeUsable(t *testing.T) {
	e := rtNew(t, "ok")
	c := e.check(t)
	ctx := context.Background()
	if _, ok, _ := e.comps.Usable(ctx, rtLang, c.PythonPath, c.WorkerSHA256); !ok {
		t.Fatal("not usable")
	}
	if _, ok, _ := e.comps.Usable(ctx, rtLang, c.PythonPath+"x", c.WorkerSHA256); ok {
		t.Fatal("usable with another python")
	}
	if _, ok, _ := e.comps.Usable(ctx, rtLang, c.PythonPath, strings.Repeat("0", 64)); ok {
		t.Fatal("usable with another worker")
	}
	if _, ok, _ := e.comps.Usable(ctx, "es", c.PythonPath, c.WorkerSHA256); ok {
		t.Fatal("usable for an unchecked language")
	}
	if err := e.comps.SetDisabled(ctx, rtLang, true); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.comps.Usable(ctx, rtLang, c.PythonPath, c.WorkerSHA256); ok {
		t.Fatal("usable when disabled")
	}
	// Disabled survives a re-check.
	if again := e.check(t); !again.Disabled {
		t.Fatal("re-check re-enabled the component")
	}
}

func TestRuntimeReady(t *testing.T) {
	e := rtNew(t, "ok")
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	id := em.Identity()
	if id.Protocol != Protocol || len(id.Vocab) != fakeworker.VocabSize || id.FrameMs != 20 || id.Language != rtLang {
		t.Fatalf("identity = %+v", id)
	}
}

func TestRuntimeReadyTimeout(t *testing.T) {
	e := rtNew(t, "no-ready")
	e.cfg.ReadyTimeout = 300 * time.Millisecond
	e.mgr = New(e.cfg, e.comps, nil)
	t.Cleanup(func() { e.mgr.Close() })
	// no-ready only affects serve, so the describe step passes.
	if _, err := e.emitter(t); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if e.mgr.pid() != 0 {
		t.Fatal("worker kept after a failed launch")
	}
}

func TestRuntimeNoise(t *testing.T) {
	e := rtNew(t, "noise")
	if _, err := e.emitter(t); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeNonce(t *testing.T) {
	e := rtNew(t, "bad-nonce")
	if _, err := e.emitter(t); !errors.Is(err, ErrIdentity) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeIdentity(t *testing.T) {
	e := rtNew(t, "ok")
	e.check(t)
	// The component says another model hash than the worker reports.
	c, _, _ := e.comps.Get(context.Background(), rtLang)
	c.ModelSHA256 = strings.Repeat("9", 64)
	if _, err := e.comps.Upsert(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	if _, err := e.mgr.Emitter(context.Background(), rtLang); !errors.Is(err, ErrIdentity) {
		t.Fatalf("err = %v", err)
	}
}

func TestRuntimeEmitOK(t *testing.T) {
	e := rtNew(t, "ok")
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	for _, samples := range []int{400, 16000, MaxSamples} {
		got, err := em.Emit(context.Background(), rtReq("req-1", samples))
		if err != nil {
			t.Fatalf("%d samples: %v", samples, err)
		}
		if got.Frames != expectedFrames(samples) || got.Vocab != fakeworker.VocabSize || got.FrameMs != 20 ||
			len(got.LogProbs) != got.Frames*got.Vocab {
			t.Fatalf("%d samples: %d frames, %d values", samples, got.Frames, len(got.LogProbs))
		}
		if d := math.Abs(float64(got.LogProbs[0]) + math.Log(32)); d > 0.01 {
			t.Fatalf("value = %v", got.LogProbs[0])
		}
	}
	if e.mgr.launches.Load() != 1 {
		t.Fatalf("launches = %d", e.mgr.launches.Load())
	}
}

func TestRuntimeEmitOversize(t *testing.T) {
	e := rtNew(t, "ok")
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := em.Emit(context.Background(), rtReq("big", MaxSamples+1)); !errors.Is(err, ErrRequestTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if e.mgr.pid() == 0 {
		t.Fatal("a refused request must not kill the worker")
	}
	if _, err := em.Emit(context.Background(), Request{ID: "bad id!", PCM: rtPCM(1000)}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := em.Emit(context.Background(), Request{ID: "short", PCM: rtPCM(10)}); !errors.Is(err, ErrProtocol) {
		t.Fatalf("short: %v", err)
	}
	// A worker answering with a payload past the cap is refused too.
	e2 := rtNew(t, "oversize")
	em2, err := e2.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := em2.Emit(context.Background(), rtReq("o", 16000)); !errors.Is(err, ErrProtocol) {
		t.Fatalf("oversize answer: %v", err)
	}
}

func TestRuntimeEmitBadFrames(t *testing.T) {
	e := rtNew(t, "bad-frames")
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := em.Emit(context.Background(), rtReq("f", 16000)); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
	if e.mgr.pid() != 0 {
		t.Fatal("worker kept after a protocol error")
	}
}

func TestRuntimeCrash(t *testing.T) {
	mark := filepath.Join(t.TempDir(), "mark")
	e := rtNew(t, "crash-once", fakeworker.EnvMark+"="+mark)
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := em.Emit(context.Background(), rtReq("c1", 16000)); !errors.Is(err, ErrWorkerCrashed) {
		t.Fatalf("err = %v", err)
	}
	// The next Emitter call relaunches and works.
	em, err = e.mgr.Emitter(context.Background(), rtLang)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := em.Emit(context.Background(), rtReq("c2", 16000)); err != nil {
		t.Fatal(err)
	}
	if e.mgr.launches.Load() != 2 {
		t.Fatalf("launches = %d", e.mgr.launches.Load())
	}
	if e.mgr.CPUTime() < 0 {
		t.Fatal("negative cpu time")
	}
}

func TestRuntimeTimeout(t *testing.T) {
	e := rtNew(t, "hang")
	e.cfg.RequestTimeout = 300 * time.Millisecond
	e.mgr = New(e.cfg, e.comps, nil)
	t.Cleanup(func() { e.mgr.Close() })
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	pid := e.mgr.pid()
	start := time.Now()
	if _, err := em.Emit(context.Background(), rtReq("h", 16000)); !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("deadline not enforced")
	}
	rtWaitGone(t, pid)
	// A cancelled context ends the request too.
	em, err = e.mgr.Emitter(context.Background(), rtLang)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := em.Emit(ctx, rtReq("h2", 16000)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ctx err = %v", err)
	}
}

func TestRuntimeOneAtATime(t *testing.T) {
	e := rtNew(t, "slow-ok")
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := em.Emit(context.Background(), rtReq("w"+strconv.Itoa(i), 2000))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if e.mgr.launches.Load() != 1 {
		t.Fatalf("launches = %d, want one shared worker", e.mgr.launches.Load())
	}
}

func rtWaitGone(t *testing.T, pid int) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
	}
	t.Fatalf("process %d still exists", pid)
}

func TestRuntimeIdleStop(t *testing.T) {
	e := rtNew(t, "ok")
	e.cfg.IdleStop = 200 * time.Millisecond
	e.mgr = New(e.cfg, e.comps, nil)
	t.Cleanup(func() { e.mgr.Close() })
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	pid := e.mgr.pid()
	if pid == 0 {
		t.Fatal("no worker")
	}
	rtWaitGone(t, pid)
	// Emit launches a new worker after the idle stop.
	if _, err := em.Emit(context.Background(), rtReq("i", 16000)); err != nil {
		t.Fatal(err)
	}
	if e.mgr.launches.Load() != 2 {
		t.Fatalf("launches = %d", e.mgr.launches.Load())
	}
}

// rtParentMain is the parent process of TestRuntimeParentDeath: it launches a worker, prints its pid
// and waits to be killed.
func rtParentMain() int {
	cfg := Config{
		Python: os.Args[0], ModelsDir: os.Getenv(rtParentSwitch), WorkDir: os.Getenv(rtParentSwitch + "_WORK"),
		Script: []byte("# fake\n"), Threads: 1, ReadyTimeout: 5 * time.Second,
		Env: []string{fakeworker.EnvSwitch + "=ok"},
	}
	m := New(cfg, nil, nil)
	py, _ := locate(cfg)
	p, err := m.launch(context.Background(), py, Component{Language: rtLang, ModelSHA256: os.Getenv(rtParentSwitch + "_SHA"), Revision: fakeworker.Revision})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(p.cmd.Process.Pid)
	time.Sleep(time.Hour)
	return 0
}

func TestRuntimeParentDeath(t *testing.T) {
	work := t.TempDir()
	models := filepath.Join(work, "models")
	man := rtModel(t, models)
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{rtParentSwitch + "=" + models, rtParentSwitch + "_WORK=" + filepath.Join(work, "align"),
		rtParentSwitch + "_SHA=" + ModelSHA(man.Files)}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	if _, err := fmt.Fscan(out, &pid); err != nil || pid <= 0 {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("no worker pid: %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("worker not running: %v", err)
	}
	cmd.Process.Kill()
	cmd.Wait()
	rtWaitGone(t, pid)
}

func TestRuntimeEnvCleared(t *testing.T) {
	t.Setenv("TENDSHELF_SECRET_TEST", "leak")
	e := rtNew(t, "env")
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, kv := range strings.Split(em.Identity().Versions["env"], "\n") {
		k, v, _ := strings.Cut(kv, "=")
		got[k] = v
	}
	work := e.cfg.WorkDir
	want := map[string]string{
		"LC_ALL": "C", "OMP_NUM_THREADS": "2", "MKL_NUM_THREADS": "2", "HF_HUB_OFFLINE": "1",
		"TRANSFORMERS_OFFLINE": "1", "HF_HOME": work + "/hf-home", "HOME": work + "/home", "TMPDIR": work + "/tmp",
		fakeworker.EnvSwitch: "env",
	}
	if len(got) != len(want)+1 { // plus the nonce
		t.Fatalf("env = %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if n := got["TENDSHELF_ALIGN_NONCE"]; len(n) != 64 {
		t.Errorf("nonce = %q", n)
	}
	if _, leaked := got["TENDSHELF_SECRET_TEST"]; leaked {
		t.Error("server environment leaked")
	}
	for _, d := range []string{"home", "tmp", "hf-home"} {
		if st, err := os.Stat(filepath.Join(work, d)); err != nil || st.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, st, err)
		}
	}
}

func TestRuntimeFloat16(t *testing.T) {
	tests := []struct {
		bits uint16
		want float64
	}{
		{0x0000, 0}, {0x3c00, 1}, {0xbc00, -1}, {0xc2ee, -3.46484375}, {0x0001, math.Pow(2, -24)},
		{0x03ff, 1023 * math.Pow(2, -24)}, {0x8001, -math.Pow(2, -24)}, {0x7bff, 65504}, {0xfbff, -65504},
		{0x0400, math.Pow(2, -14)}, {0x7c00, math.Inf(1)}, {0xfc00, math.Inf(-1)},
	}
	for _, tc := range tests {
		if got := float64(halfToFloat(tc.bits)); got != tc.want {
			t.Errorf("%#04x = %v, want %v", tc.bits, got, tc.want)
		}
	}
	if v := halfToFloat(0x7e00); !math.IsNaN(float64(v)) {
		t.Errorf("0x7e00 = %v, want NaN", v)
	}
	if v := halfToFloat(0x8000); v != 0 || !math.Signbit(float64(v)) {
		t.Errorf("-0 = %v", v)
	}
}

func TestRuntimeImportsNoNet(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatal("no files", err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, imp := range rtImports(t, f) {
			if imp == "net" || strings.HasPrefix(imp, "net/") {
				t.Errorf("%s imports %s", f, imp)
			}
		}
	}
	sub, _ := filepath.Glob("fakeworker/*.go")
	for _, f := range sub {
		for _, imp := range rtImports(t, f) {
			if imp == "net" || strings.HasPrefix(imp, "net/") {
				t.Errorf("%s imports %s", f, imp)
			}
		}
	}
}

func TestRuntimeCheckJob(t *testing.T) {
	e := rtNew(t, "ok")
	r, err := jobs.New(e.st, jobs.Options{PollEvery: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := Register(r, e.cfg, e.comps); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Stop(ctx) })
	id, err := r.Enqueue(ctx, KindCheck, CheckPayload{Language: rtLang}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		j, err := r.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == jobs.StateDone {
			if string(j.Result) != `{"code":""}` {
				t.Fatalf("result = %s", j.Result)
			}
			if c, ok, _ := e.comps.Get(ctx, rtLang); !ok || c.CheckError != "" {
				t.Fatalf("component = %+v", c)
			}
			return
		}
		if j.State == jobs.StateFailed {
			t.Fatalf("job failed: %s %s", j.ErrorCode, j.ErrorText)
		}
	}
	t.Fatal("job did not finish")
}
