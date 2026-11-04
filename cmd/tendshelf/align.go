package main

import (
	"flag"
	"fmt"
	"path/filepath"
	goruntime "runtime"

	"github.com/Tend-Stack/tendshelf/internal/align"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	alignfiles "github.com/Tend-Stack/tendshelf/runtimes/align"
)

// alignFlags holds the command-line flags of read-along alignment (docs/plans/m5-r3-alignment.md, decision 3).
type alignFlags struct {
	python, models *string
	threads        *int
	cacheGiB       *int
}

// addAlignFlags declares the alignment flags before the command line is parsed.
func addAlignFlags(fs *flag.FlagSet) *alignFlags {
	return &alignFlags{
		python:   fs.String("align-python", envOr("TENDSHELF_ALIGN_PYTHON", ""), "Python interpreter for read-along word timing; default is <data>/runtimes/align/venv"),
		models:   fs.String("align-models", envOr("TENDSHELF_ALIGN_MODELS", ""), "folder of word-timing models, one subfolder per language (en, es); default is <data>/runtimes/align/models"),
		threads:  fs.Int("align-threads", envOrInt("TENDSHELF_ALIGN_THREADS", 0), "threads for word timing; default is half the processors, at most 4"),
		cacheGiB: fs.Int("align-cache-gib", envOrInt("TENDSHELF_ALIGN_CACHE_GIB", 2), "size cap of the word-timing cache in GiB (at least 1)"),
	}
}

// alignSetup is the alignment service, the runtime handle and the manager to close on shutdown.
type alignSetup struct {
	svc      *align.Service
	provider *alignrt.Provider
	mgr      *alignrt.Manager
}

// alignConfig is the runtime configuration from the flags and the data folder.
func alignConfig(dataDir string, f *alignFlags) (alignrt.Config, error) {
	work := filepath.Join(dataDir, "runtimes", "align")
	cfg := alignrt.Config{
		Python:    *f.python,
		ModelsDir: *f.models,
		WorkDir:   work,
		Script:    alignfiles.WorkerPy(),
		Manifests: map[string]alignrt.Manifest{},
		Threads:   *f.threads,
	}
	if cfg.Python == "" {
		if goruntime.GOOS == "windows" {
			cfg.Python = filepath.Join(work, "venv", "Scripts", "python.exe")
		} else {
			cfg.Python = filepath.Join(work, "venv", "bin", "python")
		}
	}
	if cfg.ModelsDir == "" {
		cfg.ModelsDir = filepath.Join(work, "models")
	}
	for _, lang := range []string{"en", "es"} {
		raw, ok := alignfiles.ManifestJSON(lang)
		if !ok {
			return cfg, fmt.Errorf("align: no manifest for %s", lang)
		}
		m, err := alignrt.ParseManifest(raw)
		if err != nil {
			return cfg, fmt.Errorf("align: %s manifest: %w", lang, err)
		}
		cfg.Manifests[lang] = m
	}
	return cfg, nil
}

// registerAlign wires read-along alignment: the component store, the check and align.book job kinds and the
// worker manager (which starts nothing until a job asks). A missing interpreter is one Info line, never an error.
func registerAlign(d deps, f *alignFlags, ebooks *ebook.Service) (*alignSetup, error) {
	cfg, err := alignConfig(d.dataDir, f)
	if err != nil {
		return nil, err
	}
	comps := alignrt.NewComponents(d.st)
	if err := alignrt.Register(d.runner, cfg, comps); err != nil {
		return nil, err
	}
	mgr := alignrt.New(cfg, comps, d.log)
	provider := &alignrt.Provider{Cfg: cfg, Comps: comps, Mgr: mgr}
	svc := align.NewService(align.Deps{
		Store: d.st, Library: d.lib, Ebooks: ebooks.Store(), Runner: d.runner, Runtimes: provider, Decoder: d.tools,
		DataDir: d.dataDir, CacheMaxBytes: int64(max(1, *f.cacheGiB)) << 30, Log: d.log,
	})
	if err := svc.Register(d.runner); err != nil {
		return nil, err
	}
	if err := alignrt.Locate(cfg); err != nil {
		d.log.Info("read-along word timing is not installed; /admin/runtimes shows how", "reason", err.Error())
	}
	return &alignSetup{svc: svc, provider: provider, mgr: mgr}, nil
}
