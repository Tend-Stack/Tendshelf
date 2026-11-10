package radio

import (
	"fmt"
	"testing"
)

// TestReleaseDropsStationFromOrder: a station nobody listens to must not stay in the eviction order, or its
// old entry would later evict a title that is live.
func TestReleaseDropsStationFromOrder(t *testing.T) {
	f := newFixture(t, audioHandler("audio/mpeg"))
	rel, err := f.svc.Acquire("us_a", "rs_old")
	if err != nil {
		t.Fatal(err)
	}
	f.svc.SetTitle("rs_old", "x")
	rel()
	for _, id := range f.svc.order {
		if id == "rs_old" {
			t.Fatal("released station still in order")
		}
	}
	var rels []func()
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("rs_%d", i)
		r, err := f.svc.Acquire(fmt.Sprintf("us_%d", i), id)
		if err != nil {
			t.Fatal(err)
		}
		rels = append(rels, r)
		f.svc.SetTitle(id, "t")
	}
	if len(f.svc.order) != len(f.svc.titles) {
		t.Errorf("order %d titles %d", len(f.svc.order), len(f.svc.titles))
	}
	for _, r := range rels {
		r()
	}
	if len(f.svc.order) != 0 || len(f.svc.titles) != 0 {
		t.Errorf("after all released: order %d titles %d", len(f.svc.order), len(f.svc.titles))
	}
}
