// Package alignfiles embeds the alignment worker script, the pinned model manifests and the
// hash-locked requirements. The worker script is written to the data folder at launch by
// internal/align/runtime, so protocol and worker always match the binary. Nothing here
// downloads anything; the owner installs Python packages and model files by hand.
package alignfiles

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed worker.py models/*.json *.lock pyproject.toml
var files embed.FS

// downloads is the allowlist served on /admin/runtimes.
var downloads = map[string]bool{
	"requirements-torch.lock": true,
	"requirements-pypi.lock":  true,
	"pyproject.toml":          true,
	"models/en.json":          true,
	"models/es.json":          true,
}

// WorkerPy returns the embedded worker script.
func WorkerPy() []byte {
	b, err := files.ReadFile("worker.py")
	if err != nil {
		panic("alignfiles: worker.py not embedded: " + err.Error())
	}
	return b
}

// WorkerSHA256 is the hex SHA-256 of the worker script.
func WorkerSHA256() string {
	sum := sha256.Sum256(WorkerPy())
	return hex.EncodeToString(sum[:])
}

// ManifestJSON returns the model manifest for a language ("en", "es").
func ManifestJSON(lang string) ([]byte, bool) {
	if lang != "en" && lang != "es" {
		return nil, false
	}
	b, err := files.ReadFile("models/" + lang + ".json")
	return b, err == nil
}

// Download returns an allowlisted file and its content type.
func Download(name string) ([]byte, string, bool) {
	if !downloads[name] {
		return nil, "", false
	}
	b, err := files.ReadFile(name)
	if err != nil {
		return nil, "", false
	}
	return b, "text/plain; charset=utf-8", true
}
