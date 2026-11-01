package library

import "time"

// SetNow replaces the clock, so tests control created_at and updated_at.
func (r *Repo) SetNow(f func() time.Time) { r.now = f }

// SetShelfLimits lowers the shelf limits for one test and returns the restore function.
func SetShelfLimits(shelves, books int) (restore func()) {
	s, b := maxShelves, maxShelfBooks
	maxShelves, maxShelfBooks = shelves, books
	return func() { maxShelves, maxShelfBooks = s, b }
}
