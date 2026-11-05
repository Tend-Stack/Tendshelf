package web

import (
	"fmt"
	"strconv"
)

// The words of /me/reading (G2, docs/plans/m5-great-reader.md). The deck has none of them yet; they follow
// the voice rules in BRAND.md and are listed in the package report.
const (
	readTitle       = "Your reading"
	readNumTime     = "Time reading"
	readNumDays     = "Days read"
	readNumSpeed    = "Words a minute"
	readNumFinished = "Books finished"
	readSpeedAbout  = "About %d"
	readSpeedNone   = "Not yet"

	readTimeTitle = "Time"
	readSayAvgFmt = "%s in %s, about %s on the days you read."
	readSayTotFmt = "%s in %s."
	readCapDay    = "Reading time by day"
	readCapMonth  = "Reading time by month"

	readGoalTitle   = "Yearly goal"
	readGoalSayFmt  = "%d of %d books this year"
	readGoalNone    = "Set a number of books to finish this year."
	readGoalField   = "Books to finish this year"
	readGoalSave    = "Save"
	readGoalBarFmt  = "%d of %d books"
	readGoalInvalid = "Choose a whole number from 0 to 365."
	readDoneTitle   = "Finished"
	readEmptyTitle  = "Nothing to show yet"
	readEmptyBody   = "Read a book and your time will show here, by day."
	readEmptyAction = "Go to your library"
	readQuietFmt    = "No reading in %s."
	readMoreLink    = "Your reading"
)

// readSpeed writes a reading speed: characters a minute over five characters a word, rounded to 10.
// Under a minute of reading there is no speed yet.
func readSpeed(cpm int) string {
	if cpm <= 0 {
		return readSpeedNone
	}
	wpm := (cpm/5 + 5) / 10 * 10
	return fmt.Sprintf(readSpeedAbout, max(wpm, 10))
}

// goalPercent is books finished over the goal as 0 to 100 text, capped at 100.
func goalPercent(done, goal int) string {
	if goal <= 0 {
		return ""
	}
	return strconv.Itoa(min(done*100/goal, 100))
}
