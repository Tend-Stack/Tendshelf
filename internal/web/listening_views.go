package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// listeningIn is everything the page is drawn from. Today is the person's local date as UTC midnight,
// the way progress.Period.First is written.
type listeningIn struct {
	L     progress.Listening
	Top   library.TopListened
	Done  []library.FinishedBook
	Years []progress.YearTotal
	Since time.Time // the person's first session, in their zone; zero when unknown
	Today time.Time
	Loc   *time.Location
}

// listeningHref is the address of a period: the current one needs no date.
func listeningHref(rng string, first time.Time, current bool) string {
	return periodHref("/me/listening", rng, first, current)
}

// periodHref is listeningHref for any stats page.
func periodHref(base, rng string, first time.Time, current bool) string {
	if current {
		return base + "?range=" + rng
	}
	return base + "?range=" + rng + "&at=" + first.Format("2006-01-02")
}

func before2000(p progress.Period) bool { return p.First.Year() < listeningMinYear }

// listeningView turns the numbers into the page. It names no book except in Most listened and Finished,
// which came through the scope SQL.
func listeningView(in listeningIn) pages.ListeningView {
	l := in.L
	p := l.Period
	cur, _ := progress.PeriodOf(p.Range, in.Today, in.Loc)
	isCurrent := p.First.Equal(cur.First)
	v := pages.ListeningView{
		Back: listenBack, BackHref: "/me", Title: listenTitle, Lead: statsPrivate, RangeLabel: listenRangeLabel,
		PeriodLabel: periodLabel(p),
	}
	if !l.Ever {
		v.Empty = &pages.ListeningEmpty{Title: listenEmptyTitle, Body: listenEmptyBody, Action: listenEmptyAction, Href: "/libraries"}
		return v
	}
	for _, r := range []struct{ rng, label string }{{progress.RangeWeek, listenWeek}, {progress.RangeMonth, listenMonth}, {progress.RangeYear, listenYear}} {
		// A range link keeps the day being looked at, so Week from September shows a week in September.
		v.Ranges = append(v.Ranges, pages.ListeningRange{Label: r.label, Current: r.rng == p.Range,
			Href: listeningHref(r.rng, p.First, isCurrent)})
	}
	if earlier := p.Shift(-1, in.Loc); !before2000(earlier) {
		v.Earlier = pages.ListeningStep{Label: fmt.Sprintf(listenShowFmt, periodLabel(earlier)), Href: listeningHref(p.Range, earlier.First, false)}
	}
	if !isCurrent {
		later := p.Shift(1, in.Loc)
		v.Later = pages.ListeningStep{Label: fmt.Sprintf(listenShowFmt, periodLabel(later)),
			Href: listeningHref(p.Range, later.First, later.First.Equal(cur.First))}
	}
	if l.TotalMs == 0 {
		v.Quiet = fmt.Sprintf(listenQuietFmt, periodLabel(p))
		return v
	}
	v.Nums = []pages.ListeningNum{
		{Label: listenNumListened, Value: weekTime(l.TotalMs)},
		{Label: listenNumDays, Value: fmt.Sprintf(listenNumOfFmt, l.DaysListened, l.DaysSoFar)},
		{Label: listenNumFinished, Value: strconv.Itoa(l.Finished)},
		{Label: listenNumSpeed, Value: speedText(l.SpeedPct)},
	}
	var cards []pages.ListeningCard
	add := func(c pages.ListeningCard) {
		c.ID = "ld" + strconv.Itoa(len(cards)+1)
		cards = append(cards, c)
	}
	if p.Range == progress.RangeYear {
		heat := heatGrid(in)
		say := fmt.Sprintf(listenDaysInFmt, l.DaysListened, periodLabel(p))
		if l.DaysListened == 1 {
			say = fmt.Sprintf(listenOneDayInFmt, periodLabel(p))
		}
		add(pages.ListeningCard{Title: listenDayByDay, Say: say, Wide: true, Heat: &heat})
	}
	timeSay := fmt.Sprintf(listenSayTotFmt, weekTime(l.TotalMs), periodLabel(p))
	if l.DaysListened > 0 {
		timeSay = fmt.Sprintf(listenSayAvgFmt, weekTime(l.TotalMs), periodLabel(p), weekTime(l.TotalMs/int64(l.DaysListened)))
	}
	timeChart := timeChart(in)
	add(pages.ListeningCard{Title: listenTimeTitle, Say: timeSay, Wide: p.Range == progress.RangeWeek, Chart: &timeChart})
	if p.Range != progress.RangeWeek {
		wd, name := weekdayChart(l)
		add(pages.ListeningCard{Title: listenWeekdaysTitle, Say: fmt.Sprintf(listenMostDayFmt, name), Chart: &wd})
	}
	clock := clockCard(l)
	add(pages.ListeningCard{Title: listenDayTitle, Say: clock.say, Wide: true, Clock: &clock.view})
	if lists := topLists(in.Top); len(lists) > 0 {
		add(pages.ListeningCard{Title: listenMostTitle, Wide: true, Lists: lists})
	}
	if len(in.Done) > 0 {
		var done []pages.ListeningDone
		for _, f := range in.Done {
			done = append(done, pages.ListeningDone{Cover: cardView(f.Book), Href: "/books/" + f.Book.ID, Title: f.Book.Title,
				Date: time.UnixMilli(f.At).In(in.Loc).Format("2 Jan")})
		}
		add(pages.ListeningCard{Title: listenDoneTitle, Wide: true, Done: done})
	}
	if p.Range == progress.RangeYear {
		if yc, ok := yearsChart(in); ok {
			add(pages.ListeningCard{Title: listenYearByYear, Say: fmt.Sprintf(listenSinceFmt, in.Since.Format("January 2006")), Wide: true, Chart: &yc})
		}
	}
	v.Cards = cards
	return v
}

