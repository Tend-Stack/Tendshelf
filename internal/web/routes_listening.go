package web

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

const (
	listeningTopBooks  = 5
	listeningTopPeople = 3
	listeningDoneLimit = 12
	listeningMinYear   = 2000 // an `at` before this is not a date anyone listened on
)

func (s *server) registerListening(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/listening", s.getListening)
	mux.HandleFunc("GET /me/listening/history", s.getTimeline)
}

// getListening is "Your listening" (decision 0014): the signed-in person's own listening over a week,
// a month or a year. Nothing in the request names a person; `range` and `at` pick the period. An
// unknown range, an unparseable `at` or one before 2000 is 404; an `at` after today shows the current period.
func (s *server) getListening(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := currentUser(r)
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "listening", err)
		return
	}
	loc := statsLocation(set.TimeZone)
	now := s.now()
	p, today, ok := s.listeningPeriod(w, r, loc)
	if !ok {
		return
	}
	rng := p.Range

	l, err := s.progress.Listening(ctx, u.ID, loc, p, now)
	if err != nil {
		s.serverError(w, r, "listening", err)
		return
	}
	in := listeningIn{L: l, Today: today, Loc: loc}
	if l.Ever {
		from, to := p.From.UnixMilli(), p.To.UnixMilli()
		if in.Top, err = s.lib.TopListened(ctx, s.scope(r), u.ID, from, to, listeningTopBooks, listeningTopPeople); err != nil {
			s.serverError(w, r, "listening", err)
			return
		}
		if in.Done, err = s.lib.FinishedBetween(ctx, s.scope(r), u.ID, from, to, listeningDoneLimit); err != nil {
			s.serverError(w, r, "listening", err)
			return
		}
		if rng == progress.RangeYear {
			if in.Years, err = s.progress.YearTotals(ctx, u.ID, loc, now); err != nil {
				s.serverError(w, r, "listening", err)
				return
			}
			if in.Since, err = s.firstListened(r, u.ID, loc); err != nil {
				s.serverError(w, r, "listening", err)
				return
			}
		}
	}
	s.pageStyled(w, r, http.StatusOK, listenTitle, "profile", pages.Listening(listeningView(in)), nil, "css/pages-home.css", "css/pages-listening.css")
}

// listeningPeriod reads `range` and `at` and returns the period they pick and today's civil date in loc
// (UTC midnight). It answers 404 itself and reports false for an unknown range or a bad `at`; an `at` after
// today gives the current period. Both stats pages use it.
func (s *server) listeningPeriod(w http.ResponseWriter, r *http.Request, loc *time.Location) (progress.Period, time.Time, bool) {
	ly, lm, ld := s.now().In(loc).Date()
	today := time.Date(ly, lm, ld, 0, 0, 0, 0, time.UTC)
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = progress.RangeMonth
	}
	day := today
	if at := r.URL.Query().Get("at"); at != "" {
		parsed, err := time.Parse("2006-01-02", at)
		if err != nil || parsed.Year() < listeningMinYear {
			s.notFound(w, r)
			return progress.Period{}, today, false
		}
		day = parsed
	}
	p, err := progress.PeriodOf(rng, day, loc)
	if err != nil {
		s.notFound(w, r)
		return progress.Period{}, today, false
	}
	if cur, _ := progress.PeriodOf(rng, today, loc); p.First.After(cur.First) {
		p = cur
	}
	return p, today, true
}

// firstListened is the start of the person's first session, in their zone. YearTotals returns years
// only, and "Listening since March 2024" needs the month. The read is one MIN over listening_sessions_user.
func (s *server) firstListened(r *http.Request, userID string, loc *time.Location) (time.Time, error) {
	var first sql.NullInt64
	err := s.lib.Store().Reader().QueryRowContext(r.Context(), `SELECT MIN(started_at) FROM listening_sessions WHERE user_id = ?`, userID).Scan(&first)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, err
	}
	if !first.Valid {
		return time.Time{}, nil
	}
	return time.UnixMilli(first.Int64).In(loc), nil
}
