package upload

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// put writes a file below the library folder (slash form) and returns its absolute path.
func (e *env) put(rel string, data []byte) string {
	e.t.Helper()
	p := filepath.Join(e.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// handBook copies a book into the library folder by hand: one audio file per name, each with its own bytes.
func (e *env) handBook(dir string, names ...string) {
	e.t.Helper()
	for _, n := range names {
		e.put(dir+"/"+n, mp3(e.t, map[string]string{"title": dir + n}))
	}
}

func (e *env) scanAll() scan.Result {
	e.t.Helper()
	res, err := e.scanner().ScanLibrary(bg, e.lib.ID, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) bookAt(root string) string {
	e.t.Helper()
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books WHERE root_path = ?`, filepath.Join(e.root, filepath.FromSlash(root))).Scan(&id); err != nil {
		e.t.Fatalf("no book at %s: %v", root, err)
	}
	return id
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if _, err := e.st.Writer().Exec(q, args...); err != nil {
		e.t.Fatalf("%s: %v", q, err)
	}
}

func codeOf(err error) string {
	var ue *Error
	if errors.As(err, &ue) {
		return ue.Code
	}
	var je *jobs.Error
	if errors.As(err, &je) {
		return je.Code
	}
	return ""
}

// start plans and starts a removal of the book at root with the given mode; it returns the removal id.
func (e *env) start(root, mode string) string {
	e.t.Helper()
	id := e.bookAt(root)
	plan, err := e.svc.PlanRemoval(bg, id)
	if err != nil {
		e.t.Fatalf("plan: %v", err)
	}
	rid, err := e.svc.StartRemoval(bg, me, id, plan.Hash, mode)
	if err != nil {
		e.t.Fatalf("start: %v", err)
	}
	return rid
}

func (e *env) removalState(rid string) (state, code string) {
	e.t.Helper()
	rm, err := e.svc.Removal(bg, rid)
	if err != nil {
		e.t.Fatal(err)
	}
	return rm.State, rm.ErrorCode
}

func (e *env) bookCount() int { return countRows(e.t, e, "books") }

func (e *env) auditCount(action string) int {
	e.t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestPlanRemovalListsFilesExtrasAndStaying(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.put("Ann/Hand/cover.jpg", []byte("jpg"))
	e.put("Ann/Hand/info.nfo", []byte("nfo"))
	e.put("Ann/Hand/weird.bin", []byte("bin"))
	e.put("Ann/Hand/.hidden", []byte("h"))
	e.put("Ann/Hand/big.txt", make([]byte, maxExtraBytes+1))
	e.scanAll()
	plan, err := e.svc.PlanRemoval(bg, e.bookAt("Ann/Hand"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 2 || len(plan.Extras) != 2 || plan.Staying != 3 || len(plan.Dirs) != 1 || plan.RootIsFolder {
		t.Fatalf("plan = files %d extras %d staying %d dirs %d rootIsFolder %v", len(plan.Files), len(plan.Extras), plan.Staying, len(plan.Dirs), plan.RootIsFolder)
	}
	if plan.Files[0].Rel != "Ann/Hand/01.mp3" || len(plan.Hash) != 64 || plan.Bytes <= 0 {
		t.Errorf("plan = %+v", plan)
	}
	// Planning changes nothing.
	if got := len(tree(t, e.root)); got != 9 {
		t.Errorf("tree has %d entries: %v", got, tree(t, e.root))
	}
	// The same disk gives the same hash; a new sidecar file changes it.
	again, _ := e.svc.PlanRemoval(bg, e.bookAt("Ann/Hand"))
	if again.Hash != plan.Hash {
		t.Error("the hash is not stable")
	}
	e.put("Ann/Hand/new.nfo", []byte("n"))
	changed, _ := e.svc.PlanRemoval(bg, e.bookAt("Ann/Hand"))
	if changed.Hash == plan.Hash {
		t.Error("a new extra file did not change the hash")
	}
	if _, err := e.svc.StartRemoval(bg, me, e.bookAt("Ann/Hand"), plan.Hash, ModeFiles); codeOf(err) != CodePlanChanged {
		t.Errorf("stale hash: %v", err)
	}
	if _, err := e.svc.StartRemoval(bg, me, e.bookAt("Ann/Hand"), changed.Hash, "delete"); codeOf(err) != "bad_mode" {
		t.Errorf("bad mode: %v", err)
	}
	if _, err := e.svc.PlanRemoval(bg, "bk_nope"); codeOf(err) != "not_found" {
		t.Errorf("unknown book: %v", err)
	}
}

func TestPlanRemovalRootIsLibraryFolderHasNoExtrasOrDirs(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.put("cover.jpg", []byte("jpg"))
	e.put("Other/other.mp3", mp3(t, map[string]string{"title": "other"}))
	e.scanAll()
	e.exec(`UPDATE books SET root_path = ? WHERE root_path = ?`, e.root, filepath.Join(e.root, "Ann/Hand"))
	plan, err := e.svc.PlanRemoval(bg, e.bookAt(""))
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RootIsFolder || len(plan.Extras) != 0 || len(plan.Dirs) != 0 || len(plan.Files) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanRemovalSharedFolderMovesOnlyTheBooksOwnFiles(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Mix", "a1.mp3", "a2.mp3")
	e.handBook("Mix/Inner", "b1.mp3", "b2.mp3")
	e.put("Mix/cover.jpg", []byte("jpg"))
	e.scanAll()
	// Make the outer files one book rooted at the folder that also holds another book.
	var a1, a2 string
	e.st.Reader().QueryRow(`SELECT id FROM books WHERE root_path = ?`, filepath.Join(e.root, "Mix/a1.mp3")).Scan(&a1)
	e.st.Reader().QueryRow(`SELECT id FROM books WHERE root_path = ?`, filepath.Join(e.root, "Mix/a2.mp3")).Scan(&a2)
	if a1 == "" || a2 == "" {
		t.Fatalf("books: %q %q", a1, a2)
	}
	e.exec(`UPDATE audio_files SET book_id = ? WHERE book_id = ?`, a1, a2)
	e.exec(`DELETE FROM books WHERE id = ?`, a2)
	e.exec(`UPDATE books SET root_path = ? WHERE id = ?`, filepath.Join(e.root, "Mix"), a1)
	plan, err := e.svc.PlanRemoval(bg, a1)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Extras) != 0 || len(plan.Dirs) != 0 || len(plan.Files) != 2 || plan.Staying == 0 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestPlanRemovalRefusals(t *testing.T) {
	type tc struct {
		name string
		prep func(e *env, id string)
		want string
	}
	for _, c := range []tc{
		{"symlinked file", func(e *env, _ string) {
			p := filepath.Join(e.root, "Ann/Hand/01.mp3")
			os.Remove(p)
			os.Symlink(filepath.Join(e.root, "Ann/Hand/02.mp3"), p)
		}, CodeNotRemovable},
		{"symlinked root", func(e *env, _ string) {
			real := filepath.Join(e.root, "Ann/Real")
			os.Rename(filepath.Join(e.root, "Ann/Hand"), real)
			os.Symlink(real, filepath.Join(e.root, "Ann/Hand"))
		}, CodeNotRemovable},
		{"symlinked parent", func(e *env, _ string) {
			real := filepath.Join(e.root, "Real")
			os.Rename(filepath.Join(e.root, "Ann"), real)
			os.Symlink(real, filepath.Join(e.root, "Ann"))
		}, CodeNotRemovable},
		{"size changed", func(e *env, _ string) { e.put("Ann/Hand/01.mp3", []byte("shorter")) }, CodeChangedSinceScan},
		{"mtime changed", func(e *env, _ string) {
			old := time.Now().Add(-48 * time.Hour)
			os.Chtimes(filepath.Join(e.root, "Ann/Hand/01.mp3"), old, old)
		}, CodeChangedSinceScan},
		{"path with ..", func(e *env, id string) {
			e.exec(`UPDATE audio_files SET path = ? WHERE book_id = ? AND path LIKE '%01.mp3'`, e.root+"/Ann/../../x.mp3", id)
		}, CodeNotRemovable},
		{"path outside the folder", func(e *env, id string) {
			e.exec(`UPDATE audio_files SET path = '/etc/hostname' WHERE book_id = ? AND path LIKE '%01.mp3'`, id)
		}, CodeNotRemovable},
		{"root outside the folder", func(e *env, id string) { e.exec(`UPDATE books SET root_path = '/etc' WHERE id = ?`, id) }, CodeNotRemovable},
		{"root gone", func(e *env, _ string) { os.RemoveAll(filepath.Join(e.root, "Ann/Hand")) }, CodeNotRemovable},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.handBook("Ann/Hand", "01.mp3", "02.mp3")
			e.scanAll()
			id := e.bookAt("Ann/Hand")
			c.prep(e, id)
			_, err := e.svc.PlanRemoval(bg, id)
			if codeOf(err) != c.want {
				t.Fatalf("err = %v, want code %s", err, c.want)
			}
		})
	}
}

func TestPlanRemovalMissingFileIsSkippedAndListed(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.scanAll()
	os.Remove(filepath.Join(e.root, "Ann/Hand/02.mp3"))
	plan, err := e.svc.PlanRemoval(bg, e.bookAt("Ann/Hand"))
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || len(plan.Missing) != 1 || !strings.HasSuffix(plan.Missing[0], "02.mp3") {
		t.Fatalf("plan = %+v", plan)
	}
	os.Remove(filepath.Join(e.root, "Ann/Hand/01.mp3"))
	if _, err := e.svc.PlanRemoval(bg, e.bookAt("Ann/Hand")); codeOf(err) != CodeNotRemovable {
		t.Errorf("no file left: %v", err)
	}
}

func TestRemoveFilesMovesToHoldingAndForgetsTheBook(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.put("Ann/Hand/cover.jpg", []byte("jpg"))
	e.put("Ann/Hand/keep.bin", []byte("bin"))
	e.handBook("Ann/Other", "x.mp3")
	other := snapshot(t, filepath.Join(e.root, "Ann/Other"))
	e.scanAll()
	e.exec(`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, paired_by, ext, size, mtime_ns, quick_hash, state, created_at, updated_at)
  VALUES ('eb_1', 'library', ?, ?, ?, ?, 'folder', 'txt', 3, 1, ?, 'ready', 1, 1)`, e.lib.ID, e.folder.ID, filepath.Join(e.root, "Ann/Hand/read.txt"), e.bookAt("Ann/Hand"), strings.Repeat("a", 64))
	// The ebook row's file: make the record true.
	p := e.put("Ann/Hand/read.txt", []byte("txt"))
	fi, _ := os.Stat(p)
	q, _, _ := media.QuickHash(p)
	e.exec(`UPDATE ebook_files SET mtime_ns = ?, quick_hash = ? WHERE id = 'eb_1'`, fi.ModTime().UnixNano(), q)
	id := e.bookAt("Ann/Hand")
	plan, err := e.svc.PlanRemoval(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 3 || len(plan.Extras) != 1 || plan.Staying != 1 {
		t.Fatalf("plan = files %d extras %d staying %d", len(plan.Files), len(plan.Extras), plan.Staying)
	}
	rid, err := e.svc.StartRemoval(bg, me, id, plan.Hash, ModeFiles)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := e.removalState(rid); st != RemovalPlanned || len(e.enq.of(KindRemoveFiles)) != 1 {
		t.Fatalf("state %s, jobs %d", st, len(e.enq.of(KindRemoveFiles)))
	}
	if _, err := e.svc.StartRemoval(bg, me, id, plan.Hash, ModeFiles); codeOf(err) != CodeBusy {
		t.Errorf("second start: %v", err)
	}
	if e.bookCount() != 2 {
		t.Fatal("the book went before the files moved")
	}
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.removalState(rid); st != RemovalHeld {
		t.Fatalf("state = %s", st)
	}
	want := []string{
		".tendshelf-removed/", ".tendshelf-removed/" + rid + "/", ".tendshelf-removed/" + rid + "/Ann/", ".tendshelf-removed/" + rid + "/Ann/Hand/",
		".tendshelf-removed/" + rid + "/Ann/Hand/01.mp3", ".tendshelf-removed/" + rid + "/Ann/Hand/02.mp3",
		".tendshelf-removed/" + rid + "/Ann/Hand/cover.jpg", ".tendshelf-removed/" + rid + "/Ann/Hand/read.txt",
		"Ann/", "Ann/Hand/", "Ann/Hand/keep.bin", "Ann/Other/", "Ann/Other/x.mp3",
	}
	if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v", got)
	}
	if e.bookCount() != 1 || countRows(t, e, "audio_files") != 1 || countRows(t, e, "ebook_files") != 0 {
		t.Errorf("rows: books %d audio %d ebooks %d", e.bookCount(), countRows(t, e, "audio_files"), countRows(t, e, "ebook_files"))
	}
	if after := snapshot(t, filepath.Join(e.root, "Ann/Other")); !reflect.DeepEqual(other, after) {
		t.Error("another book changed")
	}
	rm, _ := e.svc.Removal(bg, rid)
	if rm.PurgeAfter != e.svc.nowMs()+int64(RemovedKeepDays)*24*3600*1000 && rm.PurgeAfter < e.svc.nowMs() {
		t.Errorf("purge_after = %d", rm.PurgeAfter)
	}
	if e.auditCount("book.remove_files") != 1 {
		t.Error("no audit row")
	}
	// A scan neither lists the holding folder nor brings the book back.
	e.scanAll()
	if e.bookCount() != 1 {
		t.Errorf("books after a scan = %d", e.bookCount())
	}
	held, err := e.svc.HeldRemovals(bg, e.lib.ID)
	if err != nil || len(held) != 1 || held[0].ID != rid {
		t.Errorf("held = %+v, %v", held, err)
	}
	if files, _ := e.svc.RemovalFiles(bg, rid); len(files) != 4 {
		t.Errorf("removal files = %v", files)
	}
}

func TestRemoveFilesMarksUploadLedgerRemoved(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3")
	e.scanAll()
	path := filepath.Join(e.root, "Ann/Hand/01.mp3")
	e.exec(`INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, created_at, updated_at) VALUES ('up_1', ?, ?, 'u', 'A', 'placed', 1, 1, 1)`, e.lib.ID, e.folder.ID)
	e.exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, created_at, updated_at) VALUES ('upp_1', 'up_1', ?, ?, 'file', 'present', 1, 1)`, e.folder.ID, path)
	rid := e.start("Ann/Hand", ModeFiles)
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	var st string
	if err := e.st.Reader().QueryRow(`SELECT state FROM upload_paths WHERE id = 'upp_1'`).Scan(&st); err != nil || st != "removed" {
		t.Errorf("ledger state = %q, %v", st, err)
	}
}

