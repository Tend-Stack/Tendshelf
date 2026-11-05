package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

const readingStatsRoute = "/me/reading"

func (s *server) registerReadingStats(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/reading", s.getReadingStats)
	mux.HandleFunc("POST /me/reading/goal", s.postReadingGoal)
}

// readingIn is everything the page is drawn from. Every number is the signed-in person's own.
type readingIn struct {
	R        progress.Reading
	Done     []library.FinishedBook
	Goal     users.ReadingGoal
	YearDone int // books finished in the calendar year of today, for the goal
	Today    time.Time
	Loc      *time.Location
	CSRF     string
	GoalErr  string
}

func (s *server) getReadingStats(w http.ResponseWriter, r *http.Request) {
	s.renderReadingStats(w, r, http.StatusOK, "")
}

// renderReadingStats is "Your reading" (G2): the person's own reading over a week, a month or a year.
// Nothing in the request names a person. A finished book's title shows only through CardsByID with the
// person's scope, so a revoked book disappears from the covers and stays in the count.
func (s *server) renderReadingStats(w http.ResponseWriter, r *http.Request, status int, goalErr string) {
	ctx := r.Context()
	u := currentUser(r)
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		s.serverError(w, r, "reading stats", err)
		return
	}
	loc := statsLocation(set.TimeZone)
	p, today, ok := s.listeningPeriod(w, r, loc)
	if !ok {
		return
	}
	rd, err := s.progress.ReadingStats(ctx, u.ID, loc, p, s.now())
	if err != nil {
		s.serverError(w, r, "reading stats", err)
		return
	}
	in := readingIn{R: rd, Today: today, Loc: loc, CSRF: s.csrf.TokenFrom(ctx), GoalErr: goalErr}
	if rd.Ever {
		if in.Goal, err = s.users.LoadReadingGoal(ctx, u.ID); err != nil {
			s.serverError(w, r, "reading stats", err)
			return
		}
		year := time.Date(today.Year(), time.January, 1, 0, 0, 0, 0, loc)
		if in.YearDone, err = s.progress.CountReadingFinished(ctx, u.ID, year.UnixMilli(), year.AddDate(1, 0, 0).UnixMilli()); err != nil {
			s.serverError(w, r, "reading stats", err)
			return
		}
		finishes, err := s.progress.ReadingFinishes(ctx, u.ID, p.From.UnixMilli(), p.To.UnixMilli())
		if err != nil {
			s.serverError(w, r, "reading stats", err)
			return
		}
		ids := make([]string, len(finishes))
		for i, f := range finishes {
			ids[i] = f.BookID
		}
		cards, err := s.lib.CardsByID(ctx, s.scope(r), ids)
		if err != nil {
			s.serverError(w, r, "reading stats", err)
			return
		}
		at := map[string]int64{}
		for _, f := range finishes {
			at[f.BookID] = f.At
		}
		for _, c := range cards {
			in.Done = append(in.Done, library.FinishedBook{Book: c, At: at[c.ID]})
		}
	}
	s.pageStyled(w, r, status, readTitle, "profile", pages.Listening(readingView(in)), nil, "css/pages-home.css", "css/pages-listening.css")
}

// postReadingGoal saves the yearly goal. A plain form answers 303 back to the page; a number outside 0 to 365
// is 422, a toast for htmx and the page with a sentence otherwise.
func (s *server) postReadingGoal(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	u := currentUser(r)
	n, err := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("goal_books")))
	goal := users.ReadingGoal{Books: n}
	if err != nil || goal.Validate() != nil {
		if r.Header.Get("HX-Request") == "true" {
			s.fragment(w, r, http.StatusUnprocessableEntity, components.ErrorToast(readGoalInvalid))
			return
		}
		s.renderReadingStats(w, r, http.StatusUnprocessableEntity, readGoalInvalid)
		return
	}
	if err := s.users.SaveReadingGoal(r.Context(), u.ID, goal); err != nil {
		s.serverError(w, r, "save reading goal", err)
		return
	}
	http.Redirect(w, r, readingStatsRoute, http.StatusSeeOther)
}

