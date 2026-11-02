//go:build dev

package db

import "testing"

func TestSeedIDsRepeatsPerKeyAndEndsWithEmptyKey(t *testing.T) {
	t.Cleanup(func() { SeedIDs("") })
	SeedIDs("a")
	first := []string{NewID("bk"), NewID("bk"), NewID("usr")}
	SeedIDs("a") // same key: the sequence goes on, so ids never repeat inside one test
	if again := NewID("bk"); again == first[0] || again == first[1] {
		t.Fatalf("id %s repeated", again)
	}
	SeedIDs("b") // a new key restarts the sequence
	if got := NewID("bk"); got != first[0] {
		t.Fatalf("restart gave %s, want %s", got, first[0])
	}
	if len(first[0]) != len("bk_")+26 || first[0] == first[1] {
		t.Fatalf("bad ids %v", first)
	}
	SeedIDs("")
	if a, b := NewID("bk"), NewID("bk"); a == b || a == first[0] {
		t.Fatalf("random ids expected after SeedIDs(\"\"): %s %s", a, b)
	}
}
