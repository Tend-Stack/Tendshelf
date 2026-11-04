package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	alignfiles "github.com/Tend-Stack/tendshelf/runtimes/align"
)

// TestRuntimeReal runs the real worker. Opt-in: TENDSHELF_TEST_ALIGN_RUNTIME names a folder holding
// venv/ and models/<lang>/ as the owner installed them (the default <data>/runtimes/align layout).
func TestRuntimeReal(t *testing.T) {
	root := os.Getenv("TENDSHELF_TEST_ALIGN_RUNTIME")
	if root == "" {
		t.Skip("set TENDSHELF_TEST_ALIGN_RUNTIME to a runtimes/align folder to run the real worker")
	}
	e := rtNew(t, "ok")
	raw, ok := alignfiles.ManifestJSON(rtLang)
	if !ok {
		t.Fatal("no manifest")
	}
	man, err := ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	e.cfg.Python = filepath.Join(root, "venv", "bin", "python")
	e.cfg.ModelsDir = filepath.Join(root, "models")
	e.cfg.Script = alignfiles.WorkerPy()
	e.cfg.Manifests = map[string]Manifest{rtLang: man}
	e.cfg.Env = nil
	e.cfg.ReadyTimeout, e.cfg.RequestTimeout, e.cfg.DescribeTimeout = 3*time.Minute, 3*time.Minute, 5*time.Minute
	e.mgr = New(e.cfg, e.comps, nil)
	t.Cleanup(func() { e.mgr.Close() })
	em, err := e.emitter(t)
	if err != nil {
		t.Fatal(err)
	}
	got, err := em.Emit(context.Background(), rtReq("real-1", 16000))
	if err != nil {
		t.Fatal(err)
	}
	if got.Frames != expectedFrames(16000) || got.Vocab != len(em.Identity().Vocab) {
		t.Fatalf("emissions = %d frames x %d", got.Frames, got.Vocab)
	}
}