func TestRemoveFilesRefusesAFileEditedAfterThePlan(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(p string)
	}{
		{"same size, same time, other bytes", func(p string) {
			fi, _ := os.Stat(p)
			b, _ := os.ReadFile(p)
			b[len(b)-1] ^= 0xff
			b[0] ^= 0xff
			os.WriteFile(p, b, 0o644)
			os.Chtimes(p, fi.ModTime(), fi.ModTime())
		}},
		{"new time", func(p string) {
			old := time.Now().Add(-24 * time.Hour)
			os.Chtimes(p, old, old)
		}},
		{"removed", func(p string) { os.Remove(p) }},
		{"replaced by a link", func(p string) {
			os.Remove(p)
			os.Symlink("/etc/hostname", p)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, nil)
			e.handBook("Ann/Hand", "01.mp3", "02.mp3")
			e.scanAll()
			rid := e.start("Ann/Hand", ModeFiles)
			before := tree(t, e.root)
			tc.edit(filepath.Join(e.root, "Ann/Hand/02.mp3"))
			after := tree(t, e.root)
			_, err := e.svc.removeFiles(bg, nil, rid)
			if codeOf(err) != CodeChangedSinceScan {
				t.Fatalf("err = %v", err)
			}
			if got := tree(t, e.root); !reflect.DeepEqual(got, after) {
				t.Errorf("files moved: %v (before the edit %v)", got, before)
			}
			if st, code := e.removalState(rid); st != RemovalFailed || code != CodeChangedSinceScan {
				t.Errorf("state %s %s", st, code)
			}
			if e.bookCount() != 1 {
				t.Error("the book went")
			}
		})
	}
}

