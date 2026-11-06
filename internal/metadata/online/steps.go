package online

import (
	"context"
	"errors"
	"strings"
	"sync"
)

// MaxSteps is how many steps a lookup keeps. A lookup of every store and every Audible match fits;
// anything past it is still done, only not listed.
const MaxSteps = 30

// Step states.
const (
	StepWaiting = "wait"   // planned, not asked yet
	StepRunning = "run"    // being asked now
	StepDone    = "done"   // answered with at least one match
	StepNone    = "none"   // answered, nothing found
	StepFailed  = "failed" // the site could not answer; Code says why
)

// Step is one line of the lookup's log: one place Tendshelf asked (or will ask) and what it sent
// there. Query holds only the title, author, ASIN or ISBN, so the log doubles as a record of what
// left the server.
type Step struct {
	Provider string `json:"provider"`
	Label    string `json:"label"`
	Region   string `json:"region,omitempty"`
	Query    string `json:"query,omitempty"`
	State    string `json:"state"`
	Count    int    `json:"count,omitempty"`
	Code     string `json:"code,omitempty"`
	Cached   bool   `json:"cached,omitempty"` // answered from the saved copy, nothing was sent
	Sent     int    `json:"sent,omitempty"`   // requests this step really sent to the site
}

// recorder collects the steps of one lookup and hands a copy to emit after every change.
type recorder struct {
	mu         sync.Mutex
	steps      []Step
	emit       func([]Step)
	cur        int // index of the running step, -1 for none (or an unlisted one)
	calls, hit int // requests of the running step, and how many the saved copy answered
}

type recorderKey struct{}

func recorderFrom(ctx context.Context) *recorder {
	r, _ := ctx.Value(recorderKey{}).(*recorder)
	return r
}

// publish hands emit a copy; the caller holds r.mu.
func (r *recorder) publish() {
	if r.emit != nil {
		r.emit(append([]Step(nil), r.steps...))
	}
}

// snapshot is a copy of the steps so far.
func (r *recorder) snapshot() []Step {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Step(nil), r.steps...)
}

// plan lists the providers that are about to be asked as waiting steps.
func (r *recorder) plan(providers []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cur = -1
	for _, p := range providers {
		if len(r.steps) < MaxSteps {
			r.steps = append(r.steps, Step{Provider: p, Label: label(p), State: StepWaiting})
		}
	}
	r.publish()
}

// begin marks a step as running: the planned one of the provider when it is still waiting, else a
// new one placed after the provider's last step.
func (r *recorder) begin(provider, name, region, query string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st := Step{Provider: provider, Label: name, Region: region, Query: query, State: StepRunning}
	r.cur, r.calls, r.hit = -1, 0, 0
	last := -1
	for i, s := range r.steps {
		if s.Provider == provider {
			last = i
			if s.State == StepWaiting {
				r.steps[i], r.cur = st, i
				r.publish()
				return
			}
		}
	}
	if len(r.steps) >= MaxSteps {
		return
	}
	at := len(r.steps)
	if last >= 0 {
		at = last + 1
	}
	r.steps = append(r.steps, Step{})
	copy(r.steps[at+1:], r.steps[at:])
	r.steps[at], r.cur = st, at
	r.publish()
}

// end closes the running step with the number of matches it found, or with why it failed.
func (r *recorder) end(count int, err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cur < 0 {
		return
	}
	s := &r.steps[r.cur]
	switch {
	case err != nil:
		s.State, s.Code = StepFailed, errorCode(err)
	case count > 0:
		s.State, s.Count = StepDone, count
	default:
		s.State = StepNone
	}
	s.Sent = r.calls - r.hit
	s.Cached = err == nil && r.calls > 0 && r.hit == r.calls
	r.cur = -1
	r.publish()
}

// request notes one request of the running step and whether the saved copy answered it.
func (r *recorder) request(cached bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.calls++
	if cached {
		r.hit++
	}
	r.mu.Unlock()
}

// ask words what a step sends: the ASIN, the ISBN, or the title with the author.
func (q Query) ask() string {
	switch {
	case q.ASIN != "":
		return q.ASIN
	case q.ISBN != "":
		return q.ISBN
	}
	return q.titleAsk()
}

func (q Query) titleAsk() string {
	switch {
	case q.Title != "" && q.Author != "":
		return `"` + q.Title + `" by ` + q.Author
	case q.Title != "":
		return `"` + q.Title + `"`
	}
	return ""
}

// askFor words what one provider is sent: Open Library takes the ISBN before the title;
// the Apple store only the title and author.
func (q Query) askFor(provider string) string {
	switch provider {
	case "openlibrary":
		if q.ISBN != "" {
			return q.ISBN
		}
		return q.titleAsk()
	case "itunes":
		return q.titleAsk()
	}
	return q.ask()
}

func regionLabel(region string) string {
	if region == "" {
		return "US"
	}
	return strings.ToUpper(region)
}

func errorCode(err error) string {
	var oe *Error
	if errors.As(err, &oe) {
		return oe.Code
	}
	return "server"
}