// dayDate is the civil date of the i-th day of the period.
func dayDate(p progress.Period, i int) time.Time { return p.First.AddDate(0, 0, i) }

// timeChart is Listening time: a bar per day (week, month) or per month (year). Days after today and
// days without listening have no bar.
func timeChart(in listeningIn) components.BarChartView {
	return barsByPeriod(in.L.Period, in.L.Days, in.L.DaysSoFar, in.Today)
}

// barsByPeriod draws ms per day of period p (days, one per day) as the Time chart of either stats page.
func barsByPeriod(p progress.Period, days []int64, daysSoFar int, today time.Time) components.BarChartView {
	v := components.BarChartView{Heads: []string{listenHeadDay, listenHeadTime}, Caption: listenCapDay}
	var most int64
	if p.Range == progress.RangeYear {
		v.Heads[0], v.Caption = listenHeadMonth, listenCapMonth
		months := monthsOf(p, days)
		for m, ms := range months {
			name := time.Month(m + 1).String()
			v.Bars = append(v.Bars, components.ChartBar{Label: name[:1], Title: name + ": " + weekTime(ms), Ms: ms})
			most = max(most, ms)
			if !p.First.AddDate(0, m, 0).After(today) {
				v.Rows = append(v.Rows, components.ChartRow{Head: name, Cells: []string{weekTime(ms)}})
			}
		}
	} else {
		for i, ms := range days {
			d := dayDate(p, i)
			label := ""
			switch {
			case p.Range == progress.RangeWeek:
				label = string(statsDayLetters[i])
			case i%7 == 0:
				label = strconv.Itoa(i + 1)
			}
			v.Bars = append(v.Bars, components.ChartBar{Label: label, Title: dayTitle(d) + ": " + weekTime(ms), Ms: ms})
			most = max(most, ms)
			if i < daysSoFar {
				v.Rows = append(v.Rows, components.ChartRow{Head: dayLong(d), Cells: []string{weekTime(ms)}})
			}
		}
	}
	v.MaxMs = niceMax(most)
	v.MaxLabel, v.MidLabel = tickText(v.MaxMs), tickText(v.MaxMs/2)
	return v
}

// yearMonths sums a year's days by month.
func monthsOf(p progress.Period, days []int64) [12]int64 {
	var out [12]int64
	for i, ms := range days {
		out[dayDate(p, i).Month()-1] += ms
	}
	return out
}