func TestRemoveFilesHardlinkedFileMovesAndTheOtherLinkStays(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3")
	e.scanAll()
	other := filepath.Join(t.TempDir(), "elsewhere.mp3")
	if err := os.Link(filepath.Join(e.root, "Ann/Hand/01.mp3"), other); err != nil {
		t.Skip("hard links not supported here")
	}
	want, _ := os.ReadFile(other)
	rid := e.start("Ann/Hand", ModeFiles)
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(other)
	if err != nil || string(got) != string(want) {
		t.Errorf("the other link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, ".tendshelf-removed", rid, "Ann/Hand/01.mp3")); err != nil {
		t.Errorf("not held: %v", err)
	}
}

func TestRemoveFilesReadOnlyFolderMovesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3")
	e.scanAll()
	rid := e.start("Ann/Hand", ModeFiles)
	before := tree(t, e.root)
	os.Chmod(e.root, 0o555)
	defer os.Chmod(e.root, 0o755)
	_, err := e.svc.removeFiles(bg, nil, rid)
	if codeOf(err) != CodeReadOnly {
		t.Fatalf("err = %v", err)
	}
	os.Chmod(e.root, 0o755)
	if got := tree(t, e.root); !reflect.DeepEqual(got, before) {
		t.Errorf("tree = %v", got)
	}
	if st, code := e.removalState(rid); st != RemovalFailed || code != CodeReadOnly || e.bookCount() != 1 {
		t.Errorf("state %s %s books %d", st, code, e.bookCount())
	}
}

