package users

import (
	"context"
	"errors"
	"testing"
)

func TestReadStatsGoalStore(t *testing.T) {
	r := newRepo(t, Options{})
	ctx := context.Background()
	u, err := createOwner(t, r, goodUser("ana"))
	if err != nil {
		t.Fatal(err)
	}
	if g, err := r.LoadReadingGoal(ctx, u.ID); err != nil || g.Books != 0 {
		t.Fatalf("default = %+v, %v", g, err)
	}
	for _, n := range []int{0, 1, 365} {
		if err := r.SaveReadingGoal(ctx, u.ID, ReadingGoal{Books: n}); err != nil {
			t.Fatalf("save %d: %v", n, err)
		}
		if g, err := r.LoadReadingGoal(ctx, u.ID); err != nil || g.Books != n {
			t.Errorf("round trip %d = %+v, %v", n, g, err)
		}
	}
	for _, n := range []int{-1, 366} {
		if err := r.SaveReadingGoal(ctx, u.ID, ReadingGoal{Books: n}); !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("save %d = %v, want ErrInvalidSetting", n, err)
		}
	}
	if g, _ := r.LoadReadingGoal(ctx, u.ID); g.Books != 365 {
		t.Errorf("a refused save changed the goal to %d", g.Books)
	}
	// A broken or out-of-range stored row means no goal.
	if _, err := r.st.Writer().Exec(`UPDATE user_settings SET value_json = '{"goalBooks":999}' WHERE user_id = ? AND key = 'reading.v1'`, u.ID); err != nil {
		t.Fatal(err)
	}
	if g, err := r.LoadReadingGoal(ctx, u.ID); err != nil || g.Books != 0 {
		t.Errorf("stored 999 loads as %+v, %v", g, err)
	}
	if g, err := r.LoadReadingGoal(ctx, "someone-else"); err != nil || g.Books != 0 {
		t.Errorf("another person sees %+v, %v", g, err)
	}
}
