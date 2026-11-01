package progress_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

var ctx = context.Background()

// progClock is a settable clock.
type progClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *progClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *progClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func (c *progClock) Ms() int64 { return c.Now().UnixMilli() }

const (
	devA = "device-aaaa-1"
	devB = "device-bbbb-2"
)

// progEnv returns a service on a fresh database with two people and three 1-hour books.
func progEnv(t testing.TB) (*progress.Service, *db.Store, [2]string, []library.Book, *progClock) {
	t.Helper()
	st, err := db.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	users := [2]string{"us_a", "us_b"}
	for i, id := range users {
		role := "member"
		if i == 0 {
			role = "owner"
		}
		if _, err := st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, ?, 1)`, id, id, role); err != nil {
			t.Fatal(err)
		}
	}
	repo := library.New(st)
	_, f := librarytest.NewLibrary(t, repo, "Main", "/audio")
	var books []library.Book
	for i := 0; i < 3; i++ {
		books = append(books, librarytest.AddBook(t, repo, f, librarytest.BookSpec{Title: fmt.Sprintf("B%d", i), DurationMs: 3_600_000}))
	}
	clk := &progClock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	return progress.New(st, clk.Now), st, users, books, clk
}

func progSaveAt(t *testing.T, svc *progress.Service, user, book, dev string, at, pos int64) progress.Result {
	t.Helper()
	r, err := svc.Save(ctx, progress.Update{UserID: user, BookID: book, DeviceID: dev, ClientUpdatedAt: at, PositionMs: pos, DurationMs: 3_600_000})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestProgressSaveNewestWins(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	base := clk.Ms() - 100_000
	type step struct {
		name     string
		at, pos  int64
		finished bool
		dev      string
		applied  bool
		wantPos  int64
		wantFin  bool
		wantDev  string
	}
	steps := []step{
		{"insert applies", base, 1000, false, devA, true, 1000, false, devA},
		{"newer applies", base + 1, 2000, false, devB, true, 2000, false, devB},
		{"equal does not", base + 1, 3000, false, devA, false, 2000, false, devB},
		{"older does not", base - 5, 4000, false, devA, false, 2000, false, devB},
		{"finish applies", base + 10, 3_600_000, true, devA, true, 3_600_000, true, devA},
		{"finish re-apply keeps first finished_at", base + 20, 3_600_000, true, devB, true, 3_600_000, true, devB},
		{"unfinish newer clears", base + 30, 10, false, devA, true, 10, false, devA},
	}
	var firstFin int64
	for _, s := range steps {
		r, err := svc.Save(ctx, progress.Update{UserID: u[0], BookID: b, DeviceID: s.dev, ClientUpdatedAt: s.at, PositionMs: s.pos, DurationMs: 3_600_000, Finished: s.finished})
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		if r.Applied != s.applied || r.Progress.PositionMs != s.wantPos || r.Progress.Finished() != s.wantFin || r.Progress.DeviceID != s.wantDev {
			t.Errorf("%s: applied=%v progress=%+v", s.name, r.Applied, r.Progress)
		}
		if s.name == "finish applies" {
			firstFin = r.Progress.FinishedAt
		}
		if s.name == "finish re-apply keeps first finished_at" {
			clk.Advance(time.Second)
			if r.Progress.FinishedAt != firstFin {
				t.Errorf("finished_at = %d, want %d", r.Progress.FinishedAt, firstFin)
			}
		}
	}
	got, err := svc.Get(ctx, u[0], b)
	if err != nil || got.PositionMs != 10 || got.UserID != u[0] || got.BookID != b {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func TestProgressClampAhead(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	now := clk.Ms()
	r := progSaveAt(t, svc, u[0], b, devA, now+3_600_000, 1000)
	if !r.Applied || r.Progress.ClientUpdatedAt != now+progress.MaxAheadMs {
		t.Fatalf("stored client time = %d, want %d", r.Progress.ClientUpdatedAt, now+progress.MaxAheadMs)
	}
	// A device with a correct clock 30 s later is older than the clamped value.
	clk.Advance(30 * time.Second)
	if r := progSaveAt(t, svc, u[0], b, devB, clk.Ms(), 2000); r.Applied {
		t.Fatalf("30 s later applied: %+v", r.Progress)
	}
	// 61 s after the first save its time passes the clamped value.
	clk.Advance(31 * time.Second)
	if r := progSaveAt(t, svc, u[0], b, devB, clk.Ms(), 3000); !r.Applied || r.Progress.PositionMs != 3000 {
		t.Fatalf("61 s later: applied=%v %+v", r.Applied, r.Progress)
	}
}

func TestProgressValidation(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	ok := progress.Update{UserID: u[0], BookID: b, DeviceID: devA, ClientUpdatedAt: clk.Ms(), PositionMs: 5, DurationMs: 10}
	cases := []struct {
		field string
		mod   func(*progress.Update)
	}{
		{"user_id", func(x *progress.Update) { x.UserID = "" }},
		{"book_id", func(x *progress.Update) { x.BookID = "" }},
		{"device_id", func(x *progress.Update) { x.DeviceID = "short" }},
		{"device_id", func(x *progress.Update) { x.DeviceID = "has space in it" }},
		{"device_id", func(x *progress.Update) { x.DeviceID = strings.Repeat("a", 65) }},
		{"device_id", func(x *progress.Update) { x.DeviceID = progress.ServerDevice }},
		{"position_ms", func(x *progress.Update) { x.PositionMs = -1 }},
		{"duration_ms", func(x *progress.Update) { x.DurationMs = -1 }},
		{"client_updated_at", func(x *progress.Update) { x.ClientUpdatedAt = 0 }},
		{"listened_ms", func(x *progress.Update) { x.ListenedMs = -1 }},
		{"speed", func(x *progress.Update) { x.ListenedMs = 1000; x.Speed = 0 }},
		{"speed", func(x *progress.Update) { x.ListenedMs = 1000; x.Speed = 3.01 }},
		{"speed", func(x *progress.Update) { x.ListenedMs = 1000; x.Speed = 0.49 }},
	}
	for _, c := range cases {
		x := ok
		c.mod(&x)
		_, err := svc.Save(ctx, x)
		var ie *progress.InvalidError
		if !errors.Is(err, progress.ErrInvalid) || !errors.As(err, &ie) || ie.Field != c.field {
			t.Errorf("%s: err = %v", c.field, err)
		}
	}
	if _, err := svc.Get(ctx, u[0], b); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("a refused save stored a row: %v", err)
	}

	over := ok
	over.PositionMs = 99
	if r, err := svc.Save(ctx, over); err != nil || r.Progress.PositionMs != 10 {
		t.Fatalf("position over duration: %+v, %v", r, err)
	}

	lbl := ok
	lbl.ClientUpdatedAt++
	lbl.DeviceLabel = "\x07" + strings.Repeat("é", 40) // 80 bytes
	r, err := svc.Save(ctx, lbl)
	if err != nil || len(r.Progress.DeviceLabel) != 64 || r.Progress.DeviceLabel != strings.Repeat("é", 32) {
		t.Fatalf("label = %q (%d bytes), %v", r.Progress.DeviceLabel, len(r.Progress.DeviceLabel), err)
	}

	unknown := ok
	unknown.BookID = "bk_nope"
	if _, err := svc.Save(ctx, unknown); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("unknown book: %v", err)
	}
	unknown = ok
	unknown.UserID = "us_nope"
	if _, err := svc.Save(ctx, unknown); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestProgressConcurrentDevices(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	const perDevice = 50
	base := clk.Ms() - 1_000_000
	var wg sync.WaitGroup
	var mu sync.Mutex
	applied := map[int64]bool{}
	var maxAt int64
	var maxDev string
	for d, dev := range []string{devA, devB} {
		for i := 0; i < perDevice; i++ {
			at := base + int64(i*2+d+1)*1000 // distinct across both devices
			wg.Add(1)
			go func() {
				defer wg.Done()
				r, err := svc.Save(ctx, progress.Update{UserID: u[0], BookID: b, DeviceID: dev, ClientUpdatedAt: at, PositionMs: at - base, DurationMs: 3_600_000})
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				applied[at] = r.Applied
				if at > maxAt {
					maxAt, maxDev = at, dev
				}
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	got, err := svc.Get(ctx, u[0], b)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientUpdatedAt != maxAt || got.DeviceID != maxDev || got.PositionMs != maxAt-base {
		t.Fatalf("stored = %+v, want client %d device %s", got, maxAt, maxDev)
	}
	if !applied[maxAt] {
		t.Fatal("the newest write did not report applied")
	}
	var n int
	for _, a := range applied {
		if a {
			n++
		}
	}
	if n < 1 {
		t.Fatal("no write applied")
	}
}

func TestProgressForce(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	progSaveAt(t, svc, u[0], b, devA, clk.Ms()+progress.MaxAheadMs, 1000)
	clk.Advance(time.Second)
	// Without Force the correct-clock save loses.
	if r := progSaveAt(t, svc, u[0], b, devB, clk.Ms(), 2000); r.Applied {
		t.Fatal("expected the stored future-dated save to win")
	}
	r, err := svc.Save(ctx, progress.Update{UserID: u[0], BookID: b, DeviceID: devB, ClientUpdatedAt: clk.Ms(), PositionMs: 2000, DurationMs: 3_600_000, Force: true})
	if err != nil || !r.Applied || r.Progress.PositionMs != 2000 || r.Progress.DeviceID != devB {
		t.Fatalf("force: %+v, %v", r, err)
	}
	// A forced save on an empty row inserts.
	r, err = svc.Save(ctx, progress.Update{UserID: u[0], BookID: books[1].ID, DeviceID: devA, ClientUpdatedAt: 5, PositionMs: 1, Force: true})
	if err != nil || !r.Applied || r.Progress.ClientUpdatedAt != clk.Ms() {
		t.Fatalf("force insert: %+v, %v", r, err)
	}
}

func TestProgressSetFinished(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	progSaveAt(t, svc, u[0], b, devA, clk.Ms()+progress.MaxAheadMs, 1000)
	r, err := svc.SetFinished(ctx, u[0], b, true, 3_600_000)
	if err != nil || !r.Applied || !r.Progress.Finished() || r.Progress.PositionMs != 3_600_000 || r.Progress.DeviceID != progress.ServerDevice {
		t.Fatalf("finish: %+v, %v", r, err)
	}
	clk.Advance(time.Second)
	r, err = svc.SetFinished(ctx, u[0], b, false, 3_600_000)
	if err != nil || !r.Applied || r.Progress.Finished() || r.Progress.PositionMs != 0 || r.Progress.DeviceID != progress.ServerDevice {
		t.Fatalf("unfinish: %+v, %v", r, err)
	}
	if _, err := svc.SetFinished(ctx, u[0], "bk_nope", true, 1); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("unknown book: %v", err)
	}
	// A normal save cannot pass as the server device.
	if _, err := svc.Save(ctx, progress.Update{UserID: u[0], BookID: b, DeviceID: progress.ServerDevice, ClientUpdatedAt: 1}); !errors.Is(err, progress.ErrInvalid) {
		t.Fatalf("server device through Save: %v", err)
	}
}

func TestProgressPerUser(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	progSaveAt(t, svc, u[0], b, devA, clk.Ms()-1000, 111)
	progSaveAt(t, svc, u[1], b, devB, clk.Ms(), 222)
	a, err := svc.Get(ctx, u[0], b)
	if err != nil || a.PositionMs != 111 || a.DeviceID != devA {
		t.Fatalf("A = %+v, %v", a, err)
	}
	if _, err := svc.Get(ctx, u[1], books[1].ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("B on an untouched book: %v", err)
	}
	ids := make([]string, 0, 1201)
	for i := 0; i < 600; i++ {
		ids = append(ids, fmt.Sprintf("bk_missing_%d", i))
	}
	ids = append(ids, b)
	for i := 600; i < 1200; i++ {
		ids = append(ids, fmt.Sprintf("bk_missing_%d", i))
	}
	ids = append(ids, books[2].ID) // 1 202 ids across three chunks; the last one has no row
	m, err := svc.ForBooks(ctx, u[0], ids)
	if err != nil || len(m) != 1 || m[b].PositionMs != 111 {
		t.Fatalf("ForBooks = %v, %v", m, err)
	}
	if m, err := svc.ForBooks(ctx, u[0], nil); err != nil || m == nil || len(m) != 0 {
		t.Fatalf("empty ForBooks = %v, %v", m, err)
	}
}
