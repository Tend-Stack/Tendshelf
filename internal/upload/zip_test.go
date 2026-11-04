package upload

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// zipEntry is one entry of an archive built in a test.
type zipEntry struct {
	name  string
	data  []byte
	mode  os.FileMode // 0 = a plain file
	flags uint16
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Flags: e.flags}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// lyingZip declares a small size for an entry whose deflate stream holds much more.
func lyingZip(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	fw, _ := flate.NewWriter(&raw, flate.BestCompression)
	big := bytes.Repeat([]byte("ab"), 1<<20)
	_, _ = fw.Write(big)
	_ = fw.Close()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "B/01.txt", Method: zip.Deflate,
		CRC32: crc32.ChecksumIEEE(big), CompressedSize64: uint64(raw.Len()), UncompressedSize64: 100})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(raw.Bytes())
	_ = zw.Close()
	return buf.Bytes()
}

func goodTrack(t *testing.T) []byte {
	return mp3(t, map[string]string{"album": "Zip Title", "artist": "Zed Author"})
}

// randomish returns n bytes that deflate badly (a simple xorshift stream).
func randomish(n int) []byte {
	b := make([]byte, n)
	x := uint64(88172645463325252)
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte(x)
	}
	return b
}

// sendZip uploads one archive as a book and queues its placement.
func (e *env) sendZip(label string, z []byte) View {
	e.t.Helper()
	return e.send(label, book{label + ".zip": z})
}

func TestZipRejections(t *testing.T) {
	track := goodTrack(t)
	many := []zipEntry{{name: "B/01.mp3", data: track}}
	for i := 0; i < 2000; i++ {
		many = append(many, zipEntry{name: fmt.Sprintf("B/n%04d.txt", i), data: []byte("x")})
	}
	enc := fakeAudio(t, mediatest.FakeFile{Container: "mp4", Encrypted: true})
	tests := []struct {
		name    string
		zip     []byte
		codes   []string // accepted error codes
		maxBook int64
		refused string // the file named when the whole book is refused
	}{
		{name: "dotdot", zip: buildZip(t, []zipEntry{{name: "../evil.mp3", data: track}}), codes: []string{"bad_path"}},
		{name: "absolute", zip: buildZip(t, []zipEntry{{name: "/abs.mp3", data: track}}), codes: []string{"bad_path"}},
		{name: "drive", zip: buildZip(t, []zipEntry{{name: `C:\x.mp3`, data: track}}), codes: []string{"bad_path"}},
		{name: "backslash dotdot", zip: buildZip(t, []zipEntry{{name: `a\..\..\b.mp3`, data: track}}), codes: []string{"bad_path"}},
		{name: "nul", zip: buildZip(t, []zipEntry{{name: "B/a\x00.mp3", data: track}}), codes: []string{"bad_path"}},
		{name: "case collision", zip: buildZip(t, []zipEntry{{name: "B/01.mp3", data: track}, {name: "B/01.MP3", data: track}}), codes: []string{"bad_path"}},
		{name: "encrypted", zip: buildZip(t, []zipEntry{{name: "B/01.mp3", data: track, flags: 1}}), codes: []string{"zip_encrypted"}},
		{name: "too many entries", zip: buildZip(t, append(many, zipEntry{name: "B/last.txt", data: []byte("x")})), codes: []string{"zip_too_large"}},
		{name: "ratio bomb", zip: buildZip(t, []zipEntry{{name: "B/01.mp3", data: track}, {name: "B/pad.txt", data: bytes.Repeat([]byte{0}, 3<<20)}}), codes: []string{"zip_bomb"}},
		{name: "total size", zip: buildZip(t, []zipEntry{{name: "B/01.mp3", data: track}, {name: "B/a.txt", data: bytes.Repeat(randomish(2<<10), 150)}, {name: "B/b.txt", data: bytes.Repeat(randomish(2<<10), 150)}}), maxBook: 400 << 10, codes: []string{"zip_too_large"}},
		{name: "header lies low", zip: lyingZip(t), codes: []string{"zip_too_large", "zip_bad"}},
		{name: "not a zip", zip: []byte("PK nothing here"), codes: []string{"zip_bad"}},
		{name: "no audio", zip: buildZip(t, []zipEntry{{name: "B/a.txt", data: []byte("hello")}}), codes: []string{"no_audio"}},
		{name: "aax inside", zip: buildZip(t, []zipEntry{{name: "B/01.mp3", data: track}, {name: "B/02.aax", data: []byte("sealed")}}), refused: "B/02.aax"},
		{name: "encrypted stream inside", zip: buildZip(t, []zipEntry{{name: "B/01.mp3", data: track}, {name: "B/02.m4a", data: enc}}), refused: "B/02.m4a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, func(c *Config) {
				if tc.maxBook > 0 {
					c.MaxBook = tc.maxBook
				}
			})
			if len(tc.zip) > int(e.svc.cfg.MaxFile) {
				t.Fatalf("test archive is %d bytes", len(tc.zip))
			}
			v := e.sendZip("Zed Author - Zip Title", tc.zip)
			res, perr := e.place(v.ID)
			u := e.upload(v.ID)
			if tc.refused != "" {
				if perr != nil || res.(PlaceResult).State != StateRefused || u.RefusedFile != tc.refused {
					t.Fatalf("want refused %s, got %v %+v", tc.refused, perr, u)
				}
			} else {
				if perr == nil || u.State != StateFailed {
					t.Fatalf("want failure, got %v state %s", perr, u.State)
				}
				if !slices.Contains(tc.codes, u.ErrorCode) {
					t.Errorf("code = %q, want %v (%v)", u.ErrorCode, tc.codes, perr)
				}
				left, _ := filepath.Glob(filepath.Join(e.svc.stagingDir(v.ID), "x*.part"))
				if len(left) != 0 {
					t.Errorf("extracted files left in staging: %v", left)
				}
			}
			if got := tree(t, e.root); len(got) != 0 {
				t.Errorf("the library folder changed: %v", got)
			}
		})
	}
}

