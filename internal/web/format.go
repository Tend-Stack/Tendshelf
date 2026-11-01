package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// agoText says how long ago t was, in plain words: "just now", "5 minutes ago", "3 hours ago",
// "yesterday", "4 days ago", then a date such as "2 Jan".
func agoText(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return countText(int(d/time.Minute), "1 minute ago", "%d minutes ago")
	case d < 24*time.Hour:
		return countText(int(d/time.Hour), "1 hour ago", "%d hours ago")
	case d < 48*time.Hour:
		return "yesterday"
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
	}
	return t.Format("2 Jan")
}

// countText returns one for n = 1 and many, a format with one %d, for any other number. For
// example countText(n, "1 book", "%d books").
func countText(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf(many, n)
}

// sizeText formats bytes with decimal units: one decimal under 10 ("1.1 TB"), none from 10 ("61 GB").
func sizeText(b int64) string {
	if b < 1000 {
		return strconv.FormatInt(b, 10) + " B"
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	v := float64(b)
	i := -1
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if v < 9.95 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

// thousands writes n with a comma between each group of three digits.
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// durationText writes a length in plain words for a card: "17 h 32 m", "5 h", "48 m". It rounds to
// the nearest minute; an unknown (zero or negative) length is "".
func durationText(ms int64) string {
	if ms <= 0 {
		return ""
	}
	mins := int((ms + 30_000) / 60_000)
	if mins < 1 {
		mins = 1
	}
	h, m := mins/60, mins%60
	switch {
	case h == 0:
		return strconv.Itoa(m) + " m"
	case m == 0:
		return strconv.Itoa(h) + " h"
	}
	return strconv.Itoa(h) + " h " + strconv.Itoa(m) + " m"
}
