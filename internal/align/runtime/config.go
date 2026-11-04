// Package runtime checks and launches the optional alignment worker (decision 0001, amended by 0017).
// It is the only code that starts the worker. Importers alias it alignrt; inside it the standard
// library runtime package is imported as goruntime.
package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"
)

// Config says where the owner installed the runtime and how the worker runs.
type Config struct {
	Python    string // path of the Python interpreter (made absolute with filepath.Abs)
	ModelsDir string // one subfolder per language
	WorkDir   string // <data>/runtimes/align: worker script, HOME, TMPDIR, HF_HOME
	Script    []byte // the embedded worker.py
	Manifests map[string]Manifest
	Threads   int

	IdleStop, ReadyTimeout, RequestTimeout, DescribeTimeout time.Duration

	Env []string // extra KEY=VALUE pairs; tests only
}

// DefaultThreads is max(1, min(4, CPUs/2)).
func DefaultThreads() int {
	return max(1, min(4, goruntime.NumCPU()/2))
}

// withDefaults fills zero values.
func (c Config) withDefaults() Config {
	if c.Threads <= 0 {
		c.Threads = DefaultThreads()
	}
	if c.IdleStop <= 0 {
		c.IdleStop = 5 * time.Minute
	}
	if c.ReadyTimeout <= 0 {
		c.ReadyTimeout = 180 * time.Second
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 120 * time.Second
	}
	if c.DescribeTimeout <= 0 {
		c.DescribeTimeout = 180 * time.Second
	}
	return c
}

// workerSHA is the hex SHA-256 of the script.
func (c Config) workerSHA() string {
	sum := sha256.Sum256(c.Script)
	return hex.EncodeToString(sum[:])
}

// ManifestFile is one pinned model file.
type ManifestFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest pins one model: repository, revision, licence and the hash of every file.
type Manifest struct {
	Format      string         `json:"format"`
	Version     int            `json:"version"`
	Language    string         `json:"language"`
	Repository  string         `json:"repository"`
	Revision    string         `json:"revision"`
	LicenceSPDX string         `json:"licenceSpdx"`
	LicenceURL  string         `json:"licenceUrl"`
	SourceURL   string         `json:"sourceUrl"`
	Files       []ManifestFile `json:"files"`
}

// ManifestFormat is the only accepted Manifest.Format.
const ManifestFormat = "tendshelf-emissions-model"

// ParseManifest decodes and validates a model manifest.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("manifest: %w", err)
	}
	switch {
	case m.Format != ManifestFormat:
		return Manifest{}, errors.New("manifest: unknown format")
	case m.Version < 1:
		return Manifest{}, errors.New("manifest: bad version")
	case m.Language == "" || m.Repository == "" || m.Revision == "":
		return Manifest{}, errors.New("manifest: language, repository and revision are required")
	case len(m.Files) == 0:
		return Manifest{}, errors.New("manifest: no files")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if f.Name == "" || strings.ContainsAny(f.Name, `/\`) || strings.Contains(f.Name, "..") {
			return Manifest{}, fmt.Errorf("manifest: bad file name %q", f.Name)
		}
		if seen[f.Name] {
			return Manifest{}, fmt.Errorf("manifest: duplicate file %q", f.Name)
		}
		seen[f.Name] = true
		if f.Size < 0 || !isHex64(f.SHA256) {
			return Manifest{}, fmt.Errorf("manifest: bad size or hash for %q", f.Name)
		}
	}
	return m, nil
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && s == strings.ToLower(s)
}

// ModelSHA is the SHA-256 of "name\tsize\tsha256\n" lines sorted by name. The worker computes the same
// digest over the folder it loads, so a describe answer can be compared with the manifest.
func ModelSHA(files []ManifestFile) string {
	sorted := append([]ManifestFile(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	h := sha256.New()
	for _, f := range sorted {
		fmt.Fprintf(h, "%s\t%d\t%s\n", f.Name, f.Size, f.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Error is a stable check failure code. Codes are shown on /admin/runtimes.
type Error struct{ Code string }

func (e *Error) Error() string { return e.Code }

// Check failure codes.
const (
	CodePythonMissing    = "python_missing"
	CodePythonUnsafe     = "python_unsafe"
	CodeModelsMissing    = "models_missing"
	CodeModelMismatch    = "model_mismatch"
	CodeIdentityMismatch = "identity_mismatch"
	CodeDescribeFailed   = "describe_failed"
	CodeUnsupportedLang  = "unsupported_language"
)

// Locate finds the interpreter and the models folder. It does not run anything.
func Locate(cfg Config) error {
	_, err := locate(cfg)
	return err
}

// locate returns the absolute interpreter path.
func locate(cfg Config) (string, error) {
	if strings.TrimSpace(cfg.Python) == "" {
		return "", &Error{CodePythonMissing}
	}
	py, err := filepath.Abs(cfg.Python)
	if err != nil || !filepath.IsAbs(py) {
		return "", &Error{CodePythonMissing}
	}
	info, err := os.Stat(py) // follows the venv's link to the real interpreter
	if err != nil {
		return "", &Error{CodePythonMissing}
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o002 != 0 {
		return "", &Error{CodePythonUnsafe}
	}
	dir, err := os.Stat(filepath.Dir(py))
	if err != nil || dir.Mode().Perm()&0o002 != 0 {
		return "", &Error{CodePythonUnsafe}
	}
	if cfg.ModelsDir == "" {
		return "", &Error{CodeModelsMissing}
	}
	if m, err := os.Stat(cfg.ModelsDir); err != nil || !m.IsDir() {
		return "", &Error{CodeModelsMissing}
	}
	return py, nil
}
