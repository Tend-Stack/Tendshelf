package web

import (
	"testing"
	"time"
)

func TestFormatHelpers(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	ago := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{5 * time.Minute, "5 minutes ago"},
		{2 * time.Hour, "2 hours ago"},
		{25 * time.Hour, "yesterday"},
		{3 * 24 * time.Hour, "3 days ago"},
		{9 * 24 * time.Hour, "1 Nov"},
	}
	for _, c := range ago {
		if got := agoText(now, now.Add(-c.d)); got != c.want {
			t.Errorf("agoText(-%v) = %q, want %q", c.d, got, c.want)
		}
	}
	sizes := []struct {
		b    int64
		want string
	}{
		{0, "0 B"}, {999, "999 B"}, {1000, "1.0 kB"}, {1_500_000, "1.5 MB"}, {61_000_000_000, "61 GB"},
		{1_100_000_000_000, "1.1 TB"}, {9_960_000_000, "10 GB"},
	}
	for _, c := range sizes {
		if got := sizeText(c.b); got != c.want {
			t.Errorf("sizeText(%d) = %q, want %q", c.b, got, c.want)
		}
	}
	counts := []struct {
		n    int
		want string
	}{{0, "0 books"}, {1, "1 book"}, {2, "2 books"}}
	for _, c := range counts {
		if got := countText(c.n, "1 book", "%d books"); got != c.want {
			t.Errorf("countText(%d) = %q, want %q", c.n, got, c.want)
		}
	}
	nums := map[int]string{0: "0", 999: "999", 1000: "1,000", 12345: "12,345", 1234567: "1,234,567", -4321: "-4,321"}
	for n, want := range nums {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q, want %q", n, got, want)
		}
	}
}
