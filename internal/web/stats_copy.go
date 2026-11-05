package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// The words of the listening statistics (Profile, and the person page for an admin). The board is
// ProfilePhone; "Listening" and every label form except the board's own sentence are not in the
// copy deck yet (docs/copy-additions.md).
const (
	statsTitle        = "Your listening"
	statsTitleAdmin   = "Listening"
	statsWeek         = "This week"
	statsStreak       = "In a row"
	statsFinishedFmt  = "Finished in %d"
	statsOneDay       = "1 day"
	statsDaysFmt      = "%d days"
	statsPrivate      = "Only you see these."
	statsNone         = "No listening yet this week"
	statsListenedFmt  = "Listened on %s this week"
	statsNotYetFmt    = "; not yet today, %s"
	statsDayLetters   = "SMTWTFS"
	statsRunMin       = 3 // a run this long reads "Sunday to Friday"
	statsHoursMinutes = "%d h %d m"
)

// weekTime writes listening time in the Home word forms, rounded down: "6 h 12 m", "45 m", "0 m".
func weekTime(ms int64) string {
	mins := max(ms, 0) / 60_000
	if mins < 60 {
		return strconv.FormatInt(mins, 10) + " m"
	}
	return fmt.Sprintf(statsHoursMinutes, mins/60, mins%60)
}

// joinNames writes "A", "A and B" or "A, B and C".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// statsLabel is the sentence the day marks speak: which days had listening, and today when it has none yet.
func statsLabel(days [7]progress.DayMark) string {
	var parts []string
	notYet := ""
	for i := 0; i < len(days); {
		if days[i].State == progress.DayToday {
			notYet = fmt.Sprintf(statsNotYetFmt, days[i].Weekday)
		}
		if days[i].State != progress.DayListened {
			i++
			continue
		}
		j := i
		for j+1 < len(days) && days[j+1].State == progress.DayListened {
			j++
		}
		if j-i+1 >= statsRunMin {
			parts = append(parts, days[i].Weekday.String()+" to "+days[j].Weekday.String())
		} else {
			for k := i; k <= j; k++ {
				parts = append(parts, days[k].Weekday.String())
			}
		}
		i = j + 1
	}
	if len(parts) == 0 {
		return statsNone + notYet
	}
	return fmt.Sprintf(statsListenedFmt, joinNames(parts)) + notYet
}

// statsView turns the numbers into the words the panel shows. admin is the person page: no day
// marks and no "only you" line.
func statsView(st progress.Stats, admin, reads bool) components.ListeningStatsView {
	v := components.ListeningStatsView{
		Title:         statsTitle,
		WeekLabel:     statsWeek,
		Week:          weekTime(st.WeekMs),
		StreakLabel:   statsStreak,
		Streak:        fmt.Sprintf(statsDaysFmt, st.Streak),
		FinishedLabel: fmt.Sprintf(statsFinishedFmt, st.Year),
		Finished:      strconv.Itoa(st.Finished),
	}
	if st.Streak == 1 {
		v.Streak = statsOneDay
	}
	if admin {
		v.Title = statsTitleAdmin
		return v
	}
	v.Note = statsPrivate
	v.More, v.MoreHref = listenMore, "/me/listening"
	if reads {
		v.Also, v.AlsoHref = readMoreLink, readingStatsRoute
	}
	v.DaysLabel = statsLabel(st.Days)
	for i, d := range st.Days {
		v.Days = append(v.Days, components.StatsDay{Letter: string(statsDayLetters[i]), State: d.State})
	}
	return v
}

// statsLocation is the zone days are cut in: the person's stored zone, else the server's.
func statsLocation(zone string) *time.Location {
	if zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	return time.Local
}
