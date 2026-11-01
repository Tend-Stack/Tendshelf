package library

import "testing"

func TestDamerauLevenshtein(t *testing.T) {
	for _, c := range []struct {
		a, b string
		max  int
		want int
	}{
		{"", "", 2, 0},
		{"abc", "abc", 2, 0},
		{"abc", "", 5, 3},
		{"", "abc", 5, 3},
		{"lighthouse", "lighthuose", 2, 1}, // transposition
		{"lighthouse", "lighthose", 2, 1},  // deletion
		{"lighthouse", "lighthouses", 2, 1},
		{"kitten", "sitten", 2, 1},
		{"kitten", "sitting", 3, 3},
		{"tomas", "tomás", 2, 1},
		{"ca", "abc", 3, 3}, // optimal string alignment, not full Damerau
		{"abcdef", "azced", 5, 3},
		{"abc", "xyz", 1, 2},          // over max: max+1
		{"short", "muchlonger", 2, 3}, // length gap alone exceeds max
		{"abcdefgh", "hgfedcba", 2, 3},
	} {
		if got := damerauLevenshtein([]rune(c.a), []rune(c.b), c.max); got != c.want {
			t.Errorf("d(%q, %q, max %d) = %d, want %d", c.a, c.b, c.max, got, c.want)
		}
	}
}
