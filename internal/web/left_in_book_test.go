package web

import (
	"strconv"
	"strings"
	"testing"
)

// TestLeftInBookWordsMatchDurationText: the full player's "left in book" line and the cards' length
// text come from the same three word forms, so the two cannot drift (2 m, 2 h, 1 h 5 m).
func TestLeftInBookWordsMatchDurationText(t *testing.T) {
	for _, minutes := range []int{1, 2, 59, 60, 61, 65, 120, 125, 1052} {
		h, m := minutes/60, minutes%60
		form := msgLeftInBookBoth
		switch {
		case h == 0:
			form = msgLeftInBookMinutes
		case m == 0:
			form = msgLeftInBookHours
		}
		got := strings.NewReplacer("{h}", strconv.Itoa(h), "{m}", strconv.Itoa(m)).Replace(form)
		want := durationText(int64(minutes)*60_000) + msgLeftInBookSuffix
		if got != want {
			t.Errorf("%d min: left in book %q, cards say %q", minutes, got, want)
		}
	}
	if durationText(65*60_000) != "1 h 5 m" || msgLeftInBookMinutes != "{m} m left in book" {
		t.Errorf("the words moved: %q, %q", durationText(65*60_000), msgLeftInBookMinutes)
	}
}
