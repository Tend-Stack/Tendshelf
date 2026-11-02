package mediajobs

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestCoverRetintAfterScan: a cover stored without a tint (as before the colour counter learned busy
// artwork) is queued by the next scan of its library, and the job fills the tint and accent again.
// A cover that has a tint is left alone.
func TestCoverRetintAfterScan(t *testing.T) {
	e := newEnv(t)
	e.fake("Plain/01.m4b", mediatest.FakeFile{})
	e.image("Plain/cover.jpg", "#2c6660", 400, 400)
	e.fake("Done/01.m4b", mediatest.FakeFile{})
	e.image("Done/cover.jpg", "#c83c3c", 400, 400)
	e.scanAll()
	c := e.covers()
	e.run(c, "Plain")
	e.run(c, "Done")
	want := e.oneCover("Plain")
	if want.Tint == "" || want.Accent == nil {
		t.Fatalf("the fixture cover has no tint: %+v", want)
	}

	// Back to the old state: the tint and accent of one cover were never stored.
	if _, err := e.st.Writer().Exec(`UPDATE covers SET tint = NULL, accent_json = NULL WHERE id = ?`, want.ID); err != nil {
		t.Fatal(err)
	}
	e.sc.OnScanFinished(c.Finisher())
	before := e.enq.count(KindCover)
	if _, err := e.sc.ScanLibrary(bg, e.lib.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.enq.count(KindCover) - before; got != 1 {
		t.Fatalf("the scan queued %d cover jobs, want 1 (the book without a tint)", got)
	}
	if last := e.enq.ids[len(e.enq.ids)-1]; last != e.book("Plain").ID {
		t.Errorf("queued book %s, want the one without a tint", last)
	}

	// A second scan while the job still waits queues nothing more.
	if _, err := e.sc.ScanLibrary(bg, e.lib.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := e.enq.count(KindCover) - before; got != 1 {
		t.Errorf("a waiting job was queued again: %d jobs", got)
	}

	if out := e.run(c, "Plain"); out != OutcomeSet {
		t.Errorf("the job's outcome = %s, want %s", out, OutcomeSet)
	}
	got := e.oneCover("Plain")
	if got.Tint != want.Tint || string(got.Accent) != string(want.Accent) {
		t.Errorf("after the job tint %q accent %s, want %q %s", got.Tint, got.Accent, want.Tint, want.Accent)
	}
}
