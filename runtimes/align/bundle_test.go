package alignfiles

import (
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func TestWorkerBundleManifests(t *testing.T) {
	want := map[string]string{
		"en": "facebook/wav2vec2-base-960h@22aad52d435eb6dbaf354bdad9b0da84ce7d6156",
		"es": "jonatasgrosman/wav2vec2-large-xlsr-53-spanish@80106547eee5065e01618d6588c22c256e8d0e70",
	}
	for lang, pin := range want {
		raw, ok := ManifestJSON(lang)
		if !ok {
			t.Fatalf("%s: no manifest", lang)
		}
		var m struct {
			Format      string `json:"format"`
			Version     int    `json:"version"`
			Language    string `json:"language"`
			Repository  string `json:"repository"`
			Revision    string `json:"revision"`
			LicenceSPDX string `json:"licenceSpdx"`
			LicenceURL  string `json:"licenceUrl"`
			SourceURL   string `json:"sourceUrl"`
			Files       []struct {
				Name   string `json:"name"`
				Size   int64  `json:"size"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		if m.Format != "tendshelf-emissions-model" || m.Version != 1 || m.Language != lang {
			t.Errorf("%s: header %q %d %q", lang, m.Format, m.Version, m.Language)
		}
		if got := m.Repository + "@" + m.Revision; got != pin {
			t.Errorf("%s: pin %s, want %s", lang, got, pin)
		}
		url := "https://huggingface.co/" + m.Repository + "/tree/" + m.Revision
		if m.LicenceSPDX != "Apache-2.0" || m.LicenceURL != url || m.SourceURL != url {
			t.Errorf("%s: licence fields %q %q %q", lang, m.LicenceSPDX, m.LicenceURL, m.SourceURL)
		}
		seen := map[string]bool{}
		for _, f := range m.Files {
			if f.Name == "" || strings.ContainsAny(f.Name, `/\`) || strings.Contains(f.Name, "..") {
				t.Errorf("%s: unsafe name %q", lang, f.Name)
			}
			if !hex64.MatchString(f.SHA256) || f.Size <= 0 {
				t.Errorf("%s: %s bad size or hash", lang, f.Name)
			}
			seen[f.Name] = true
		}
		for _, need := range []string{"model.safetensors", "config.json", "vocab.json", "preprocessor_config.json"} {
			if !seen[need] {
				t.Errorf("%s: %s missing", lang, need)
			}
		}
	}
	if _, ok := ManifestJSON("fr"); ok {
		t.Error("unknown language must not resolve")
	}
}

func TestWorkerBundleLocks(t *testing.T) {
	for _, name := range []string{"requirements-torch.lock", "requirements-pypi.lock"} {
		raw, _, ok := Download(name)
		if !ok {
			t.Fatalf("%s not embedded", name)
		}
		var packages, hashes int
		for _, line := range strings.Split(string(raw), "\n") {
			trim := strings.TrimSpace(line)
			switch {
			case trim == "" || strings.HasPrefix(trim, "#"):
			case strings.HasPrefix(trim, "--hash=sha256:"):
				hashes++
				digest := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trim, "--hash=sha256:"), "\\"))
				if !hex64.MatchString(digest) {
					t.Errorf("%s: bad hash line %q", name, trim)
				}
			default:
				if !strings.Contains(trim, "==") {
					t.Errorf("%s: not pinned with ==: %q", name, trim)
				}
				packages++
			}
		}
		if packages == 0 || hashes < packages {
			t.Errorf("%s: %d packages, %d hashes", name, packages, hashes)
		}
	}
}

func TestWorkerBundleProtocol(t *testing.T) {
	script := string(WorkerPy())
	if !strings.Contains(script, `PROTOCOL = "tendshelf-emissions-v1"`) {
		t.Error("worker.py lacks the protocol string")
	}
	if !hex64.MatchString(WorkerSHA256()) {
		t.Error("worker sha is not 64 hex")
	}
	for _, banned := range []string{"http.server", "urllib", "requests", "socketserver"} {
		if strings.Contains(script, "import "+banned) || strings.Contains(script, "from "+banned) {
			t.Errorf("worker.py imports %s", banned)
		}
	}
}

func TestWorkerBundleDownload(t *testing.T) {
	for _, n := range []string{"requirements-torch.lock", "requirements-pypi.lock", "pyproject.toml", "models/en.json", "models/es.json"} {
		if b, ct, ok := Download(n); !ok || len(b) == 0 || ct != "text/plain; charset=utf-8" {
			t.Errorf("%s not served", n)
		}
	}
	for _, n := range []string{"worker.py", "../bundle.go", "models/fr.json", ""} {
		if _, _, ok := Download(n); ok {
			t.Errorf("%q must not be served", n)
		}
	}
}

// TestWorkerBundlePython runs the pure-Python protocol checks. Opt-in: set
// TENDSHELF_TEST_ALIGN_RUNTIME to a Python 3 interpreter.
func TestWorkerBundlePython(t *testing.T) {
	python := os.Getenv("TENDSHELF_TEST_ALIGN_RUNTIME")
	if python == "" {
		t.Skip("TENDSHELF_TEST_ALIGN_RUNTIME not set")
	}
	out, err := exec.Command(python, "-I", "test_worker.py").CombinedOutput()
	if err != nil {
		t.Fatalf("test_worker.py: %v\n%s", err, out)
	}
}