func TestZipPlacesOneBookAndLeavesTheRestOut(t *testing.T) {
	track := goodTrack(t)
	inner := buildZip(t, []zipEntry{{name: "deep.mp3", data: track}})
	z := buildZip(t, []zipEntry{
		{name: "My Book/", mode: os.ModeDir | 0o755},
		{name: "My Book/01.mp3", data: track},
		{name: "My Book/02.mp3", data: track},
		{name: "My Book/cover.jpg", data: []byte("jpeg")},
		{name: "My Book/inner.zip", data: inner},
		{name: "My Book/link.mp3", data: []byte("01.mp3"), mode: os.ModeSymlink | 0o777},
		{name: "__MACOSX/My Book/._01.mp3", data: []byte("junk")},
	})
	e := newEnv(t, nil)
	v := e.sendZip("Zed Author - Zip Title", z)
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Fatalf("state = %s", res.State)
	}
	got := tree(t, e.root)
	joined := strings.Join(got, "\n")
	for _, bad := range []string{"inner.zip", "deep.mp3", "link.mp3", "MACOSX"} {
		if strings.Contains(joined, bad) {
			t.Errorf("%s was placed: %v", bad, got)
		}
	}
	audio := 0
	for _, p := range got {
		if strings.HasSuffix(p, ".mp3") {
			audio++
		}
	}
	if audio != 2 || !strings.Contains(joined, "cover.jpg") {
		t.Errorf("tree = %v", got)
	}
	if u := e.upload(v.ID); u.Skipped != 3 {
		t.Errorf("skipped = %d, want 3", u.Skipped)
	}
}

// Several archives are several uploads, placed one at a time: a bad one does not stop the next.
func TestZipOneBadBookDoesNotStopTheOther(t *testing.T) {
	track := goodTrack(t)
	e := newEnv(t, nil)
	bad := e.sendZip("Bad", buildZip(t, []zipEntry{{name: "../x.mp3", data: track}}))
	good := e.sendZip("Zed Author - Zip Title", buildZip(t, []zipEntry{{name: "G/01.mp3", data: track}}))
	if _, err := e.place(bad.ID); err == nil {
		t.Fatal("the bad book was placed")
	}
	if res := e.mustPlace(good.ID); res.State != StatePlaced {
		t.Fatalf("good book state = %s", res.State)
	}
	if len(tree(t, e.root)) == 0 {
		t.Error("the good book is missing")
	}
	if e.upload(bad.ID).State != StateFailed {
		t.Error("the bad book is not failed")
	}
}

func TestZipRetryRebuildsExtraction(t *testing.T) {
	e := newEnv(t, nil)
	v := e.sendZip("Zed Author - Zip Title", buildZip(t, []zipEntry{{name: "G/01.mp3", data: goodTrack(t)}}))
	stale := filepath.Join(e.svc.stagingDir(v.ID), "x0000.part")
	if err := os.WriteFile(stale, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Fatalf("state = %s", res.State)
	}
}

func TestZipQuotaCountsUnpackedBytes(t *testing.T) {
	z := buildZip(t, []zipEntry{{name: "G/01.mp3", data: goodTrack(t)}, {name: "G/a.txt", data: randomish(300 << 10)}})
	e := newEnv(t, func(c *Config) { c.PerPerson = int64(len(z)) + 100<<10 })
	v := e.sendZip("Z", z)
	if _, err := e.place(v.ID); err == nil || e.upload(v.ID).ErrorCode != "zip_too_large" {
		t.Fatalf("err = %v, code %q", err, e.upload(v.ID).ErrorCode)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("library changed: %v", got)
	}
}
