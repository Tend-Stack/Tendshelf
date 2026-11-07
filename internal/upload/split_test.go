package upload

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// twoFolders is a zip of two book folders, each with two tracks.
func twoFolders(t *testing.T, extra ...zipEntry) []byte {
	tags := func(a, b string) []byte { return mp3(t, map[string]string{"album": a, "artist": b}) }
	one, two := tags("First Tale", "Ann Author"), tags("Second Tale", "Bob Writer")
	entries := []zipEntry{
		{name: "First Tale/01.mp3", data: one}, {name: "First Tale/02.mp3", data: one},
		{name: "Second Tale/01.mp3", data: two}, {name: "Second Tale/02.mp3", data: two},
	}
	return buildZip(t, append(entries, extra...))
}

// siblings returns the uploads the archive was split into.
func (e *env) siblings(id string) []View {
	e.t.Helper()
	v, err := e.svc.Siblings(bg, me, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// scanLast imports what the latest placement queued.
func (e *env) scanLast() string {
	e.t.Helper()
	var p scan.ScanPayload
	q := e.enq.of(scan.KindScan)
	if err := json.Unmarshal(q[len(q)-1].Payload, &p); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.scanner().ScanSubtree(bg, p.LibraryID, p.FolderID, p.Subdir, nil); err != nil {
		e.t.Fatal(err)
	}
	return p.Subdir
}

func TestSplitTwoFoldersAreTwoBooks(t *testing.T) {
	e := newEnv(t, nil)
	v := e.sendZip("Shelf", twoFolders(t, zipEntry{name: "readme.txt", data: []byte("loose")}))
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Fatalf("first book = %s", res.State)
	}
	sibs := e.siblings(v.ID)
	if len(sibs) != 1 || sibs[0].State != StateQueued || sibs[0].Label != "Second Tale" {
		t.Fatalf("siblings = %+v", sibs)
	}
	jobs := e.enq.of(KindPlace)
	if len(jobs) != 2 { // the zip's own job is queued by Finish; the sibling's by the split
		t.Fatalf("place jobs = %d", len(jobs))
	}
	if first := e.upload(v.ID); first.Label != "First Tale" || first.Skipped != 1 {
		t.Errorf("first book = %+v", first)
	}
	if res := e.mustPlace(sibs[0].ID); res.State != StatePlaced {
		t.Fatalf("second book = %s", res.State)
	}
	got := strings.Join(tree(t, e.root), "\n")
	for _, want := range []string{"Ann Author/First Tale/01.mp3", "Bob Writer/Second Tale/02.mp3"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "readme.txt") {
		t.Error("the loose file was placed")
	}
	// Each book has its own ledger rows.
	if len(e.ledger(v.ID)) == 0 || len(e.ledger(sibs[0].ID)) == 0 {
		t.Error("a book has no ledger rows")
	}
	// The archive is gone from staging and nothing else is stranded.
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); err == nil {
		t.Error("the first book's staging was kept")
	}
	if _, err := os.Stat(e.svc.stagingDir(sibs[0].ID)); err == nil {
		t.Error("the sibling's staging was kept")
	}
}

func TestSplitOneFolderStaysOneBook(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
	}{
		{"one folder", []zipEntry{{name: "Only/01.mp3", data: goodTrack(t)}}},
		{"one folder and loose files", []zipEntry{{name: "Only/01.mp3", data: goodTrack(t)}, {name: "cover.jpg", data: []byte("jpg")}}},
		{"one folder with audio, one without", []zipEntry{{name: "Only/01.mp3", data: goodTrack(t)}, {name: "Extras/a.jpg", data: []byte("jpg")}}},
		{"flat", []zipEntry{{name: "01.mp3", data: goodTrack(t)}, {name: "02.mp3", data: goodTrack(t)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			v := e.sendZip("Zed Author - Zip Title", buildZip(t, tc.entries))
			if res := e.mustPlace(v.ID); res.State != StatePlaced {
				t.Fatalf("state = %s", res.State)
			}
			if s := e.siblings(v.ID); len(s) != 0 {
				t.Errorf("siblings = %+v", s)
			}
		})
	}
}

