package progress_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

type sessionRow struct {
	Device                             string
	StartedAt, EndedAt                 int64
	StartMs, EndMs, ListenedMs, SpeedP int64
}

func sessionRows(t testing.TB, st *db.Store, user, book string) []sessionRow {
	t.Helper()
	rows, err := st.Reader().Query(`SELECT device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct FROM listening_sessions
WHERE user_id = ? AND book_id = ? ORDER BY ended_at, id`, user, book)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []sessionRow
	for rows.Next() {
		var r sessionRow
		if err := rows.Scan(&r.Device, &r.StartedAt, &r.EndedAt, &r.StartMs, &r.EndMs, &r.ListenedMs, &r.SpeedP); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// progSave saves a position with a listening report. at is the client time in ms.
func progSave(t testing.TB, svc *progress.Service, user, book, dev string, at, pos int64, speed float64, listened int64) progress.Result {
	t.Helper()
	r, err := svc.Save(ctx, progress.Update{UserID: user, BookID: book, DeviceID: dev, ClientUpdatedAt: at,
		PositionMs: pos, DurationMs: 3_600_000, Speed: speed, ListenedMs: listened})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSessionContinuous(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	b := books[0].ID
	t0 := clk.Ms() - 600_000
	for i := int64(0); i < 6; i++ {
		progSave(t, svc, u[0], b, devA, t0+i*10_000, 100_000+i*10_000, 1.0, 10_000)
	}
	rows := sessionRows(t, st, u[0], b)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	want := sessionRow{Device: devA, StartedAt: t0 - 10_000, EndedAt: t0 + 50_000, StartMs: 90_000, EndMs: 150_000, ListenedMs: 60_000, SpeedP: 100}
	if rows[0] != want {
		t.Fatalf("row = %+v, want %+v", rows[0], want)
	}
}

func TestSessionRules(t *testing.T) {
	// Each case starts from one open session: device A, ended at t0 after 10 s at 1.0x, end_ms 100 000.
	cases := []struct {
		name         string
		at           int64 // seconds after t0
		pos          int64
		speed        float64
		listened     int64
		rows         int
		wantListened int64 // listened_ms of the newest row
		wantSpeed    int64
	}{
		{"pause 30 s then 10 s play extends", 40, 110_000, 1.0, 10_000, 1, 20_000, 100},
		{"pause 61 s starts a new session", 71, 110_000, 1.0, 10_000, 2, 10_000, 100},
		{"speed change starts a new session", 10, 115_000, 1.5, 10_000, 2, 10_000, 150},
		{"seek forward 5 min starts a new session", 10, 400_000, 1.0, 10_000, 2, 10_000, 100},
		{"skip back 15 s extends", 10, 95_000, 1.0, 10_000, 1, 20_000, 100},
		{"same client time again changes nothing", 0, 110_000, 1.0, 10_000, 1, 10_000, 100},
		{"older client time changes nothing", -5, 110_000, 1.0, 10_000, 1, 10_000, 100},
		{"nothing played changes nothing", 10, 110_000, 1.0, 0, 1, 10_000, 100},
		{"half a second after a jump opens nothing", 10, 400_000, 1.0, 500, 1, 10_000, 100},
		{"20 minutes reported is capped", 1500, 3_000_000, 1.0, 1_200_000, 2, progress.SessionMaxListenMs, 100},
		{"an extension adds at most the time since the last save", 5, 105_000, 1.0, 10_000, 1, 15_000, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, u, books, clk := progEnv(t)
			b := books[0].ID
			t0 := clk.Ms() - 3_000_000
			progSave(t, svc, u[0], b, devA, t0, 100_000, 1.0, 10_000)
			progSave(t, svc, u[0], b, devA, t0+c.at*1000, c.pos, c.speed, c.listened)
			rows := sessionRows(t, st, u[0], b)
			if len(rows) != c.rows {
				t.Fatalf("rows = %+v", rows)
			}
			// Rows are ordered by ended_at, so the newest session is last unless nothing changed.
			last := rows[len(rows)-1]
			if last.ListenedMs != c.wantListened || last.SpeedP != c.wantSpeed {
				t.Fatalf("last = %+v, want listened %d speed %d", last, c.wantListened, c.wantSpeed)
			}
		})
	}
}

func TestSessionDevicesAndUsers(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	b := books[0].ID
	t0 := clk.Ms() - 600_000
	progSave(t, svc, u[0], b, devA, t0, 100_000, 1.0, 10_000)
	progSave(t, svc, u[0], b, devB, t0+1000, 500_000, 1.0, 10_000)
	progSave(t, svc, u[1], b, devA, t0+2000, 700_000, 1.0, 10_000)
	progSave(t, svc, u[0], b, devA, t0+10_000, 110_000, 1.0, 10_000)
	rows := sessionRows(t, st, u[0], b)
	if len(rows) != 2 || rows[0].Device != devB || rows[1].Device != devA || rows[1].ListenedMs != 20_000 || rows[0].ListenedMs != 10_000 {
		t.Fatalf("A's rows = %+v", rows)
	}
	if rows := sessionRows(t, st, u[1], b); len(rows) != 1 || rows[0].ListenedMs != 10_000 || rows[0].EndMs != 700_000 {
		t.Fatalf("B's rows = %+v", rows)
	}

	// A save refused by newest-wins still records the listening that happened on this device.
	if r := progSave(t, svc, u[0], b, devB, t0+5000, 505_000, 1.0, 5_000); r.Applied {
		t.Fatal("expected the old save to be refused")
	}
	rowsB := sessionRows(t, st, u[0], b)
	var devBListened int64
	for _, r := range rowsB {
		if r.Device == devB {
			devBListened = r.ListenedMs
		}
	}
	if devBListened != 14_000 { // extends by min(5 000, 4 000 since the last save)
		t.Fatalf("device B listened = %d, rows %+v", devBListened, rowsB)
	}

	// Server acts record no session.
	before := len(sessionRows(t, st, u[0], b))
	clk.Advance(time.Second)
	if _, err := svc.SetFinished(ctx, u[0], b, true, 3_600_000); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetFinished(ctx, u[0], b, false, 3_600_000); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionRows(t, st, u[0], b)); n != before {
		t.Fatalf("server acts changed the session count: %d -> %d", before, n)
	}

	// A bad speed refuses the whole save: neither table changes.
	prog, _ := svc.Get(ctx, u[1], b)
	_, err := svc.Save(ctx, progress.Update{UserID: u[1], BookID: b, DeviceID: devA, ClientUpdatedAt: t0 + 60_000, PositionMs: 1, DurationMs: 10, Speed: 4.0, ListenedMs: 10_000})
	var ie *progress.InvalidError
	if !errors.As(err, &ie) || ie.Field != "speed" {
		t.Fatalf("speed 4.0: %v", err)
	}
	after, _ := svc.Get(ctx, u[1], b)
	if after != prog || len(sessionRows(t, st, u[1], b)) != 1 {
		t.Fatal("a refused save changed progress or sessions")
	}

	// Deleting the book or the person removes the sessions.
	if _, err := st.Writer().Exec(`DELETE FROM users WHERE id = ?`, u[1]); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionRows(t, st, u[1], b)); n != 0 {
		t.Fatalf("user delete left %d sessions", n)
	}
	if _, err := st.Writer().Exec(`DELETE FROM books WHERE id = ?`, b); err != nil {
		t.Fatal(err)
	}
	if n := len(sessionRows(t, st, u[0], b)); n != 0 {
		t.Fatalf("book delete left %d sessions", n)
	}
}

