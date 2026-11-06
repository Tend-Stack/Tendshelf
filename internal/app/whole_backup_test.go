package app

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/backups"
	"github.com/Tend-Stack/tendshelf/internal/db"
)

// TestWholeBackupRestoreStarts proves the restore steps in RESTORE.txt: a data folder is backed up to a
// backup folder, then rebuilt in an empty folder by copying exactly what the note says (the five folders and
// the newest database copy as tendshelf.db), and the program starts on it with the data intact.
func TestWholeBackupRestoreStarts(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	target := t.TempDir()
	st, err := db.Open(ctx, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(`INSERT INTO libraries(id, name, created_at) VALUES ('lib_restore', 'Restored shelf', 1)`); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"ebooks/ab/book.epub": "epub bytes", "covers/c.jpg": "cover bytes", "people/p.jpg": "person bytes",
		"text/t.json": "text bytes", "dictionaries/en.dict": "dictionary bytes",
	}
	for rel, text := range files {
		p := filepath.Join(data, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := backups.New(st, nil, data, nil, nil)
	if err := svc.SetFolder(ctx, target); err != nil {
		t.Fatal(err)
	}
	if res, err := svc.Backup(ctx, "manual"); err != nil || !res.Folder || res.Files != len(files) {
		t.Fatalf("backup = %+v, %v", res, err)
	}
	st.Close()

	// The restore, by hand, as the note describes it, into an empty folder.
	root := filepath.Join(target, backups.RootName)
	restored := t.TempDir()
	for _, name := range backups.Mirrored {
		copyTree(t, filepath.Join(root, name), filepath.Join(restored, name))
	}
	copies, err := db.ListCopies(root)
	if err != nil || len(copies) == 0 {
		t.Fatalf("no database copy in the backup: %v", err)
	}
	copyFile(t, filepath.Join(root, "backups", copies[0].Name), filepath.Join(restored, db.FileName))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, stop := startApp(t, Config{Listener: ln, DataDir: restored, Addr: "127.0.0.1:1"})
	resp, err := http.Get("http://" + ln.Addr().String() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d", resp.StatusCode)
	}
	if err := stop(); err != nil {
		t.Fatalf("Run = %v", err)
	}

	back, err := db.Open(ctx, restored)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	var name string
	if err := back.Reader().QueryRow(`SELECT name FROM libraries WHERE id = 'lib_restore'`).Scan(&name); err != nil || name != "Restored shelf" {
		t.Errorf("library after restore = %q, %v", name, err)
	}
	for rel, text := range files {
		got, err := os.ReadFile(filepath.Join(restored, filepath.FromSlash(rel)))
		if err != nil || string(got) != text {
			t.Errorf("%s after restore = %q, %v", rel, got, err)
		}
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		copyFile(t, p, filepath.Join(to, rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
