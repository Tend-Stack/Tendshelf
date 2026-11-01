package library

import "time"

// SetNow replaces the clock, so tests control created_at and updated_at.
func (r *Repo) SetNow(f func() time.Time) { r.now = f }