func TestSplitOneBadBookIsRefusedAlone(t *testing.T) {
	enc := fakeAudio(t, mediatest.FakeFile{Container: "mp4", Encrypted: true})
	tests := []struct {
		name  string
		bad   string // the folder that holds the bad file
		entry zipEntry
	}{
		{"aax in the second folder", "Second Tale", zipEntry{name: "Second Tale/03.aax", data: []byte("sealed")}},
		{"aax in the first folder", "First Tale", zipEntry{name: "First Tale/03.aax", data: []byte("sealed")}},
		{"encrypted stream in the second", "Second Tale", zipEntry{name: "Second Tale/03.m4a", data: enc}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			v := e.sendZip("Shelf", twoFolders(t, tc.entry))
			res, err := e.place(v.ID)
			if err != nil {
				t.Fatal(err)
			}
			sibs := e.siblings(v.ID)
			if len(sibs) != 1 {
				t.Fatalf("siblings = %+v", sibs)
			}
			all := []View{{ID: v.ID, Label: e.upload(v.ID).Label, State: res.(PlaceResult).State}, sibs[0]}
			for i := 1; i < len(all); i++ {
				if all[i].State == StateQueued {
					r, err := e.place(all[i].ID)
					if err != nil {
						t.Fatal(err)
					}
					all[i].State = r.(PlaceResult).State
				}
			}
			for _, b := range all {
				want := StatePlaced
				if b.Label == tc.bad {
					want = StateRefused
				}
				if b.State != want {
					t.Errorf("%s is %s, want %s", b.Label, b.State, want)
				}
			}
			got := strings.Join(tree(t, e.root), "\n")
			if strings.Contains(got, tc.bad) || len(got) == 0 {
				t.Errorf("tree = %s", got)
			}
			for _, b := range all {
				if b.Label == tc.bad {
					continue
				}
				if _, err := os.Stat(e.svc.stagingDir(b.ID)); err == nil {
					t.Errorf("staging of %s kept", b.Label)
				}
			}
		})
	}
}

func TestSplitZipWideBombRefusesEverything(t *testing.T) {
	e := newEnv(t, nil)
	bomb := zipEntry{name: "Second Tale/pad.txt", data: bytes.Repeat([]byte{0}, 3<<20)}
	v := e.sendZip("Shelf", twoFolders(t, bomb))
	if _, err := e.place(v.ID); err == nil || e.upload(v.ID).ErrorCode != "zip_bomb" {
		t.Fatalf("err = %v, code %q", err, e.upload(v.ID).ErrorCode)
	}
	if s := e.siblings(v.ID); len(s) != 0 {
		t.Errorf("siblings = %+v", s)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("library changed: %v", got)
	}
}

func TestSplitSlipNameRefusesEverything(t *testing.T) {
	e := newEnv(t, nil)
	v := e.sendZip("Shelf", twoFolders(t, zipEntry{name: "Second Tale/../../evil.mp3", data: goodTrack(t)}))
	if _, err := e.place(v.ID); err == nil || e.upload(v.ID).ErrorCode != "bad_path" {
		t.Fatalf("err = %v, code %q", err, e.upload(v.ID).ErrorCode)
	}
	if len(e.siblings(v.ID)) != 0 || len(tree(t, e.root)) != 0 {
		t.Error("something was made")
	}
}

func TestSplitQuotaCountsTheArchiveOnce(t *testing.T) {
	z := twoFolders(t)
	e := newEnv(t, nil)
	v := e.sendZip("Shelf", z)
	e.mustPlace(v.ID)
	var unpacked int64
	rows, err := e.st.Reader().Query(`SELECT COALESCE(SUM(size), 0) FROM upload_files`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		_ = rows.Scan(&unpacked)
	}
	rows.Close()
	var total, n int64
	if err := e.st.Reader().QueryRow(`SELECT COALESCE(SUM(bytes_total), 0), count(*) FROM uploads`).Scan(&total, &n); err != nil {
		t.Fatal(err)
	}
	if n != 2 || total != unpacked {
		t.Errorf("%d uploads count %d bytes, the files are %d (the archive is %d)", n, total, unpacked, len(z))
	}
}

func TestSplitRemoveLeavesTheOtherBook(t *testing.T) {
	e := newEnv(t, nil)
	v := e.sendZip("Shelf", twoFolders(t))
	e.mustPlace(v.ID)
	e.scanLast()
	sib := e.siblings(v.ID)[0]
	e.mustPlace(sib.ID)
	e.scanLast()
	var bookID string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books WHERE root_path LIKE '%First Tale%' LIMIT 1`).Scan(&bookID); err != nil {
		t.Fatalf("cannot find the first book: %v", err)
	}
	if _, err := e.svc.Remove(bg, me, bookID, ""); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(tree(t, e.root), "\n")
	if strings.Contains(got, "First Tale") || !strings.Contains(got, "Bob Writer/Second Tale/01.mp3") {
		t.Errorf("tree = %s", got)
	}
}

func TestSplitSweepWithAStuckSibling(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	clock := now
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return clock } })
	v := e.sendZip("Shelf", twoFolders(t))
	e.mustPlace(v.ID)
	sib := e.siblings(v.ID)[0]
	dir := e.svc.stagingDir(sib.ID)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("sibling staging: %v", err)
	}
	// Its job never ran: after a day it is failed, after a week expired, and its staging goes.
	clock = now.Add(25 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if u := e.upload(sib.ID); u.State != StateFailed || u.ErrorCode != "stuck" {
		t.Fatalf("sibling = %+v", u)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Error("a failed sibling lost its files before the week was out")
	}
	clock = now.Add(9 * 24 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the expired sibling's staging stayed")
	}
	// The first book (placed) is untouched by all this.
	if !strings.Contains(strings.Join(tree(t, e.root), "\n"), "First Tale") {
		t.Error("the placed book is gone")
	}
}
