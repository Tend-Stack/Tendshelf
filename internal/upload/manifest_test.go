package upload

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func wantErr(t *testing.T, err error, status int, code string) *Error {
	t.Helper()
	var ue *Error
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *Error %d %s", err, status, code)
	}
	if ue.Status != status || ue.Code != code {
		t.Fatalf("err = %d %s (%s), want %d %s", ue.Status, ue.Code, ue.Message, status, code)
	}
	return ue
}

func files(paths ...string) Manifest {
	m := Manifest{Label: "Book"}
	for _, p := range paths {
		m.Files = append(m.Files, FileIn{Path: p, Size: 10, ModifiedMs: 1})
	}
	return m
}

func TestCreateManifestErrors(t *testing.T) {
	e := newEnv(t, nil)
	for _, c := range []struct {
		name   string
		m      Manifest
		status int
		code   string
		field  string
	}{
		{"no files", Manifest{}, 422, "no_audio", ""},
		{"no audio", files("cover.jpg", "notes.txt"), 422, "no_audio", ""},
		{"traversal", files("../a.mp3"), 422, "bad_path", "../a.mp3"},
		{"traversal in the middle", files("a/../../b.mp3"), 422, "bad_path", "a/../../b.mp3"},
		{"absolute", files("/etc/x.mp3"), 422, "bad_path", "/etc/x.mp3"},
		{"backslash", files(`a\b.mp3`), 422, "bad_path", `a\b.mp3`},
		{"dot segment", files("a/./b.mp3"), 422, "bad_path", "a/./b.mp3"},
		{"empty segment", files("a//b.mp3"), 422, "bad_path", "a//b.mp3"},
		{"NUL", files("a\x00b.mp3"), 422, "bad_path", "a\x00b.mp3"},
		{"control character", files("a\nb.mp3"), 422, "bad_path", "a\nb.mp3"},
		{"not UTF-8", files("a\xffb.mp3"), 422, "bad_path", "a\xffb.mp3"},
		{"too long", files(strings.Repeat("a", 1030) + ".mp3"), 422, "bad_path", ""},
		{"too deep", files("1/2/3/4/5/6/7/8/9.mp3"), 422, "bad_path", ""},
		{"duplicate by case", files("a/B.mp3", "a/b.MP3"), 422, "bad_path", "a/b.MP3"},
		{"aax among mp3", files("a.mp3", "b.aax"), 422, "copy_protected", "b.aax"},
		{"aaxc", files("b.AAXC", "a.mp3"), 422, "copy_protected", "b.AAXC"},
		{"m4p", files("x/y.m4p"), 422, "copy_protected", "x/y.m4p"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := e.svc.Create(bg, me, e.lib.ID, c.m)
			ue := wantErr(t, err, c.status, c.code)
			if c.field != "" && ue.Field != c.field {
				t.Errorf("field = %q, want %q", ue.Field, c.field)
			}
		})
	}
	// Nothing was created by any refusal.
	if n := countRows(t, e, "uploads"); n != 0 {
		t.Errorf("%d uploads after refusals", n)
	}
	if entries, _ := os.ReadDir(e.svc.incomingDir()); len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

func countRows(t *testing.T, e *env, table string) int {
	t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCreateLimits(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxFiles = 6; c.MaxFile = 100; c.MaxBook = 150; c.MaxReceiving = 2 })
	sized := func(sizes ...int64) Manifest {
		m := Manifest{Label: "B"}
		for i, s := range sizes {
			m.Files = append(m.Files, FileIn{Path: "f" + itoa(i) + ".mp3", Size: s})
		}
		return m
	}
	_, err := e.svc.Create(bg, me, e.lib.ID, files("a.mp3", "b.mp3", "c.mp3", "d.mp3", "e.mp3", "f.mp3", "g.mp3"))
	wantErr(t, err, 413, "too_large")
	_, err = e.svc.Create(bg, me, e.lib.ID, sized(101))
	wantErr(t, err, 413, "too_large")
	_, err = e.svc.Create(bg, me, e.lib.ID, sized(100, 60))
	wantErr(t, err, 413, "too_large")
	_, err = e.svc.Create(bg, me, e.lib.ID, sized(0))
	wantErr(t, err, 422, "empty_file")
	// Hidden junk is left out and counted, whatever its size.
	m := files("a.mp3", ".DS_Store", "@eaDir/x.mp3", "readme.pdf", "cover.JPG", "info.opf")
	m.Files[1].Size = 0
	v, err := e.svc.Create(bg, me, e.lib.ID, m)
	if err != nil {
		t.Fatal(err)
	}
	if v.Skipped != 3 || len(v.Files) != 3 || v.ChunkSize != e.svc.cfg.ChunkSize {
		t.Errorf("view = %+v", v)
	}
	for _, f := range v.Files {
		if f.Offset != 0 {
			t.Errorf("fresh file at offset %d", f.Offset)
		}
	}
	if fi, err := os.Stat(e.svc.stagingDir(v.ID)); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Errorf("staging folder: %v %v", fi, err)
	}
	if _, err := e.svc.Create(bg, me, e.lib.ID, files("b.mp3")); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Create(bg, me, e.lib.ID, files("c.mp3"))
	wantErr(t, err, 429, "too_many")
	// Another person is not held back by mine.
	if _, err := e.svc.Create(bg, "us_other", e.lib.ID, files("c.mp3")); err != nil {
		t.Errorf("another person: %v", err)
	}
}

func TestCreateOffAndUnknown(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.repo.SetUploadFolder(bg, e.lib.ID, ""); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Create(bg, me, e.lib.ID, files("a.mp3"))
	wantErr(t, err, 409, "uploads_off")
	_, err = e.svc.Create(bg, me, "lib_nope", files("a.mp3"))
	wantErr(t, err, 404, "not_found")
}

func TestCreateNoSpace(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Margin = 1000 })
	e.svc.free = func(dir string) (uint64, error) {
		if strings.HasPrefix(dir, e.data) {
			return 500, nil
		}
		return 1 << 40, nil
	}
	ue := wantErr(t, mustErr(e.svc.Create(bg, me, e.lib.ID, files("a.mp3"))), 507, "no_space")
	if !strings.Contains(ue.Message, "data folder") {
		t.Errorf("message = %q", ue.Message)
	}
	e.svc.free = func(dir string) (uint64, error) {
		if dir == e.root {
			return 500, nil
		}
		return 1 << 40, nil
	}
	ue = wantErr(t, mustErr(e.svc.Create(bg, me, e.lib.ID, files("a.mp3"))), 507, "no_space")
	if !strings.Contains(ue.Message, "library folder") {
		t.Errorf("message = %q", ue.Message)
	}
	if n := countRows(t, e, "uploads"); n != 0 {
		t.Errorf("%d uploads left", n)
	}
	if entries, _ := os.ReadDir(e.svc.incomingDir()); len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

func mustErr(_ View, err error) error { return err }
