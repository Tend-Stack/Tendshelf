package online

import "time"

// Pace of a bulk lookup. These are constants, not settings: politeness to a free community
// service is not a user preference, and one place holds them.
const (
	BulkGap    = 4 * time.Second // between books that sent a request
	BulkJitter = 2 * time.Second

	BackoffBase = 30 * time.Second // after a troubled book: 30s, 60s, 120s, ...
	BackoffCap  = 15 * time.Minute

	BreakerAfter    = 4                // consecutive troubled books pause the job
	BreakerPause    = 30 * time.Minute // 30m, 60m, 120m, 240m, 240m
	BreakerPauseCap = 4 * time.Hour
	BreakerMaxTrips = 5 // the 6th trip stops the job

	DailyCap    = 2000 // network requests per provider per UTC day
	DailyJitter = 10 * time.Minute

	BulkAudnexusBooks = 3 // Audnexus detail requests per book in a bulk lookup
)

// Outcome is how one book's lookup went, for pacing.
type Outcome int

const (
	OutcomeSent    Outcome = iota // at least one request went out
	OutcomeCached                 // nothing was sent
	OutcomeTrouble                // a site was busy, down or answered badly
)

// Pace tracks the run of troubled books of one job; it is saved with the job.
type Pace struct {
	Consecutive int `json:"consecutive"`
	Trips       int `json:"trips"`
}

// Wait is what to do before the next book. Reason is "", "pace", "backoff" or "pause".
type Wait struct {
	For    time.Duration
	Reason string
	Stop   bool
}

// Next records an outcome and says how long to wait. jitter is in [0,1).
func (p *Pace) Next(o Outcome, jitter float64) Wait {
	switch o {
	case OutcomeSent:
		p.Consecutive = 0
		return Wait{For: BulkGap + time.Duration(jitter*float64(BulkJitter)), Reason: "pace"}
	case OutcomeCached:
		p.Consecutive = 0
		return Wait{}
	}
	p.Consecutive++
	if p.Consecutive >= BreakerAfter {
		p.Consecutive = 0
		p.Trips++
		if p.Trips > BreakerMaxTrips {
			return Wait{Stop: true}
		}
		return Wait{For: shiftCap(BreakerPause, p.Trips-1, BreakerPauseCap), Reason: "pause"}
	}
	return Wait{For: shiftCap(BackoffBase, p.Consecutive-1, BackoffCap), Reason: "backoff"}
}

// shiftCap is base doubled n times, no more than limit.
func shiftCap(base time.Duration, n int, limit time.Duration) time.Duration {
	d := base
	for i := 0; i < n && d < limit; i++ {
		d *= 2
	}
	return min(d, limit)
}

// NextDay is the next 00:00 UTC after now plus jitter (in [0,1)) of DailyJitter: when a provider
// that hit its daily cap may be asked again.
func NextDay(now time.Time, jitter float64) time.Time {
	u := now.UTC()
	midnight := time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
	return midnight.Add(time.Duration(jitter * float64(DailyJitter)))
}

// Troubled says whether an error code means the site had trouble (as opposed to a plain "not found").
func Troubled(code string) bool {
	switch code {
	case "busy", "server", "offline", "bad_answer", "too_large":
		return true
	}
	return false
}