func TestRemoveFilesFailedMoveRollsEverythingBack(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Book/CD1", "a.mp3")
	e.handBook("Ann/Book/CD2", "b.mp3")
	e.scanAll()
	rid := e.start("Ann/Book", ModeFiles)
	before := snapshot(t, e.root)
	// The second file's holding folder cannot be made: a file has its name.
	blocker := filepath.Join(e.root, HoldingDir, rid, "Ann/Book")
	if err := os.MkdirAll(blocker, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(blocker, "CD2"), []byte("in the way"), 0o644)
	_, err := e.svc.removeFiles(bg, nil, rid)
	if codeOf(err) != CodeCannotMove {
		t.Fatalf("err = %v", err)
	}
	if st, code := e.removalState(rid); st != RemovalFailed || code != CodeCannotMove {
		t.Errorf("state %s %s", st, code)
	}
	for _, rel := range []string{"Ann/Book/CD1/a.mp3", "Ann/Book/CD2/b.mp3"} {
		if _, err := os.Stat(filepath.Join(e.root, rel)); err != nil {
			t.Errorf("%s did not come back: %v", rel, err)
		}
	}
	after := snapshot(t, e.root)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed", k)
		}
	}
	if e.bookCount() != 1 {
		t.Error("the book went")
	}
}

