package ebook

import (
	"os"
	"testing"
	"time"
)

func (e *ienv) digest(id string) string {
	e.t.Helper()
	var d *string
	if err := e.st.Reader().QueryRow(`SELECT partial_md5 FROM ebook_files WHERE id = ?`, id).Scan(&d); err != nil {
		e.t.Fatal(err)
	}
	if d == nil {
		return ""
	}
	return *d
}

func (e *ienv) waitDigest(id string) string {
	e.t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if d := e.digest(id); d != "" {
			return d
		}
	}
	e.t.Fatalf("file %s never got a digest", id)
	return ""
}

// TestPartialMD5IngestAndBackfill: the ingest job sets the digest, a changed file loses it, and the
// backfill job restores it for ready files that lack one.
func TestPartialMD5IngestAndBackfill(t *testing.T) {
	e := newIEnv(t, true)
	p := e.write("a.epub", sampleEPUB(t, "Digest One"))
	e.scan()
	f := e.waitState(e.fileAt(p).ID)
	if f.State != "ready" {
		t.Fatalf("state = %s", f.State)
	}
	want, err := PartialMD5(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.digest(f.ID); got != want {
		t.Fatalf("ingest digest = %q, want %q", got, want)
	}

	// A file read before the column existed: the backfill finds it.
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET partial_md5 = NULL WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueueDigests(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.waitDigest(f.ID); got != want {
		t.Fatalf("backfilled digest = %q, want %q", got, want)
	}

	// Nothing to do: no second job.
	if err := e.svc.EnqueueDigests(bg); err != nil {
		t.Fatal(err)
	}
}

// TestPartialMD5ChangedFileLosesDigest: a file whose bytes change goes back to pending without a digest.
func TestPartialMD5ChangedFileLosesDigest(t *testing.T) {
	e := newIEnv(t, false)
	p := e.write("a.epub", []byte("first bytes"))
	e.scan()
	f := e.fileAt(p)
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET state='ready', partial_md5 = '0123456789abcdef0123456789abcdef' WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	e.write("a.epub", []byte("second, longer bytes"))
	e.scan()
	if got := e.digest(f.ID); got != "" {
		t.Errorf("digest after a change = %q", got)
	}
	if again := e.file(f.ID); again.State != "pending" {
		t.Errorf("state = %s", again.State)
	}
}

// TestPartialMD5BackfillPastUnreadable: a ready file that cannot be read does not stop the others.
func TestPartialMD5BackfillPastUnreadable(t *testing.T) {
	e := newIEnv(t, true)
	pa := e.write("a.epub", sampleEPUB(t, "Digest A"))
	pb := e.write("b.epub", sampleEPUB(t, "Digest B"))
	e.scan()
	fa, fb := e.waitState(e.fileAt(pa).ID), e.waitState(e.fileAt(pb).ID)
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET partial_md5 = NULL`); err != nil {
		t.Fatal(err)
	}
	first := fa
	if fb.ID < fa.ID {
		first = fb
	}
	firstPath := pa
	if first.ID == fb.ID {
		firstPath = pb
	}
	other := fa.ID
	if first.ID == fa.ID {
		other = fb.ID
	}
	if err := osRemove(firstPath); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueueDigests(bg); err != nil {
		t.Fatal(err)
	}
	e.waitDigest(other)
	if got := e.digest(first.ID); got != "" {
		t.Errorf("an unreadable file got a digest %q", got)
	}
}

func osRemove(p string) error { return os.Remove(p) }
