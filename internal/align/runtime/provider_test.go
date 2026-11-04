package runtime

import (
	"context"
	"testing"
)

// TestRuntimeProviderHealth walks one language through the states the admin page shows, with the fake worker.
func TestRuntimeProviderHealth(t *testing.T) {
	ctx := context.Background()
	e := rtNew(t, "ok")
	p := &Provider{Cfg: e.cfg, Comps: e.comps, Mgr: e.mgr}
	health := func(p *Provider) Health {
		h, err := p.Health(ctx, rtLang)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	// Installed but never checked.
	if h := health(p); h.State != HealthNeedsCheck || h.Found {
		t.Fatalf("before the check: %+v", h)
	}
	if _, ok, err := p.Usable(ctx, rtLang); err != nil || ok {
		t.Fatalf("usable before the check: %v %v", ok, err)
	}
	// Checked clean.
	e.check(t)
	if h := health(p); h.State != HealthReady || h.Code != "" || !h.Found {
		t.Fatalf("after the check: %+v", h)
	}
	c, ok, err := p.Usable(ctx, rtLang)
	if err != nil || !ok || c.Language != rtLang {
		t.Fatalf("usable after the check: %v %v %+v", ok, err, c)
	}
	// Switched off, then on again.
	if err := e.comps.SetDisabled(ctx, rtLang, true); err != nil {
		t.Fatal(err)
	}
	if h := health(p); h.State != HealthProblem || h.Code != CodeDisabled {
		t.Fatalf("disabled: %+v", h)
	}
	if _, ok, _ := p.Usable(ctx, rtLang); ok {
		t.Error("a switched off component is usable")
	}
	if err := e.comps.SetDisabled(ctx, rtLang, false); err != nil {
		t.Fatal(err)
	}
	// A different worker script means the check no longer counts.
	other := *p
	other.Cfg.Script = []byte("# another script\n")
	if h := health(&other); h.State != HealthNeedsCheck {
		t.Errorf("another worker script: %+v", h)
	}
	if _, ok, _ := other.Usable(ctx, rtLang); ok {
		t.Error("a component checked with another script is usable")
	}
	// No interpreter: not set up, and never usable. The code says why.
	gone := *p
	gone.Cfg.Python = e.cfg.Python + "-missing"
	if h := health(&gone); h.State != HealthNotSetUp || h.Code != CodePythonMissing {
		t.Errorf("no interpreter: %+v", h)
	}
	if _, ok, err := gone.Usable(ctx, rtLang); ok || err != nil {
		t.Errorf("usable without an interpreter: %v %v", ok, err)
	}
	gone.Cfg.Python = ""
	if h := health(&gone); h.State != HealthNotSetUp {
		t.Errorf("empty interpreter: %+v", h)
	}
	// A failed check is a problem with its code.
	bad := *p
	man := e.man
	man.Files = append([]ManifestFile(nil), e.man.Files...)
	man.Files[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	bad.Cfg.Manifests = map[string]Manifest{rtLang: man}
	if _, err := Check(ctx, bad.Cfg, e.comps, rtLang); err != nil {
		t.Fatal(err)
	}
	if h := health(&bad); h.State != HealthProblem || h.Code != CodeModelMismatch {
		t.Errorf("failed check: %+v", h)
	}
}