func TestRemoveFilesCrashIsRolledBackByTheNextRunAndTheSweep(t *testing.T) {
	boom := errors.New("simulated crash")
	for _, how := range []string{"rerun", "sweep"} {
		t.Run(how, func(t *testing.T) {
			now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
			e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
			e.handBook("Ann/Hand", "01.mp3", "02.mp3")
			e.scanAll()
			rid := e.start("Ann/Hand", ModeFiles)
			before := snapshot(t, filepath.Join(e.root, "Ann"))
			e.svc.crash = func(p string) error {
				if p == "remove_files:after_move:1" {
					return boom
				}
				return nil
			}
			if _, err := e.svc.removeFiles(bg, nil, rid); !errors.Is(err, boom) {
				t.Fatalf("err = %v", err)
			}
			e.svc.crash = func(string) error { return nil }
			if st, _ := e.removalState(rid); st != RemovalMoving {
				t.Fatalf("state = %s", st)
			}
			if _, err := os.Stat(filepath.Join(e.root, "Ann/Hand/01.mp3")); err == nil {
				t.Fatal("the crash left the first file in place; the test proves nothing")
			}
			if how == "rerun" {
				_, err := e.svc.removeFiles(bg, nil, rid)
				if codeOf(err) != CodeInterrupted {
					t.Fatalf("rerun: %v", err)
				}
			} else {
				if err := e.svc.Sweep(bg); err != nil {
					t.Fatal(err)
				}
				if st, _ := e.removalState(rid); st != RemovalMoving {
					t.Fatal("the sweep touched a fresh removal")
				}
				now = now.Add(2 * time.Hour)
				if err := e.svc.Sweep(bg); err != nil {
					t.Fatal(err)
				}
			}
			if st, code := e.removalState(rid); st != RemovalFailed || code != CodeInterrupted {
				t.Errorf("state %s %s", st, code)
			}
			if after := snapshot(t, filepath.Join(e.root, "Ann")); !reflect.DeepEqual(before, after) {
				t.Errorf("files not back: %v", tree(t, e.root))
			}
			if e.bookCount() != 1 {
				t.Error("the book went")
			}
			if got := tree(t, e.root); len(got) != 4 || strings.Contains(strings.Join(got, ","), HoldingDir) {
				t.Errorf("tree = %v", got)
			}
		})
	}
}

