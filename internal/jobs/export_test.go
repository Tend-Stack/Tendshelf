package jobs

import "context"

// ProgressWrites is how many progress writes the keepers have made.
func (r *Runner) ProgressWrites() int64 { return r.progress.Load() }

// ClaimOne claims one due job of kind without starting the runner. It returns
// the claimed job id, or "" when none is due.
func (r *Runner) ClaimOne(ctx context.Context, kind string) (string, error) {
	r.mu.Lock()
	k := r.kinds[kind]
	if r.base == nil {
		r.base, r.cancel = context.WithCancelCause(context.Background())
	}
	r.mu.Unlock()
	a, err := r.claim(ctx, k)
	if err != nil || a == nil {
		return "", err
	}
	r.mu.Lock()
	delete(r.active, a.id)
	r.mu.Unlock()
	a.cancel(nil)
	return a.id, nil
}