// weekdayChart is Days of the week and the name of the weekday with the most time (the earlier day wins a tie).
func weekdayChart(l progress.Listening) (components.BarChartView, string) {
	v := components.BarChartView{Caption: listenCapWeekday, Heads: []string{listenHeadDay, listenHeadTime}}
	var most int64
	best := 0
	for d, ms := range l.Weekdays {
		name := time.Weekday(d).String()
		v.Bars = append(v.Bars, components.ChartBar{Label: string(statsDayLetters[d]), Title: name + ": " + weekTime(ms), Ms: ms})
		v.Rows = append(v.Rows, components.ChartRow{Head: name, Cells: []string{weekTime(ms)}})
		if ms > most {
			most, best = ms, d
		}
	}
	v.MaxMs = niceMax(most)
	v.MaxLabel, v.MidLabel = tickText(v.MaxMs), tickText(v.MaxMs/2)
	return v, time.Weekday(best).String()
}

type clockView struct {
	say  string
	view pages.ListeningClock
}

// clockCard is Time of day: 24 hour bars over the day and night band, then four parts. The part with
// the most time wins "You listen most ..."; the earlier of Morning, Afternoon, Evening, Night wins a tie.
func clockCard(l progress.Listening) clockView {
	chart := components.BarChartView{Night: true, Edge: true, Caption: listenCapHour, Heads: []string{listenHeadHour, listenHeadTime}}
	marks := map[int]struct{ label, icon string }{
		0: {listenMidnight, "sleep"}, 6: {listenSixAm, "sunrise"}, 12: {listenNoon, "sun"}, 18: {listenSixPm, "sunset"},
	}
	var most, total int64
	for h, ms := range l.Hours {
		m := marks[h]
		chart.Bars = append(chart.Bars, components.ChartBar{Label: m.label, Icon: m.icon, Title: hourSpan(h) + ": " + weekTime(ms), Ms: ms})
		chart.Rows = append(chart.Rows, components.ChartRow{Head: hourSpan(h), Cells: []string{weekTime(ms)}})
		most = max(most, ms)
		total += ms
	}
	chart.MaxMs = niceMax(most)
	chart.MaxLabel, chart.MidLabel = tickText(chart.MaxMs), tickText(chart.MaxMs/2)
	sum := func(hours ...int) int64 {
		var t int64
		for _, h := range hours {
			t += l.Hours[h]
		}
		return t
	}
	defs := []struct {
		name, span, icon, said string
		hours                  []int
	}{
		{listenMorning, listenMorningH, "sunrise", listenMostMorn, []int{6, 7, 8, 9, 10, 11}},
		{listenAfternoon, listenAfternoonH, "sun", listenMostAfter, []int{12, 13, 14, 15, 16, 17}},
		{listenEvening, listenEveningH, "sunset", listenMostEve, []int{18, 19, 20, 21}},
		{listenNight, listenNightH, "sleep", listenMostNight, []int{22, 23, 0, 1, 2, 3, 4, 5}},
	}
	out := clockView{view: pages.ListeningClock{Chart: chart, Note: listenBand}}
	var best int64 = -1
	for _, d := range defs {
		ms := sum(d.hours...)
		share := 0
		if total > 0 {
			share = int((ms*100 + total/2) / total)
		}
		out.view.Parts = append(out.view.Parts, pages.ListeningPart{Icon: d.icon, Name: d.name, Hours: d.span,
			Time: weekTime(ms), Share: strconv.Itoa(share) + " %"})
		if ms > best {
			best, out.say = ms, d.said
		}
	}
	return out
}

