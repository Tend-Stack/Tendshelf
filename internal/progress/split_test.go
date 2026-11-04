package progress

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

func zone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Skip("no zoneinfo for " + name)
	}
	return loc
}

func TestSplitSession(t *testing.T) {
	ny := zone(t, "America/New_York")
	kol := zone(t, "Asia/Kolkata")
	lh := zone(t, "Australia/Lord_Howe")
	const min = int64(60_000)
	at := func(loc *time.Location, y int, m time.Month, day, h, mi int) int64 {
		return time.Date(y, m, day, h, mi, 0, 0, loc).UnixMilli()
	}
	cases := []struct {
		name       string
		loc        *time.Location
		start, end int64
		listened   int64
		want       []string // "date hour ms"; nil checks only the sum
		pieces     int      // expected piece count when want is nil
	}{
		{"23:30 to 00:30 splits the day", time.UTC, at(time.UTC, 2026, 10, 31, 23, 30), at(time.UTC, 2026, 11, 1, 0, 30), 60 * min,
			[]string{"2026-10-31 23 1800000", "2026-11-01 0 1800000"}, 0},
		{"10:50 to 11:10 splits the hour", time.UTC, at(time.UTC, 2026, 5, 1, 10, 50), at(time.UTC, 2026, 5, 1, 11, 10), 20 * min,
			[]string{"2026-05-01 10 600000", "2026-05-01 11 600000"}, 0},
		{"7 ms over 3 pieces sums to 7", time.UTC, at(time.UTC, 2026, 5, 1, 10, 50), at(time.UTC, 2026, 5, 1, 12, 10), 7, nil, 3},
		{"ended == started", time.UTC, at(time.UTC, 2026, 5, 1, 10, 5), at(time.UTC, 2026, 5, 1, 10, 5), 5000,
			[]string{"2026-05-01 10 5000"}, 0},
		{"nothing listened", time.UTC, 0, 1000, 0, nil, 0},
		{"30 h span counts only the last 24 h", time.UTC, at(time.UTC, 2026, 5, 1, 0, 0), at(time.UTC, 2026, 5, 2, 6, 0), 24 * 60 * min, nil, 24},
		{"spring forward 01:30 EST to 03:30 EDT", ny, at(ny, 2026, 3, 8, 1, 30), at(ny, 2026, 3, 8, 3, 30), 60 * min,
			[]string{"2026-03-08 1 1800000", "2026-03-08 3 1800000"}, 0},
		{"fall back 01:00 EDT to 01:00 EST is all hour 1", ny, at(ny, 2026, 11, 1, 1, 0), at(ny, 2026, 11, 1, 1, 0) + 3_600_000, 60 * min,
			[]string{"2026-11-01 1 3600000"}, 0},
		{"Kolkata cuts at local :00", kol, at(kol, 2026, 6, 1, 8, 40), at(kol, 2026, 6, 1, 9, 20), 40 * min,
			[]string{"2026-06-01 8 1200000", "2026-06-01 9 1200000"}, 0},
		{"Lord Howe (30 min DST) terminates", lh, at(lh, 2026, 10, 3, 1, 0), at(lh, 2026, 10, 3, 4, 0), 180 * min, nil, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			var sum int64
			splitSession(c.start, c.end, c.listened, c.loc, func(at time.Time, share int64) {
				got = append(got, fmt.Sprintf("%s %d %d", at.Format("2006-01-02"), at.Hour(), share))
				sum += share
			})
			if c.listened > 0 && sum != c.listened {
				t.Fatalf("pieces sum %d, want %d (%v)", sum, c.listened, got)
			}
			if c.listened <= 0 && len(got) != 0 {
				t.Fatalf("pieces %v for nothing listened", got)
			}
			if c.want != nil && !reflect.DeepEqual(got, c.want) {
				t.Fatalf("pieces %v, want %v", got, c.want)
			}
			if c.want == nil && c.pieces > 0 && len(got) != c.pieces {
				t.Fatalf("%d pieces %v, want %d", len(got), got, c.pieces)
			}
		})
	}

	t.Run("30 h span starts 24 h before the end", func(t *testing.T) {
		var first string
		splitSession(at(time.UTC, 2026, 5, 1, 0, 0), at(time.UTC, 2026, 5, 2, 6, 0), 3600, time.UTC, func(t time.Time, _ int64) {
			if first == "" {
				first = t.Format("2006-01-02 15")
			}
		})
		if first != "2026-05-01 06" {
			t.Fatalf("first piece %s, want 2026-05-01 06", first)
		}
	})
}

// TestListeningQueryPlan pins that the piece query uses the (user_id, started_at) index.
func TestListeningQueryPlan(t *testing.T) {
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	rows, err := st.Reader().Query(`EXPLAIN QUERY PLAN `+eachPieceSQL, "u", 0, 1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if p := strings.Join(plan, "; "); !strings.Contains(p, "listening_sessions_user") {
		t.Fatalf("plan %q does not use listening_sessions_user", p)
	}
}
