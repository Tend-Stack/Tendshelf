package main

import (
	"flag"
	"path/filepath"
	"strings"
	"testing"
)

// TestAlignConfigDefaults checks the word-timing flags and the folders they default to, with no Python anywhere.
func TestAlignConfigDefaults(t *testing.T) {
	t.Setenv("TENDSHELF_ALIGN_PYTHON", "")
	t.Setenv("TENDSHELF_ALIGN_MODELS", "")
	t.Setenv("TENDSHELF_ALIGN_THREADS", "")
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	f := addAlignFlags(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	cfg, err := alignConfig(data, f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.Python, filepath.Join(data, "runtimes", "align", "venv")) || cfg.ModelsDir != filepath.Join(data, "runtimes", "align", "models") ||
		cfg.WorkDir != filepath.Join(data, "runtimes", "align") {
		t.Errorf("defaults: %+v", cfg)
	}
	if len(cfg.Script) == 0 || len(cfg.Manifests) != 2 || cfg.Manifests["en"].Language != "en" || cfg.Manifests["es"].Language != "es" {
		t.Errorf("worker script or manifests missing: %d bytes, %d manifests", len(cfg.Script), len(cfg.Manifests))
	}
	if *f.cacheGiB != 2 {
		t.Errorf("cache default: %d", *f.cacheGiB)
	}

	// Flags and the environment move the paths and the thread count.
	fs = flag.NewFlagSet("t", flag.ContinueOnError)
	f = addAlignFlags(fs)
	if err := fs.Parse([]string{"-align-python", "/opt/py/bin/python", "-align-models", "/opt/models", "-align-threads", "3"}); err != nil {
		t.Fatal(err)
	}
	if cfg, err = alignConfig(data, f); err != nil || cfg.Python != "/opt/py/bin/python" || cfg.ModelsDir != "/opt/models" || cfg.Threads != 3 {
		t.Errorf("flags: %+v %v", cfg, err)
	}
	t.Setenv("TENDSHELF_ALIGN_THREADS", "5")
	fs = flag.NewFlagSet("t", flag.ContinueOnError)
	f = addAlignFlags(fs)
	_ = fs.Parse(nil)
	if cfg, _ = alignConfig(data, f); cfg.Threads != 5 {
		t.Errorf("environment threads: %d", cfg.Threads)
	}
}
