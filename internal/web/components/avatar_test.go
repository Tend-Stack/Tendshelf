package components

import "testing"

func TestAvatarInitials(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Daniel Reyes", "DR"},
		{"maya", "M"},
		{"Rosa María de la Cruz", "RM"},
		{"  ínes   ", "Í"},
		{"", "?"},
		{"   ", "?"},
	} {
		if got := initials(c.name); got != c.want {
			t.Errorf("initials(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	// The colour is stable for an id and always one of the six palette classes.
	seen := map[int]bool{}
	for _, id := range []string{"usr_a", "usr_b", "usr_c", "usr_d", "usr_e", "usr_f", "usr_g", "usr_h"} {
		c := avatarColour(id)
		if c < 0 || c > 5 || c != avatarColour(id) {
			t.Fatalf("avatarColour(%q) = %d", id, c)
		}
		seen[c] = true
	}
	if len(seen) < 2 {
		t.Error("every id got the same colour")
	}
}
