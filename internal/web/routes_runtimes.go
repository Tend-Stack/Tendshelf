package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
	alignfiles "github.com/Tend-Stack/tendshelf/runtimes/align"
)

// The word-timing page (M5 R3 P5): the owner sees whether the speech model that times read-along is
// installed and checked, presses Check, and finds the install steps. Owner only (the server's settings);
// everyone else gets a 404. Tendshelf never downloads anything here: the page shows text and links.

// runtimeLangs are the languages with a pinned model, in the order the cards show them.
var runtimeLangs = []struct{ Code, Name, Time string }{
	{"en", runtimesEnglish, runtimesTimeEn},
	{"es", runtimesSpanish, runtimesTimeEs},
}

func (s *server) registerRuntimes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/runtimes", s.getRuntimes)
	mux.HandleFunc("POST /admin/runtimes/check", s.postRuntimesCheck)
	mux.HandleFunc("GET /admin/runtimes/files/{name...}", s.getRuntimeFile)
}

// runtimesGate is the page's rule: the server's settings, and a server that has the runtime wired in.
func (s *server) runtimesGate(w http.ResponseWriter, r *http.Request) bool {
	if !s.allow(w, r, share.ManageServer, share.Target{Kind: share.TargetServer}) {
		return false
	}
	if s.runtimes == nil {
		s.notFound(w, r)
		return false
	}
	return true
}

// runtimeLangName is the display name of a language code the card knows.
func runtimeLangName(code string) (string, bool) {
	for _, l := range runtimeLangs {
		if l.Code == code {
			return l.Name, true
		}
	}
	return "", false
}

// shq quotes a path for the install commands.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runtimeStateLine says in plain words why a language is in the state it is in.
func (s *server) runtimeStateLine(h alignrt.Health, name string) (label, line string) {
	cfg := s.runtimes.Cfg
	py, _ := filepath.Abs(cfg.Python)
	models, _ := filepath.Abs(cfg.ModelsDir)
	switch h.State {
	case alignrt.HealthReady:
		return runtimesStateReady, fmt.Sprintf(runtimesLineReady, name, name)
	case alignrt.HealthNeedsCheck:
		return runtimesStateCheck, runtimesLineNeeds
	case alignrt.HealthNotSetUp:
		switch h.Code {
		case alignrt.CodePythonMissing:
			return runtimesStateNotSetUp, fmt.Sprintf(runtimesLineNoPython, py)
		case alignrt.CodePythonUnsafe:
			return runtimesStateNotSetUp, fmt.Sprintf(runtimesLineUnsafe, py)
		case alignrt.CodeModelsMissing:
			return runtimesStateNotSetUp, fmt.Sprintf(runtimesLineNoModels, models)
		}
		return runtimesStateNotSetUp, runtimesLineNotSetUp
	}
	switch h.Code {
	case alignrt.CodeModelsMissing:
		return runtimesStateProblem, fmt.Sprintf(runtimesLineModels, name, models)
	case alignrt.CodeModelMismatch:
		return runtimesStateProblem, runtimesLineMismatch
	case alignrt.CodeIdentityMismatch:
		return runtimesStateProblem, runtimesLineIdentity
	case alignrt.CodeDescribeFailed:
		return runtimesStateProblem, runtimesLineDescribe
	case alignrt.CodeDisabled:
		return runtimesStateProblem, runtimesLineOff
	case alignrt.CodePythonUnsafe:
		return runtimesStateProblem, fmt.Sprintf(runtimesLineUnsafe, py)
	}
	return runtimesStateProblem, runtimesLineOther
}

