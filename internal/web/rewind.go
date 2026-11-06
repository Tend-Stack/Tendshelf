package web

import (
	"strconv"
	"strings"
	"time"
)

// Smart rewind: when playback resumes, the player steps back by an amount that grows with the
// length of the pause, so the listener hears again the sentence they were in. The table is the one
// source: the player reads it from data-rewind, and rewindSeconds is the same rule for the tests.
//
// The choices (nothing in docs/research covers rewinding, so the reasons are the player's own):
//   - Under 10 s nothing moves. A pause that short is a tap, an interruption of one breath.
//   - 2 s up to a minute: enough to catch the start of the sentence that was cut off.
//   - 5 s up to an hour: the old fixed value, which listeners were already used to.
//   - 10 s up to a day, 20 s beyond: memory of where the story stood fades with the gap, so more
//     of the scene is replayed; 20 s is the cap, so a long break never replays a whole page.
//
// The step is in book time, whatever the speed. The "Auto rewind" setting turns the whole rule off.
var rewindSteps = []struct {
	From    time.Duration // a pause at least this long...
	Seconds int           // ...rewinds this many seconds
}{
	{10 * time.Second, 2},
	{time.Minute, 5},
	{time.Hour, 10},
	{24 * time.Hour, 20},
}

// rewindSeconds is how far to step back after a pause of the given length; 0 means not at all.
func rewindSeconds(pause time.Duration) int {
	seconds := 0
	for _, s := range rewindSteps {
		if pause >= s.From {
			seconds = s.Seconds
		}
	}
	return seconds
}

// rewindAttr writes the table as data-rewind reads it: "10000:2,60000:5,..." (pause ms, seconds).
func rewindAttr() string {
	parts := make([]string, 0, len(rewindSteps))
	for _, s := range rewindSteps {
		parts = append(parts, strconv.FormatInt(s.From.Milliseconds(), 10)+":"+strconv.Itoa(s.Seconds))
	}
	return strings.Join(parts, ",")
}