// readingView turns the numbers into the page, on the listening page's pieces.
func readingView(in readingIn) pages.ListeningView {
	rd := in.R
	p := rd.Period
	cur, _ := progress.PeriodOf(p.Range, in.Today, in.Loc)
	isCurrent := p.First.Equal(cur.First)
	v := pages.ListeningView{
		Back: listenBack, BackHref: "/me", Title: readTitle, Lead: statsPrivate, RangeLabel: listenRangeLabel,
		PeriodLabel: periodLabel(p),
	}
	if !rd.Ever {
		v.Empty = &pages.ListeningEmpty{Title: readEmptyTitle, Body: readEmptyBody, Action: readEmptyAction, Href: "/libraries"}
		return v
	}
	for _, r := range []struct{ rng, label string }{{progress.RangeWeek, listenWeek}, {progress.RangeMonth, listenMonth}, {progress.RangeYear, listenYear}} {
		v.Ranges = append(v.Ranges, pages.ListeningRange{Label: r.label, Current: r.rng == p.Range,
			Href: periodHref(readingStatsRoute, r.rng, p.First, isCurrent)})
	}
	if earlier := p.Shift(-1, in.Loc); !before2000(earlier) {
		v.Earlier = pages.ListeningStep{Label: fmt.Sprintf(listenShowFmt, periodLabel(earlier)), Href: periodHref(readingStatsRoute, p.Range, earlier.First, false)}
	}
	if !isCurrent {
		later := p.Shift(1, in.Loc)
		v.Later = pages.ListeningStep{Label: fmt.Sprintf(listenShowFmt, periodLabel(later)),
			Href: periodHref(readingStatsRoute, p.Range, later.First, later.First.Equal(cur.First))}
	}
	if rd.TotalMs == 0 {
		v.Quiet = fmt.Sprintf(readQuietFmt, periodLabel(p))
		return v
	}
	v.Nums = []pages.ListeningNum{
		{Label: readNumTime, Value: weekTime(rd.TotalMs)},
		{Label: readNumDays, Value: fmt.Sprintf(listenNumOfFmt, rd.DaysRead, rd.DaysSoFar)},
		{Label: readNumSpeed, Value: readSpeed(rd.Cpm)},
		{Label: readNumFinished, Value: strconv.Itoa(rd.Finished)},
	}
	var cards []pages.ListeningCard
	add := func(c pages.ListeningCard) {
		c.ID = "rd" + strconv.Itoa(len(cards)+1)
		cards = append(cards, c)
	}
	say := fmt.Sprintf(readSayTotFmt, weekTime(rd.TotalMs), periodLabel(p))
	if rd.DaysRead > 0 {
		say = fmt.Sprintf(readSayAvgFmt, weekTime(rd.TotalMs), periodLabel(p), weekTime(rd.TotalMs/int64(rd.DaysRead)))
	}
	chart := barsByPeriod(p, rd.Days, rd.DaysSoFar, in.Today)
	if p.Range == progress.RangeYear {
		chart.Caption = readCapMonth
	} else {
		chart.Caption = readCapDay
	}
	add(pages.ListeningCard{Title: readTimeTitle, Say: say, Wide: true, Chart: &chart})
	add(pages.ListeningCard{Title: readGoalTitle, Say: goalSay(in), Wide: true, Goal: goalView(in)})
	if len(in.Done) > 0 {
		var done []pages.ListeningDone
		for _, f := range in.Done {
			done = append(done, pages.ListeningDone{Cover: cardView(f.Book), Href: "/books/" + f.Book.ID, Title: f.Book.Title,
				Date: time.UnixMilli(f.At).In(in.Loc).Format("2 Jan")})
		}
		add(pages.ListeningCard{Title: readDoneTitle, Wide: true, Done: done})
	}
	v.Cards = cards
	return v
}

func goalSay(in readingIn) string {
	if in.Goal.Books == 0 {
		return readGoalNone
	}
	return fmt.Sprintf(readGoalSayFmt, in.YearDone, in.Goal.Books)
}

func goalView(in readingIn) *pages.ReadingGoalView {
	g := &pages.ReadingGoalView{
		Action: readingStatsRoute + "/goal", CSRF: in.CSRF, FieldLabel: readGoalField, Save: readGoalSave,
		Max: strconv.Itoa(users.MaxReadingGoal), Error: in.GoalErr,
		Percent: goalPercent(in.YearDone, in.Goal.Books),
	}
	if in.Goal.Books > 0 {
		g.Value = strconv.Itoa(in.Goal.Books)
		g.BarLabel = fmt.Sprintf(readGoalBarFmt, in.YearDone, in.Goal.Books)
	}
	return g
}