// runtimeCard builds the card of one language. pendingJob is the id of a check that is running for it.
func (s *server) runtimeCard(r *http.Request, code string, pendingJob string, failed bool) (pages.RuntimeCardView, error) {
	name, _ := runtimeLangName(code)
	h, err := s.runtimes.Health(r.Context(), code)
	if err != nil {
		return pages.RuntimeCardView{}, err
	}
	label, line := s.runtimeStateLine(h, name)
	v := pages.RuntimeCardView{
		Lang: code, Name: name, State: h.State, StateLabel: label, Line: line,
		CheckAction: "/admin/runtimes/check", CheckLabel: runtimesCheck, CheckAria: fmt.Sprintf(runtimesCheckAria, name),
		CSRF: s.csrf.TokenFrom(r.Context()),
	}
	if pendingJob != "" {
		v.Pending, v.PollHref = true, "/hx/jobs/"+url.PathEscape(pendingJob)
		v.ResultHref, v.ResultLabel = "/admin/runtimes?check="+url.QueryEscape(pendingJob), runtimesCheckLink
		v.Line = fmt.Sprintf(runtimesChecking, name)
	} else if failed {
		v.Line, v.Failed = runtimesCheckFailed, true
	}
	cfg := s.runtimes.Cfg
	if m, ok := cfg.Manifests[code]; ok {
		var size int64
		for _, f := range m.Files {
			size += f.Size
		}
		est := ""
		for _, l := range runtimeLangs {
			if l.Code == code {
				est = l.Time
			}
		}
		threads := cfg.Threads
		if threads <= 0 {
			threads = alignrt.DefaultThreads()
		}
		py, _ := filepath.Abs(cfg.Python)
		v.Facts = []pages.RuntimeFactView{
			{Label: runtimesFactModel, Value: m.Repository},
			{Label: runtimesFactRev, Value: m.Revision, Mono: true},
			{Label: runtimesFactSize, Value: sizeText(size)},
			{Label: runtimesFactLicence, Value: m.LicenceSPDX, Href: m.LicenceURL},
			{Label: runtimesFactPage, Value: runtimesPageText, Href: m.SourceURL},
			{Label: runtimesFactPython, Value: py, Mono: true},
			{Label: runtimesFactThreads, Value: fmt.Sprint(threads)},
			{Label: runtimesFactTime, Value: est},
		}
	}
	return v, nil
}

// runtimeSteps are the owner's install steps with the paths of this server filled in.
func (s *server) runtimeSteps() []pages.RuntimeStepView {
	cfg := s.runtimes.Cfg
	py, _ := filepath.Abs(cfg.Python)
	models, _ := filepath.Abs(cfg.ModelsDir)
	venv := filepath.Dir(filepath.Dir(py))
	step3 := make([]string, 0, len(runtimeLangs))
	for _, l := range runtimeLangs {
		m, ok := cfg.Manifests[l.Code]
		if !ok {
			continue
		}
		names := make([]string, len(m.Files))
		for i, f := range m.Files {
			names[i] = f.Name
		}
		step3 = append(step3, fmt.Sprintf("for f in %s; do curl -fL -o %s/\"$f\" https://huggingface.co/%s/resolve/%s/\"$f\"; done",
			strings.Join(names, " "), shq(filepath.Join(models, l.Code)), m.Repository, m.Revision))
	}
	file := func(name string) pages.RuntimeFileView {
		return pages.RuntimeFileView{Label: name, Href: "/admin/runtimes/files/" + name}
	}
	return []pages.RuntimeStepView{
		{Text: runtimesStep1, CodeLabel: fmt.Sprintf(runtimesStepCode, 1),
			Code: fmt.Sprintf("mkdir -p %s/en %s/es\nuv venv --python 3.12.14 %s", shq(models), shq(models), shq(venv))},
		{Text: runtimesStep2, CodeLabel: fmt.Sprintf(runtimesStepCode, 2),
			Files: []pages.RuntimeFileView{file("requirements-torch.lock"), file("requirements-pypi.lock")},
			Code: fmt.Sprintf("uv pip install --python %s --require-hashes --no-deps --index-url https://download.pytorch.org/whl/cpu -r requirements-torch.lock\nuv pip install --python %s --require-hashes --no-deps -r requirements-pypi.lock",
				shq(py), shq(py))},
		{Text: runtimesStep3, CodeLabel: fmt.Sprintf(runtimesStepCode, 3), Code: strings.Join(step3, "\n")},
		{Text: runtimesStep4},
	}
}

