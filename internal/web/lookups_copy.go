package web

import (
	"fmt"
	"strconv"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// The words of automatic and bulk lookups (AL-4). The owner's switch keeps its words in
// details_copy.go.
const (
	lookupAutoLabel  = "Look up details online for new books"
	lookupAutoHint   = "Tendshelf sends each new book's title, author, ASIN and ISBN to Audible and Audnexus, and fills in only what is missing. Your files are not changed."
	lookupAutoSave   = "Save"
	lookupStart      = "Look up"
	lookupScopeLabel = "Which books to look up"
	lookupStop       = "Stop"

	lookupPausedBusy  = "Paused because Audible asked for a break. Continues at %s."
	lookupPausedDaily = "Paused for today to stay within the sites' limits. Continues at %s."
	lookupRefusing    = "Audible kept refusing, so Tendshelf stopped. Books already done stay done. Try again later."
	lookupStopped     = "The lookup stopped."

	lookupNeedsTitle   = "Books that need a look"
	lookupNeedsBack    = "Libraries & scanning"
	lookupNeedsEmpty   = "No book needs a look."
	lookupCompare      = "Compare"
	lookupLeave        = "Leave as it is"
	lookupOutcomeMany  = "Several possible matches"
	lookupOutcomeNone  = "Nothing found"
	lookupNeedsMaxRows = 200
)

// lookupBooksText is "1 book" or "{n} books".
func lookupBooksText(n int) string { return countText(n, "1 book", "%d books") }

// lookupProgressText is "Looking up {n} of {N} books · {m} found · {k} need a look · {f} not found",
// with " · {x} could not be checked" added when some books could not be.
func lookupProgressText(p metadata.BulkProgress) string {
	s := fmt.Sprintf("Looking up %d of %s · %d found · %d need a look · %d not found",
		p.Done, lookupBooksText(p.Total), p.Matched, p.NeedsLook, p.NotFound)
	if p.Failed > 0 {
		s += fmt.Sprintf(" · %d could not be checked", p.Failed)
	}
	return s
}

// lookupDoneText says how a finished lookup ended.
func lookupDoneText(p metadata.BulkProgress) string {
	return fmt.Sprintf("Done: %d found, %d need a look, %d not found.", p.Matched, p.NeedsLook, p.NotFound)
}

// lookupPauseText says why the lookup waits and when it goes on, in the server's own time zone.
func lookupPauseText(reason string, until time.Time) string {
	f := lookupPausedBusy
	if reason == metadata.PauseDailyCap {
		f = lookupPausedDaily
	}
	return fmt.Sprintf(f, until.Local().Format("15:04"))
}

// lookupNeedsLabel is the link "{k} need a look".
func lookupNeedsLabel(n int) string {
	return strconv.Itoa(n) + " need a look"
}

// lookupStoppedText says how a lookup that a person stopped ended.
func lookupStoppedText(p metadata.BulkProgress) string {
	return fmt.Sprintf("%s %d found, %d need a look, %d not found.", lookupStopped, p.Matched, p.NeedsLook, p.NotFound)
}

func lookupMoreText(n int) string { return "and " + strconv.Itoa(n) + " more" }

// The three choices of what a lookup covers, each with how many books it would ask about right now.
func lookupScopeMissing(n int) string  { return "Books missing details (" + thousands(n) + ")" }
func lookupScopeUnlooked(n int) string { return "Books not looked up yet (" + thousands(n) + ")" }
func lookupScopeAll(n int) string      { return "All books (" + thousands(n) + ")" }