// A save whose session cannot be written leaves no progress behind either: one transaction.
func TestSessionRollsBackWithProgress(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	if _, err := st.Writer().Exec(`CREATE TRIGGER no_sessions BEFORE INSERT ON listening_sessions BEGIN SELECT RAISE(ABORT, 'no'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Save(ctx, progress.Update{UserID: u[0], BookID: books[0].ID, DeviceID: devA, ClientUpdatedAt: clk.Ms(), PositionMs: 1, DurationMs: 10, Speed: 1, ListenedMs: 5000})
	if err == nil {
		t.Fatal("expected the session insert to fail the save")
	}
	if _, err := svc.Get(ctx, u[0], books[0].ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("progress survived a failed session: %v", err)
	}
}

func BenchmarkProgressSave(b *testing.B) {
	svc, _, u, books, clk := progEnv(b)
	at := clk.Ms() - 100_000_000_000
	pos := int64(0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		at += 10_000
		pos += 10_000
		if pos > 3_000_000 {
			pos = 10_000
		}
		if _, err := svc.Save(ctx, progress.Update{UserID: u[0], BookID: books[0].ID, DeviceID: devA, ClientUpdatedAt: at,
			PositionMs: pos, DurationMs: 3_600_000, Speed: 1.0, ListenedMs: 10_000}); err != nil {
			b.Fatal(err)
		}
	}
}
