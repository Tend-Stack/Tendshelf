package progress_test

import (
	"fmt"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

func kosRow(i int, at int64) progress.KosyncRow {
	return progress.KosyncRow{Document: fmt.Sprintf("%032x", i), Progress: "p", Percentage: 0.5, Device: "d", DeviceID: "id", UpdatedAt: at}
}

func TestKosyncPutGetAndReplace(t *testing.T) {
	svc, _, u, _, _ := progEnv(t)
	if _, ok, err := svc.KosyncGet(ctx, u[0], kosRow(1, 1).Document); ok || err != nil {
		t.Fatalf("empty get = %v %v", ok, err)
	}
	if err := svc.KosyncPut(ctx, u[0], kosRow(1, 10)); err != nil {
		t.Fatal(err)
	}
	r := kosRow(1, 20)
	r.Progress, r.Percentage = "/body/DocFragment[2]/body", 0.75
	if err := svc.KosyncPut(ctx, u[0], r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := svc.KosyncGet(ctx, u[0], r.Document)
	if err != nil || !ok || got != r {
		t.Fatalf("get = %+v %v %v, want %+v", got, ok, err, r)
	}
	if _, ok, _ := svc.KosyncGet(ctx, u[1], r.Document); ok {
		t.Error("another person read the row")
	}
}

// TestKosyncCapDropsTheOldest: a person keeps KosyncMaxRows documents; the least recently updated go.
func TestKosyncCapDropsTheOldest(t *testing.T) {
	svc, st, u, _, _ := progEnv(t)
	tx, err := st.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= progress.KosyncMaxRows; i++ {
		r := kosRow(i, int64(i))
		if _, err := tx.Exec(`INSERT INTO kosync_progress(user_id, document, progress, percentage, device, device_id, updated_at) VALUES (?,?,?,?,?,?,?)`,
			u[0], r.Document, r.Progress, r.Percentage, r.Device, r.DeviceID, r.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := svc.KosyncPut(ctx, u[1], kosRow(1, 5)); err != nil { // another person's rows do not count
		t.Fatal(err)
	}
	if err := svc.KosyncPut(ctx, u[0], kosRow(progress.KosyncMaxRows+1, 1_000_000)); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM kosync_progress WHERE user_id = ?`, u[0]).Scan(&n); err != nil || n != progress.KosyncMaxRows {
		t.Fatalf("rows = %d, %v", n, err)
	}
	if _, ok, _ := svc.KosyncGet(ctx, u[0], kosRow(1, 1).Document); ok {
		t.Error("the oldest row survived")
	}
	if _, ok, _ := svc.KosyncGet(ctx, u[0], kosRow(2, 1).Document); !ok {
		t.Error("the second oldest was dropped too")
	}
	if _, ok, _ := svc.KosyncGet(ctx, u[1], kosRow(1, 1).Document); !ok {
		t.Error("another person's row was dropped")
	}
}
