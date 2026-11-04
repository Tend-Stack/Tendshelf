package runtime

import (
	"context"
	"errors"
)

// Provider joins the configuration, the component store and the worker manager into the one handle the
// alignment service and the admin page use. It never starts anything on its own: a missing interpreter or
// model only makes a language unusable.
type Provider struct {
	Cfg   Config
	Comps *Components
	Mgr   *Manager
}

// Health states of one language, in the order the admin page decides them.
const (
	HealthNotSetUp   = "not_set_up"  // no interpreter or no models folder: nothing to check yet
	HealthNeedsCheck = "needs_check" // never checked, or checked against another interpreter or worker script
	HealthProblem    = "problem"     // the last check failed (Code says why) or the owner switched it off
	HealthReady      = "ready"
)

// CodeDisabled is the Health code of a component the owner switched off.
const CodeDisabled = "disabled"

// Health is what the admin page shows for one language.
type Health struct {
	State     string
	Code      string    // why: a locate or check code, empty when ready
	Component Component // the stored row, valid when Found
	Found     bool
}

// Usable returns the checked component for lang when it may be used now. An unusable runtime is not an error.
func (p *Provider) Usable(ctx context.Context, lang string) (Component, bool, error) {
	py, err := locate(p.Cfg)
	if err != nil {
		return Component{}, false, nil
	}
	return p.Comps.Usable(ctx, lang, py, p.Cfg.workerSHA())
}

// Emitter starts (or reuses) the worker for lang.
func (p *Provider) Emitter(ctx context.Context, lang string) (Emitter, error) {
	return p.Mgr.Emitter(ctx, lang)
}

// Health decides the state of one language.
func (p *Provider) Health(ctx context.Context, lang string) (Health, error) {
	var h Health
	c, found, err := p.Comps.Get(ctx, lang)
	if err != nil {
		return h, err
	}
	h.Component, h.Found = c, found
	py, lerr := locate(p.Cfg)
	if lerr != nil {
		h.State = HealthNotSetUp
		var e *Error
		if errors.As(lerr, &e) {
			h.Code = e.Code
		}
		return h, nil
	}
	switch {
	case !found || c.PythonPath != py || c.WorkerSHA256 != p.Cfg.workerSHA():
		h.State = HealthNeedsCheck
	case c.CheckError != "":
		h.State, h.Code = HealthProblem, c.CheckError
	case c.Disabled:
		h.State, h.Code = HealthProblem, CodeDisabled
	default:
		h.State = HealthReady
	}
	return h, nil
}
