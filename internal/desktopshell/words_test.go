package desktopshell

import (
	"fmt"
	"testing"
)

type spokenError struct{}

func (spokenError) Error() string { return "internal detail" }
func (spokenError) Words() string { return "Plain words." }

// A failure that carries its own words is shown in them, also when wrapped; the detail stays out.
func TestDesktopCheckSpokenError(t *testing.T) {
	got := Words(fmt.Errorf("start: %w", spokenError{}))
	if got != "Plain words." {
		t.Fatalf("Words = %q", got)
	}
}
