package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/users"
	alignfiles "github.com/Tend-Stack/tendshelf/runtimes/align"
)

// artLab is an app with the word-timing runtime wired in and no Python anywhere: the interpreter is a file
// the test writes (or not), never run.
type artLab struct {
	a      *testApp
	owner  *http.Cookie
	cfg    alignrt.Config
	comps  *alignrt.Components
	python string
}

// artConfig is the runtime configuration the tests share: the real manifests and worker script, an
// interpreter path under the test's folder and a models folder beside it.
func artConfig(t *testing.T) alignrt.Config {
	t.Helper()
	dir := t.TempDir()
	cfg := alignrt.Config{
		Python: filepath.Join(dir, "venv", "bin", "python"), ModelsDir: filepath.Join(dir, "models"),
		WorkDir: filepath.Join(dir, "work"), Script: alignfiles.WorkerPy(), Manifests: map[string]alignrt.Manifest{}, Threads: 2,
	}
	for _, lang := range []string{"en", "es"} {
		raw, _ := alignfiles.ManifestJSON(lang)
		m, err := alignrt.ParseManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Manifests[lang] = m
	}
	return cfg
}

func newArtLab(t *testing.T) *artLab {
	t.Helper()
	a := newTestApp(t)
	l := &artLab{a: a, cfg: artConfig(t)}
	l.python = l.cfg.Python
	l.owner = a.as(t, users.RoleOwner)
	l.comps = alignrt.NewComponents(a.st)
	if err := alignrt.Register(a.runner, l.cfg, l.comps); err != nil {
		t.Fatal(err)
	}
	a.srv.runtimes = &alignrt.Provider{Cfg: l.cfg, Comps: l.comps}
	return l
}

// install writes an interpreter file and the models folder, so the runtime counts as set up.
func (l *artLab) install(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(l.python), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.python, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(l.cfg.ModelsDir, 0o755); err != nil {
		t.Fatal(err)
	}
}