// topLists turns Most listened into its lists; an empty list is left out. The meter of each row is
// scaled to the first row of its list.
func topLists(t library.TopListened) []pages.ListeningTopList {
	var out []pages.ListeningTopList
	meter := func(ms, first int64) string { return fmt.Sprintf("%.1f", float64(ms)*100/float64(max(first, 1))) }
	if len(t.Books) > 0 {
		list := pages.ListeningTopList{Heading: listenBooks}
		for _, b := range t.Books {
			c := cardView(b.Book)
			if c.CoverSrc == "" {
				c.Title = initialOf(b.Book.Title) // a 36 px tile has room for a letter, not a title
			}
			list.Rows = append(list.Rows, pages.ListeningTopRow{Cover: &c, Href: c.Href, Name: b.Book.Title, Byline: c.Byline,
				Time: weekTime(b.Ms), Meter: meter(b.Ms, t.Books[0].Ms)})
		}
		out = append(out, list)
	}
	people := func(heading string, names []library.TopName) {
		if len(names) == 0 {
			return
		}
		list := pages.ListeningTopList{Heading: heading}
		for _, n := range names {
			list.Rows = append(list.Rows, pages.ListeningTopRow{Name: n.Name, Time: weekTime(n.Ms), Meter: meter(n.Ms, names[0].Ms)})
		}
		out = append(out, list)
	}
	people(listenAuthors, t.Authors)
	people(listenNarrators, t.Narrators)
	return out
}

// heatGrid is Day by day: a cell for each day of the year up to today, Sunday-first columns.
func heatGrid(in listeningIn) components.HeatGridView {
	l := in.L
	p := l.Period
	offset := int(p.First.Weekday())
	v := components.HeatGridView{
		Weeks: (p.Days + offset + 6) / 7, Caption: listenCapHeat,
		Heads: []string{listenHeadMonth, listenHeadDaysEvr, listenHeadTime},
		Key: []components.HeatKey{{Level: 0, Label: listenKeyNone}, {Level: 1, Label: listenKey1}, {Level: 2, Label: listenKey2},
			{Level: 3, Label: listenKey3}, {Level: 4, Label: listenKey4}},
	}
	v.WeekLabels = make([]string, v.Weeks)
	for m := time.January; m <= time.December; m++ {
		v.WeekLabels[(time.Date(p.First.Year(), m, 1, 0, 0, 0, 0, time.UTC).YearDay()-1+offset)/7] = m.String()[:3]
	}
	var days [12]int
	var ms [12]int64
	for i := 0; i < l.DaysSoFar && i < len(l.Days); i++ {
		d := dayDate(p, i)
		title := dayTitle(d) + ": " + listenKeyNone
		if l.Days[i] > 0 {
			title = dayTitle(d) + ": " + weekTime(l.Days[i])
		}
		v.Cells = append(v.Cells, components.HeatCell{Week: (i + offset) / 7, Weekday: int(d.Weekday()), Month: int(d.Month()) - 1,
			Day: d.Day(), Level: heatLevel(l.Days[i]), Title: title})
		ms[d.Month()-1] += l.Days[i]
		if l.Days[i] >= progress.StatsDayMinMs {
			days[d.Month()-1]++
		}
	}
	for m := 0; m < 12; m++ {
		if p.First.AddDate(0, m, 0).After(in.Today) {
			break
		}
		v.Rows = append(v.Rows, components.ChartRow{Head: time.Month(m + 1).String(), Cells: []string{strconv.Itoa(days[m]), weekTime(ms[m])}})
	}
	return v
}

// yearsChart is Year by year, shown when two or more years have listening.
func yearsChart(in listeningIn) (components.BarChartView, bool) {
	v := components.BarChartView{Caption: listenCapYear, Heads: []string{listenHeadYear, listenHeadTime}}
	var most int64
	with := 0
	for _, y := range in.Years {
		name := strconv.Itoa(y.Year)
		v.Bars = append(v.Bars, components.ChartBar{Label: name, Title: name + ": " + weekTime(y.Ms), Ms: y.Ms})
		v.Rows = append(v.Rows, components.ChartRow{Head: name, Cells: []string{weekTime(y.Ms)}})
		most = max(most, y.Ms)
		if y.Ms > 0 {
			with++
		}
	}
	if with < 2 {
		return v, false
	}
	v.MaxMs = niceMax(most)
	v.MaxLabel, v.MidLabel = tickText(v.MaxMs), tickText(v.MaxMs/2)
	return v, true
}

// initialOf is the first letter of a title, upper case; "" for an empty title.
func initialOf(title string) string {
	for _, r := range strings.TrimSpace(title) {
		return strings.ToUpper(string(r))
	}
	return ""
}
