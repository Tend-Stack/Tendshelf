package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// The words of /me/listening (decision 0014, board ListeningDashboard). The deck has none of them yet
// (docs/copy-additions.md); they follow the voice rules in BRAND.md.
const (
	listenTitle       = "Your listening"
	listenBack        = "Profile"
	listenMore        = "See your listening over time"
	listenRangeLabel  = "Show"
	listenWeek        = "Week"
	listenMonth       = "Month"
	listenYear        = "Year"
	listenShowFmt     = "Show %s"
	listenNumListened = "Listened"
	listenNumDays     = "Days you listened"
	listenNumOfFmt    = "%d of %d"
	listenNumFinished = "Finished"
	listenNumSpeed    = "Usual speed"

	listenTimeTitle  = "Listening time"
	listenSayAvgFmt  = "%s in %s, about %s on the days you listened."
	listenSayTotFmt  = "%s in %s."
	listenDayTitle   = "Time of day"
	listenMostMorn   = "You listen most in the morning."
	listenMostAfter  = "You listen most in the afternoon."
	listenMostEve    = "You listen most in the evening."
	listenMostNight  = "You listen most at night."
	listenMorning    = "Morning"
	listenAfternoon  = "Afternoon"
	listenEvening    = "Evening"
	listenNight      = "Night"
	listenMorningH   = "6 am to noon"
	listenAfternoonH = "noon to 6 pm"
	listenEveningH   = "6 pm to 10 pm"
	listenNightH     = "10 pm to 6 am"
	listenMidnight   = "midnight"
	listenNoon       = "noon"
	listenSixAm      = "6 am"
	listenSixPm      = "6 pm"
	listenBand       = "The light band is daytime, 6 am to 6 pm."

	listenWeekdaysTitle = "Days of the week"
	listenMostDayFmt    = "You listen most on %ss."
	listenDayByDay      = "Day by day"
	listenDaysInFmt     = "You listened on %d days in %s."
	listenOneDayInFmt   = "You listened on 1 day in %s."
	listenKeyNone       = "No listening"
	listenKey1          = "Under 15 m"
	listenKey2          = "15 to 45 m"
	listenKey3          = "45 m to 1 h 30 m"
	listenKey4          = "1 h 30 m or more"

	listenMostTitle  = "Most listened"
	listenBooks      = "Books"
	listenAuthors    = "Authors"
	listenNarrators  = "Narrators"
	listenDoneTitle  = "Finished"
	listenYearByYear = "Year by year"
	listenSinceFmt   = "Listening since %s."

	listenEmptyTitle  = "Nothing to show yet"
	listenEmptyBody   = "Listen to a book and your time will show here, by day and by hour."
	listenEmptyAction = "Go to your library"
	listenQuietFmt    = "No listening in %s."

	listenCapDay      = "Listening time by day"
	listenCapMonth    = "Listening time by month"
	listenCapHour     = "Listening by hour of the day"
	listenCapWeekday  = "Listening by day of the week"
	listenCapHeat     = "Listening by month"
	listenCapYear     = "Listening by year"
	listenHeadDay     = "Day"
	listenHeadMonth   = "Month"
	listenHeadHour    = "Hour"
	listenHeadYear    = "Year"
	listenHeadTime    = "Time"
	listenHeadDaysEvr = "Days you listened"
)

// listenMaxMinutes are the rounded maxima a bar chart's top tick can take, in minutes.
var listenMaxMinutes = []int64{10, 20, 30, 60, 90, 120, 180, 240, 300, 360, 480, 600, 720, 900, 1200, 1500, 1800,
	2400, 3000, 3600, 4800, 6000, 7200, 9000, 12000, 15000, 18000, 24000, 30000, 36000, 48000, 60000}

// niceMax rounds the tallest bar up to the next value in listenMaxMinutes; past the list it rounds up to
// a multiple of 60,000 minutes. The result is in ms.
func niceMax(ms int64) int64 {
	mins := (ms + 59_999) / 60_000
	for _, m := range listenMaxMinutes {
		if m >= mins {
			return m * 60_000
		}
	}
	const step = 60_000
	return (mins + step - 1) / step * step * 60_000
}

// tickText writes an axis value: "45 m", "2 h", "1 h 30 m".
func tickText(ms int64) string {
	mins := max(ms, 0) / 60_000
	switch {
	case mins < 60:
		return strconv.FormatInt(mins, 10) + " m"
	case mins%60 == 0:
		return strconv.FormatInt(mins/60, 10) + " h"
	}
	return fmt.Sprintf("%d h %d m", mins/60, mins%60)
}

// speedText writes a speed percent as the number in "1.25×": "1.25", "1.2", "1".
func speedText(pct int) string {
	return strings.Replace(msgSpeedValue, "{v}", strconv.FormatFloat(float64(pct)/100, 'f', -1, 64), 1)
}

// heatLevel is a day's step of the heat ramp: fixed thresholds, so a colour means the same every year.
func heatLevel(ms int64) int {
	switch {
	case ms < 60_000:
		return 0
	case ms < 15*60_000:
		return 1
	case ms < 45*60_000:
		return 2
	case ms < 90*60_000:
		return 3
	}
	return 4
}

// periodLabel names a period: "1–7 Nov 2026", "29 Oct – 4 Nov 2026", "28 Dec 2025 – 3 Jan 2026",
// "October 2026", "2026".
func periodLabel(p progress.Period) string {
	switch p.Range {
	case progress.RangeYear:
		return strconv.Itoa(p.First.Year())
	case progress.RangeMonth:
		return p.First.Format("January 2006")
	}
	last := p.First.AddDate(0, 0, p.Days-1)
	switch {
	case p.First.Year() != last.Year():
		return p.First.Format("2 Jan 2006") + " – " + last.Format("2 Jan 2006")
	case p.First.Month() != last.Month():
		return p.First.Format("2 Jan") + " – " + last.Format("2 Jan 2006")
	}
	return p.First.Format("2") + "–" + last.Format("2 Jan 2006")
}

// hourName writes a clock hour in the 12-hour words the chart uses: "midnight", "6 am", "noon", "6 pm".
func hourName(h int) string {
	switch h %= 24; {
	case h == 0:
		return listenMidnight
	case h == 12:
		return listenNoon
	case h < 12:
		return strconv.Itoa(h) + " am"
	}
	return strconv.Itoa(h-12) + " pm"
}

// hourSpan is "6 am to 7 am".
func hourSpan(h int) string { return hourName(h) + " to " + hourName(h+1) }

// dayTitle is a day as the chart tooltips write it: "Tue 6 Oct".
func dayTitle(d time.Time) string { return d.Format("Mon 2 Jan") }

// dayLong is a day as the hidden tables write it: "Tuesday 6 October".
func dayLong(d time.Time) string { return d.Format("Monday 2 January") }
