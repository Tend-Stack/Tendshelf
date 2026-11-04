package progress_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

func rrSave(t *testing.T, svc *progress.Service, user, book, dev string, at int64, pct int) (progress.ReadingPosition, bool) {
	t.Helper()
	p, applied, err := svc.SaveReading(ctx, progress.ReadingSave{UserID: user, BookID: book, DeviceID: dev, ClientUpdatedAt: at,
		ChapterIdx: 1, SID: "c1.p1.s0", Off: 3, Quote: "Hello", CharPos: pct, PercentBP: pct})
	if err != nil {
		t.Fatal(err)
	}
	return p, applied
}

func TestReadingRepoNewestWins(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	base := clk.Ms() - 100_000
	for _, s := range []struct {
		name    string
		at      int64
		pct     int
		dev     string
		applied bool
		want    int
		wantDev string
	}{
		{"insert", base, 100, devA, true, 100, devA},
		{"newer", base + 1, 200, devB, true, 200, devB},
		{"equal does not", base + 1, 300, devA, false, 200, devB},
		{"older does not", base - 5, 400, devA, false, 200, devB},
		{"future is clamped, still newest", clk.Ms() + 10*3_600_000, 500, devA, true, 500, devA},
	} {
		p, applied := rrSave(t, svc, u[0], b, s.dev, s.at, s.pct)
		if applied != s.applied || p.PercentBP != s.want || p.DeviceID != s.wantDev {
			t.Errorf("%s: applied=%v pct=%d dev=%s", s.name, applied, p.PercentBP, p.DeviceID)
		}
	}
	got, ok, err := svc.Reading(ctx, u[0], b)
	if err != nil || !ok || got.ClientUpdatedAt > clk.Ms()+progress.MaxAheadMs {
		t.Fatalf("future stamp not clamped: %+v %v %v", got, ok, err)
	}
	// Reading never touches listening progress.
	if p, err := svc.Get(ctx, u[0], b); err == nil && (p.PositionMs != 0 || p.ClientUpdatedAt != 0) {
		t.Errorf("listening progress written: %+v", p)
	}
	for name, bad := range map[string]progress.ReadingSave{
		"chapter 0": {UserID: u[0], BookID: b, DeviceID: devA, ClientUpdatedAt: 1, ChapterIdx: 0},
		"percent":   {UserID: u[0], BookID: b, DeviceID: devA, ClientUpdatedAt: 1, ChapterIdx: 1, PercentBP: 10001},
		"device":    {UserID: u[0], BookID: b, DeviceID: "x", ClientUpdatedAt: 1, ChapterIdx: 1},
		"sid":       {UserID: u[0], BookID: b, DeviceID: devA, ClientUpdatedAt: 1, ChapterIdx: 1, SID: "abc"},
	} {
		if _, _, err := svc.SaveReading(ctx, bad); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Finished and read again.
	p, err := svc.SetReadingFinished(ctx, u[0], b, true)
	if err != nil || !p.Finished() || p.PercentBP != 10000 {
		t.Fatalf("finish: %+v %v", p, err)
	}
	p, err = svc.SetReadingFinished(ctx, u[0], b, false)
	if err != nil || p.Finished() || p.PercentBP != 0 || p.CharPos != 0 {
		t.Fatalf("unfinish: %+v %v", p, err)
	}
}

func TestReadingRepoSpeed(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	now := clk.Ms()
	min1 := int64(60_000)
	add := func(i int, chars, active int64) {
		t.Helper()
		// A fresh device per session keeps each one separate; times are an hour apart anyway.
		dev := "device-sess-" + string(rune('a'+i))
		if err := svc.AddReadingTime(ctx, u[0], books[0].ID, dev, chars, active, now+int64(i)*3_600_000); err != nil {
			t.Fatal(err)
		}
	}
	if cpm, learned := svc.ReadingSpeed(ctx, u[0]); cpm != 1000 || learned {
		t.Fatalf("default = %d %v", cpm, learned)
	}
	add(0, 2000, 2*min1) // 1000 cpm
	add(1, 6000, 2*min1) // 3000 cpm
	add(2, 100, 30_000)  // under a minute: teaches nothing
	if _, learned := svc.ReadingSpeed(ctx, u[0]); learned {
		t.Fatal("learned from two usable sessions")
	}
	add(3, 4000, 2*min1) // 2000 cpm: third usable session
	cpm, learned := svc.ReadingSpeed(ctx, u[0])
	if !learned || cpm != 2000 { // (2000+6000+4000) chars over 6 minutes
		t.Fatalf("speed = %d %v, want 2000 true", cpm, learned)
	}
	// Clamp both ways.
	svc2, _, u2, books2, clk2 := progEnv(t)
	for i, chars := range []int64{100, 100, 100} {
		dev := "device-slow-" + string(rune('a'+i))
		if err := svc2.AddReadingTime(ctx, u2[0], books2[0].ID, dev, chars, 5*min1, clk2.Ms()+int64(i)*3_600_000); err != nil {
			t.Fatal(err)
		}
	}
	if cpm, learned := svc2.ReadingSpeed(ctx, u2[0]); !learned || cpm != progress.ReadingSpeedMin {
		t.Errorf("slow = %d %v, want %d", cpm, learned, progress.ReadingSpeedMin)
	}
	svc3, _, u3, books3, clk3 := progEnv(t)
	for i := 0; i < 3; i++ {
		dev := "device-fast-" + string(rune('a'+i))
		if err := svc3.AddReadingTime(ctx, u3[0], books3[0].ID, dev, 90_000, min1, clk3.Ms()+int64(i)*3_600_000); err != nil {
			t.Fatal(err)
		}
	}
	if cpm, _ := svc3.ReadingSpeed(ctx, u3[0]); cpm != progress.ReadingSpeedMax {
		t.Errorf("fast = %d, want %d", cpm, progress.ReadingSpeedMax)
	}
	// One device within 30 minutes is one session; after a longer pause it is a new one.
	svc4, st4, u4, books4, clk4 := progEnv(t)
	t0 := clk4.Ms()
	for _, at := range []int64{t0, t0 + 10*min1, t0 + 20*min1, t0 + 80*min1} {
		if err := svc4.AddReadingTime(ctx, u4[0], books4[0].ID, devA, 100, 1000, at); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := st4.Reader().QueryRow(`SELECT count(*) FROM reading_sessions`).Scan(&n); err != nil || n != 2 {
		t.Errorf("sessions = %d, %v; want 2", n, err)
	}
}

func TestReadingRepoPrivate(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	rrSave(t, svc, u[0], b, devA, clk.Ms()-1000, 4000)
	if _, ok, err := svc.Reading(ctx, u[1], b); err != nil || ok {
		t.Errorf("another person sees a place: %v %v", ok, err)
	}
	// Saving as the other person writes their own row only.
	rrSave(t, svc, u[1], b, devB, clk.Ms()-500, 9000)
	if p, _, _ := svc.Reading(ctx, u[0], b); p.PercentBP != 4000 {
		t.Errorf("first person's place changed: %+v", p)
	}
	if _, _, err := svc.SaveReading(ctx, progress.ReadingSave{UserID: u[0], BookID: "nobook", DeviceID: devA, ClientUpdatedAt: 5, ChapterIdx: 1}); err == nil {
		t.Error("saved on an unknown book")
	}
}