// checked stores a component row for lang as a check would have left it.
func (l *artLab) checked(t *testing.T, lang, checkError string) {
	t.Helper()
	sum := sha256.Sum256(l.cfg.Script)
	man := l.cfg.Manifests[lang]
	py, _ := filepath.Abs(l.python)
	_, err := l.comps.Upsert(context.Background(), alignrt.Component{
		Kind: alignrt.KindEmissionsModel, Name: man.Repository, Language: lang, Revision: man.Revision,
		ModelSHA256: alignrt.ModelSHA(man.Files), FilesJSON: "[]", IdentityJSON: "{}", PythonPath: py,
		WorkerSHA256: hex.EncodeToString(sum[:]), LicenceSPDX: man.LicenceSPDX, LicenceURL: man.LicenceURL,
		SourceURL: man.SourceURL, CheckError: checkError,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (l *artLab) get(path string, htmx bool, ck *http.Cookie) *http.Response {
	return l.a.do(http.MethodGet, path, nil, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck).Result()
}

func (l *artLab) body(path string) string {
	rec := l.a.do(http.MethodGet, path, nil, nil, l.owner)
	return rec.Body.String()
}

func (l *artLab) post(t *testing.T, path string, form url.Values, htmx bool, ck *http.Cookie) *http.Response {
	t.Helper()
	form.Set("_csrf", l.a.tokenFor(t, ck))
	return l.a.do(http.MethodPost, path, form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck).Result()
}

func TestAdminRuntimesForbidden(t *testing.T) {
	l := newArtLab(t)
	for _, role := range []users.Role{users.RoleAdmin, users.RoleMember, users.RoleGuest} {
		ck := l.a.as(t, role)
		for _, p := range []string{"/admin/runtimes", "/admin/runtimes/files/requirements-torch.lock"} {
			if r := l.get(p, false, ck); r.StatusCode != http.StatusNotFound {
				t.Errorf("%s GET %s: %d", role, p, r.StatusCode)
			}
		}
		if r := l.post(t, "/admin/runtimes/check", url.Values{"language": {"en"}}, false, ck); r.StatusCode != http.StatusNotFound {
			t.Errorf("%s POST check: %d", role, r.StatusCode)
		}
	}
	// Signed out is sent to sign in, never shown the page.
	if r := l.get("/admin/runtimes", false, nil); r.StatusCode == http.StatusOK {
		t.Errorf("signed out: %d", r.StatusCode)
	}
	// The link is for the owner only, and only where the runtime is wired in.
	if strings.Contains(l.a.do(http.MethodGet, "/me", nil, nil, l.a.as(t, users.RoleAdmin)).Body.String(), "/admin/runtimes") {
		t.Error("an admin is shown the word timing link")
	}
	if !strings.Contains(l.body("/me"), `href="/admin/runtimes"`) || !strings.Contains(l.body("/me/settings"), `href="/admin/runtimes"`) {
		t.Error("the owner is not shown the word timing link")
	}
	// Without the runtime wired in, even the owner gets a 404.
	l.a.srv.runtimes = nil
	if r := l.get("/admin/runtimes", false, l.owner); r.StatusCode != http.StatusNotFound {
		t.Errorf("not wired: %d", r.StatusCode)
	}
}

func TestAdminRuntimesPage(t *testing.T) {
	l := newArtLab(t)
	// No Python at all: the page says so and still renders everything else.
	body := l.body("/admin/runtimes")
	for _, want := range []string{"Word timing", "English", "Spanish", "Not set up", "Word timing is not installed.", "No Python", "facebook/wav2vec2-base-960h",
		"22aad52d435eb6dbaf354bdad9b0da84ce7d6156", "Apache-2.0", "https://huggingface.co/facebook/wav2vec2-base-960h/tree/22aad52d435eb6dbaf354bdad9b0da84ce7d6156",
		"View on Hugging Face", "How to install it", "uv venv --python 3.12.14", "--require-hashes --no-deps --index-url https://download.pytorch.org/whl/cpu",
		"https://huggingface.co/jonatasgrosman/wav2vec2-large-xlsr-53-spanish/resolve/80106547eee5065e01618d6588c22c256e8d0e70/",
		"/admin/runtimes/files/requirements-torch.lock", "2 to 4 minutes"} {
		if want == "No Python" {
			want = "found no Python"
		}
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	if strings.Contains(body, "Ready</span>") {
		t.Error("a runtime nobody installed is called ready")
	}
	// Installed but never checked.
	l.install(t)
	if body = l.body("/admin/runtimes"); strings.Count(body, "Needs a check") != 2 {
		t.Errorf("needs a check: %d", strings.Count(body, "Needs a check"))
	}
	// English checked clean, Spanish failed a model hash.
	l.checked(t, "en", "")
	l.checked(t, "es", alignrt.CodeModelMismatch)
	body = l.body("/admin/runtimes")
	for _, want := range []string{"Ready", "Word timing for English is ready.", "Problem", "A model file is missing or is not the pinned version"} {
		if !strings.Contains(body, want) {
			t.Errorf("checked page misses %q", want)
		}
	}
	// A component checked against another interpreter needs a check again.
	if _, err := l.a.st.Writer().Exec(`UPDATE runtime_components SET python_path = '/elsewhere/python' WHERE language = 'en'`); err != nil {
		t.Fatal(err)
	}
	if body = l.body("/admin/runtimes"); !strings.Contains(body, "Needs a check") {
		t.Error("a component of another interpreter is still ready")
	}
	// Switched off.
	l.checked(t, "en", "")
	if err := l.comps.SetDisabled(context.Background(), "en", true); err != nil {
		t.Fatal(err)
	}
	if body = l.body("/admin/runtimes"); !strings.Contains(body, "switched off") {
		t.Error("a switched off component is not said to be")
	}
	// The page fetches nothing: the only outside addresses are links and text.
	if strings.Contains(body, "<script src=\"http") || strings.Contains(body, "hx-get=\"http") {
		t.Error("the page loads something from outside")
	}
}

func TestAdminRuntimesCheck(t *testing.T) {
	l := newArtLab(t)
	// A check is queued and the page comes back with that card checking.
	r := l.post(t, "/admin/runtimes/check", url.Values{"language": {"es"}}, false, l.owner)
	loc := r.Header.Get("Location")
	if r.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/admin/runtimes?check=") {
		t.Fatalf("check: %d %q", r.StatusCode, loc)
	}
	id := strings.TrimPrefix(loc, "/admin/runtimes?check=")
	var kind, payload, state string
	if err := l.a.st.Reader().QueryRow(`SELECT kind, payload_json, state FROM jobs WHERE id = ?`, id).Scan(&kind, &payload, &state); err != nil ||
		kind != alignrt.KindCheck || state != "queued" || !strings.Contains(payload, `"es"`) {
		t.Fatalf("job: %q %q %q %v", kind, payload, state, err)
	}
	body := l.body(loc)
	if !strings.Contains(body, `hx-get="/hx/jobs/`+id+`"`) || !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "Checking Spanish.") {
		t.Errorf("page does not poll: %d", strings.Count(body, "every 2s"))
	}
	if strings.Count(body, "every 2s") != 1 {
		t.Errorf("polling cards: %d", strings.Count(body, "every 2s"))
	}
	// The poll answers with the card alone, still checking while the job waits.
	rec := l.a.do(http.MethodGet, "/hx/jobs/"+id, nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, l.owner)
	frag := rec.Body.String()
	if rec.Code != http.StatusOK || strings.Contains(frag, "<html") || !strings.Contains(frag, `id="runtime-card-es"`) || !strings.Contains(frag, "every 2s") {
		t.Fatalf("poll: %d %s", rec.Code, frag)
	}
	// htmx gets the page itself at 200 with the address pushed.
	hr := l.post(t, "/admin/runtimes/check", url.Values{"language": {"en"}}, true, l.owner)
	if hr.StatusCode != http.StatusOK || !strings.HasPrefix(hr.Header.Get("HX-Push-Url"), "/admin/runtimes?check=") {
		t.Errorf("htmx check: %d %q", hr.StatusCode, hr.Header.Get("HX-Push-Url"))
	}
	// The job ends: the card has no more polling and shows the state in words.
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'done' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	l.checked(t, "es", alignrt.CodeModelsMissing)
	frag = l.a.do(http.MethodGet, "/hx/jobs/"+id, nil, nil, l.owner).Body.String()
	if strings.Contains(frag, "every 2s") || !strings.Contains(frag, "Not set up") {
		t.Errorf("finished poll: %s", frag)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET state = 'failed', error_code = 'x' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if frag = l.a.do(http.MethodGet, "/hx/jobs/"+id, nil, nil, l.owner).Body.String(); !strings.Contains(frag, "The check did not finish.") {
		t.Errorf("failed poll: %s", frag)
	}
	// Anybody else cannot read the job.
	for _, role := range []users.Role{users.RoleAdmin, users.RoleMember, users.RoleGuest} {
		if rr := l.get("/hx/jobs/"+id, true, l.a.as(t, role)); rr.StatusCode != http.StatusNotFound {
			t.Errorf("%s reads the check job: %d", role, rr.StatusCode)
		}
	}
	// Bad languages: 422, a toast for htmx and the page with the sentence otherwise. Nothing is queued.
	var before, after int
	_ = l.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, alignrt.KindCheck).Scan(&before)
	for _, lang := range []string{"", "fr", "EN", "en/../es"} {
		pr := l.post(t, "/admin/runtimes/check", url.Values{"language": {lang}}, false, l.owner)
		if pr.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("language %q: %d", lang, pr.StatusCode)
		}
	}
	rec = l.a.do(http.MethodPost, "/admin/runtimes/check", url.Values{"language": {"fr"}, "_csrf": {l.a.tokenFor(t, l.owner)}},
		func(r *http.Request) { r.Header.Set("HX-Request", "true") }, l.owner)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Choose English or Spanish.") || !strings.Contains(rec.Body.String(), "ts-toast") {
		t.Errorf("htmx bad language: %d %s", rec.Code, rec.Body.String())
	}
	_ = l.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, alignrt.KindCheck).Scan(&after)
	if after != before {
		t.Errorf("bad languages queued %d jobs", after-before)
	}
}