func TestRestoreRemoval(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.put("Ann/Hand/cover.jpg", []byte("jpg"))
	e.scanAll()
	before := snapshot(t, filepath.Join(e.root, "Ann"))
	rid := e.start("Ann/Hand", ModeFiles)
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RestoreRemoval(bg, me, rid)
	if err != nil || len(res.Restored) != 3 || len(res.Kept) != 0 {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	if after := snapshot(t, filepath.Join(e.root, "Ann")); !reflect.DeepEqual(before, after) {
		t.Errorf("not as before: %v", tree(t, e.root))
	}
	if got := tree(t, e.root); strings.Contains(strings.Join(got, ","), HoldingDir) {
		t.Errorf("holding folder left: %v", got)
	}
	if st, _ := e.removalState(rid); st != RemovalRestored {
		t.Errorf("state = %s", st)
	}
	scans := e.enq.of(scan.KindScan)
	if len(scans) == 0 {
		t.Fatal("no scan queued")
	}
	e.scanAll()
	if e.bookCount() != 1 {
		t.Errorf("the scan did not bring the book back: %d", e.bookCount())
	}
	if _, err := e.svc.RestoreRemoval(bg, me, rid); codeOf(err) != "not_held" {
		t.Errorf("second restore: %v", err)
	}
	if e.auditCount("removal.restore") != 1 {
		t.Error("no audit row")
	}
}

func TestRestoreRemovalKeepsAFileWhoseOldPlaceIsTaken(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.scanAll()
	rid := e.start("Ann/Hand", ModeFiles)
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	// Someone put a different file at the old name.
	taken := e.put("Ann/Hand/01.mp3", []byte("a new file"))
	res, err := e.svc.RestoreRemoval(bg, me, rid)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Restored) != 1 || len(res.Kept) != 1 || res.Kept[0] != taken {
		t.Fatalf("restore = %+v", res)
	}
	if b, _ := os.ReadFile(taken); string(b) != "a new file" {
		t.Error("the new file was overwritten")
	}
	if _, err := os.Stat(filepath.Join(e.root, HoldingDir, rid, "Ann/Hand/01.mp3")); err != nil {
		t.Errorf("the kept file left the holding folder: %v", err)
	}
}

func TestPurgeRemovalAfterTheKeepPeriod(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	e.handBook("Ann/Hand", "01.mp3")
	e.handBook("Ann/Other", "x.mp3")
	e.scanAll()
	rid := e.start("Ann/Hand", ModeFiles)
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	now = now.Add(29 * 24 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.removalState(rid); st != RemovalHeld {
		t.Fatalf("purged early: %s", st)
	}
	now = now.Add(2 * 24 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.removalState(rid); st != RemovalPurged {
		t.Fatalf("state = %s", st)
	}
	want := []string{"Ann/", "Ann/Other/", "Ann/Other/x.mp3"}
	if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v", got)
	}
	if e.auditCount("removal.purge") != 1 {
		t.Error("no audit row")
	}
	if _, err := e.svc.RestoreRemoval(bg, me, rid); codeOf(err) != "not_held" {
		t.Errorf("restore after purge: %v", err)
	}
	if err := e.svc.PurgeRemoval(bg, me, rid); codeOf(err) != "not_held" {
		t.Errorf("purge twice: %v", err)
	}
}

func TestPurgeRemovalDeleteNow(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3")
	e.scanAll()
	rid := e.start("Ann/Hand", ModeFiles)
	if err := e.svc.PurgeRemoval(bg, me, rid); codeOf(err) != "not_held" {
		t.Fatalf("purge before the move: %v", err)
	}
	if _, err := e.svc.removeFiles(bg, nil, rid); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.PurgeRemoval(bg, me, rid); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, e.root); len(got) != 1 || got[0] != "Ann/" {
		t.Errorf("tree = %v", got)
	}
}