// runtimesView builds the page. check is the id of a check to show as running.
func (s *server) runtimesView(r *http.Request, check string, errLine string) (pages.AdminRuntimesView, error) {
	v := pages.AdminRuntimesView{
		Lead: runtimesLead, Title: runtimesTitle, Intro: runtimesIntro, Error: errLine,
		StepsTitle: runtimesStepsTitle, StepsLead: runtimesStepsLead, Steps: s.runtimeSteps(),
	}
	pendingLang := ""
	if check != "" {
		if lang, st, ok := s.runtimeCheckJob(r, check); ok && (st == jobs.StateQueued || st == jobs.StateRunning) {
			pendingLang = lang
		}
	}
	for _, l := range runtimeLangs {
		job := ""
		if l.Code == pendingLang {
			job = check
		}
		c, err := s.runtimeCard(r, l.Code, job, false)
		if err != nil {
			return v, err
		}
		v.Cards = append(v.Cards, c)
	}
	return v, nil
}

// runtimeCheckJob reads an align.check job: the language it checks and its state.
func (s *server) runtimeCheckJob(r *http.Request, id string) (string, jobs.State, bool) {
	j, err := s.jobs.Get(r.Context(), id)
	if err != nil || j.Kind != alignrt.KindCheck {
		return "", "", false
	}
	var p alignrt.CheckPayload
	if json.Unmarshal(j.Payload, &p) != nil {
		return "", "", false
	}
	if _, ok := runtimeLangName(p.Language); !ok {
		return "", "", false
	}
	return p.Language, j.State, true
}

func (s *server) renderRuntimes(w http.ResponseWriter, r *http.Request, status int, v pages.AdminRuntimesView) {
	s.prefsPage(w, r, status, runtimesTitle, "admin-runtimes", "runtimes", false, pages.AdminRuntimes(v), nil, pages.RuntimesStyles...)
}

func (s *server) getRuntimes(w http.ResponseWriter, r *http.Request) {
	if !s.runtimesGate(w, r) {
		return
	}
	v, err := s.runtimesView(r, r.URL.Query().Get("check"), "")
	if err != nil {
		s.serverError(w, r, "word timing page", err)
		return
	}
	s.renderRuntimes(w, r, http.StatusOK, v)
}

// postRuntimesCheck queues a check of one language and shows the page with that card checking.
func (s *server) postRuntimesCheck(w http.ResponseWriter, r *http.Request) {
	if !s.runtimesGate(w, r) || !s.parseForm(w, r) {
		return
	}
	lang := r.PostFormValue("language")
	if _, ok := runtimeLangName(lang); !ok {
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(runtimesBadLanguage))
			return
		}
		v, err := s.runtimesView(r, "", runtimesBadLanguage)
		if err != nil {
			s.serverError(w, r, "word timing page", err)
			return
		}
		s.renderRuntimes(w, r, http.StatusUnprocessableEntity, v)
		return
	}
	id, err := s.jobs.Enqueue(r.Context(), alignrt.KindCheck, alignrt.CheckPayload{Language: lang}, jobs.EnqueueOptions{CreatedBy: currentUser(r).ID})
	if err != nil {
		s.serverError(w, r, "queue word timing check", err)
		return
	}
	s.answerPage(w, r, "/admin/runtimes?check="+url.QueryEscape(id))
}

// getRuntimeCheckJob is the card of the language a check job is for: checking while it runs, then the
// result. The owner only; anyone else gets "no such job".
func (s *server) getRuntimeCheckJob(w http.ResponseWriter, r *http.Request, j jobs.Job) {
	if !s.runtimesGate(w, r) {
		return
	}
	lang, state, ok := s.runtimeCheckJob(r, j.ID)
	if !ok {
		s.notFound(w, r)
		return
	}
	pending := ""
	if state == jobs.StateQueued || state == jobs.StateRunning {
		pending = j.ID
	}
	c, err := s.runtimeCard(r, lang, pending, state == jobs.StateFailed || state == jobs.StateCancelled)
	if err != nil {
		s.serverError(w, r, "word timing card", err)
		return
	}
	s.fragment(w, r, http.StatusOK, pages.RuntimeCard(c))
}

// getRuntimeFile serves one pinned file (the lock files and the model manifests) as text so the owner can
// save it. Only names the bundle lists; anything else is a 404.
func (s *server) getRuntimeFile(w http.ResponseWriter, r *http.Request) {
	if !s.runtimesGate(w, r) {
		return
	}
	body, ctype, ok := alignfiles.Download(r.PathValue("name"))
	if !ok {
		s.notFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "private, no-store")
	_, _ = w.Write(body)
}