func TestAdminRuntimesFiles(t *testing.T) {
	l := newArtLab(t)
	for _, name := range []string{"requirements-torch.lock", "requirements-pypi.lock", "pyproject.toml", "models/en.json", "models/es.json"} {
		want, ctype, ok := alignfiles.Download(name)
		if !ok {
			t.Fatalf("bundle lacks %s", name)
		}
		rec := l.a.do(http.MethodGet, "/admin/runtimes/files/"+name, nil, nil, l.owner)
		if rec.Code != http.StatusOK || rec.Body.String() != string(want) || rec.Header().Get("Content-Type") != ctype {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
	for _, name := range []string{"worker.py", "go.mod", "models/fr.json", "models", "", "..%2Fgo.mod", "%2e%2e%2f%2e%2e%2fgo.mod", "models%2F..%2Fworker.py", "requirements-torch.lock/"} {
		rec := l.a.do(http.MethodGet, "/admin/runtimes/files/"+name, nil, nil, l.owner)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q: %d", name, rec.Code)
		}
	}
	// Cleaned paths are sent away by the router, never served.
	rec := l.a.do(http.MethodGet, "/admin/runtimes/files/../../go.mod", nil, nil, l.owner)
	if rec.Code == http.StatusOK {
		t.Errorf("dot-dot path served: %d", rec.Code)
	}
}

// runtimesSeedStates wires the word-timing runtime into an app for the accessibility audit and the captures:
// no Python is installed, so both cards say "Not set up" and the install steps are open.
func runtimesSeedStates(t *testing.T, a *testApp) {
	t.Helper()
	cfg := artConfig(t)
	comps := alignrt.NewComponents(a.st)
	if err := alignrt.Register(a.runner, cfg, comps); err != nil {
		t.Fatal(err)
	}
	a.srv.runtimes = &alignrt.Provider{Cfg: cfg, Comps: comps}
}