func TestKeepFilesRemovalKeepsTheScannerAway(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3", "02.mp3")
	e.put("Ann/Hand/read.epub", []byte("not a real epub"))
	e.handBook("Ann/Other", "x.mp3")
	e.scanAll()
	before := snapshot(t, e.root)
	rid := e.start("Ann/Hand", ModeKeepFiles)
	if st, _ := e.removalState(rid); st != RemovalHeld {
		t.Fatalf("state = %s", st)
	}
	if after := snapshot(t, e.root); !reflect.DeepEqual(before, after) {
		t.Error("files changed")
	}
	if e.bookCount() != 1 || len(e.enq.of(KindRemoveFiles)) != 0 {
		t.Errorf("books %d jobs %d", e.bookCount(), len(e.enq.of(KindRemoveFiles)))
	}
	if res := e.scanAll(); res.BooksKeptOut != 1 || e.bookCount() != 1 {
		t.Errorf("kept out %d, books %d", res.BooksKeptOut, e.bookCount())
	}
	if e.auditCount("book.remove_keep_files") != 1 {
		t.Error("no audit row")
	}
	if err := e.svc.PurgeRemoval(bg, me, rid); codeOf(err) != "not_held" {
		t.Errorf("purge of a keep-files removal: %v", err)
	}
	if _, err := e.svc.RestoreRemoval(bg, me, rid); err != nil {
		t.Fatal(err)
	}
	e.scanAll()
	if e.bookCount() != 2 {
		t.Errorf("books after show again = %d", e.bookCount())
	}
}

func TestScannerSkipsTheHoldingFolder(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook(HoldingDir+"/rmv_x/Ann/Hand", "01.mp3")
	e.handBook("Ann/Other", "x.mp3")
	e.scanAll()
	if e.bookCount() != 1 {
		t.Errorf("books = %d", e.bookCount())
	}
}

func TestKeptOutRootsOnlyForHeldKeepFiles(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3")
	e.handBook("Ann/Two", "01.mp3")
	e.scanAll()
	rid := e.start("Ann/Hand", ModeKeepFiles)
	roots, err := e.repo.KeptOutRoots(bg, e.lib.ID)
	if err != nil || len(roots) != 1 || !roots[filepath.Join(e.root, "Ann/Hand")] {
		t.Fatalf("roots = %v, %v", roots, err)
	}
	if _, err := e.svc.RestoreRemoval(bg, me, rid); err != nil {
		t.Fatal(err)
	}
	if roots, _ := e.repo.KeptOutRoots(bg, e.lib.ID); len(roots) != 0 {
		t.Errorf("roots after restore = %v", roots)
	}
}

func TestSweepFailsAPlannedRemovalWithNoJob(t *testing.T) {
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	e.handBook("Ann/Hand", "01.mp3")
	e.scanAll()
	rid := e.start("Ann/Hand", ModeFiles)
	now = now.Add(48 * time.Hour)
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if st, code := e.removalState(rid); st != RemovalFailed || code != "stuck" {
		t.Errorf("state %s %s", st, code)
	}
	// The book can be removed again.
	e.start("Ann/Hand", ModeFiles)
}

func TestMkdirAllInsideTheRoot(t *testing.T) {
	dir := t.TempDir()
	lr, err := openLroot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lr.Close()
	if err := lr.MkdirAll(filepath.Join(dir, "a/b/c"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := lr.MkdirAll(filepath.Join(dir, "a/b/c"), 0o755); err != nil {
		t.Errorf("again: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644)
	if err := lr.MkdirAll(filepath.Join(dir, "file/sub"), 0o755); err == nil {
		t.Error("made a folder below a file")
	}
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(dir, "link"))
	if err := lr.MkdirAll(filepath.Join(dir, "link/sub"), 0o755); err == nil {
		t.Error("went through a link")
	}
	if _, err := os.Stat(filepath.Join(outside, "sub")); err == nil {
		t.Error("created a folder outside the root")
	}
	if err := lr.MkdirAll(filepath.Join(outside, "x"), 0o755); err == nil {
		t.Error("made a folder outside the root")
	}
}

func TestExistingUploadRemoveIsNotOfferedTheHandPath(t *testing.T) {
	e := newEnv(t, nil)
	e.handBook("Ann/Hand", "01.mp3")
	e.scanAll()
	if e.svc.Removable(bg, e.bookAt("Ann/Hand")) {
		t.Error("a hand-added book is removable through the upload path")
	}
}
