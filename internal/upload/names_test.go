package upload

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafeName(t *testing.T) {
	long := strings.Repeat("é", 150) // 300 bytes
	for _, c := range []struct{ in, want string }{
		{"../x", "_x"},
		{"..", "Fallback"},
		{"a/b", "a_b"},
		{`a\b`, "a_b"},
		{"CON", "CON_"},
		{"con.txt", "con_.txt"},
		{"Lpt1", "Lpt1_"},
		{"COM9.mp3", "COM9_.mp3"},
		{"COM10", "COM10"},
		{".hidden", "hidden"},
		{"CD1", "Book CD1"},
		{"Disc 2", "Book Disc 2"},
		{"@eaDir", "Book @eaDir"},
		{"x:y", "x-y"},
		{"Title: Sub", "Title - Sub"},
		{"a\x00b\x07c", "abc"},
		{"  spaced   out  ", "spaced out"},
		{"ends. ", "ends"},
		{`q*?"<>|`, "q______"},
		{"", "Fallback"},
		{"   ", "Fallback"},
	} {
		if got := SafeName(c.in, "Fallback"); got != c.want {
			t.Errorf("SafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := SafeName(long, "F")
	if len(got) > maxNameBytes || !utf8.ValidString(got) || got == "" {
		t.Errorf("long name: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	ext := SafeName(strings.Repeat("a", 300)+".mp3", "F")
	if len(ext) > maxNameBytes || !strings.HasSuffix(ext, ".mp3") {
		t.Errorf("long name lost its extension: %q (%d)", ext, len(ext))
	}
	if got := safeInner("CD1", "F"); got != "CD1" {
		t.Errorf("inside a book CD1 must stay CD1, got %q", got)
	}
}

func TestSeqPrefix(t *testing.T) {
	for in, want := range map[string]string{"1": "01", "12": "12", "1.5": "01.5", "007": "07", "0": "00", "": "", "x": "", "1a": ""} {
		if got := seqPrefix(in); got != want {
			t.Errorf("seqPrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

func mk(t *testing.T, root string, rel ...string) {
	t.Helper()
	for _, r := range rel {
		p := filepath.Join(root, filepath.FromSlash(r))
		if strings.HasSuffix(r, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLayout(t *testing.T) {
	rel := func(root, parent, name string) string {
		r, _ := filepath.Rel(root, filepath.Join(parent, name))
		return filepath.ToSlash(r)
	}
	t.Run("author and title", func(t *testing.T) {
		root := t.TempDir()
		p, n, ok := layout(root, "Ann Author", "", "", "The Book", nil)
		if !ok || rel(root, p, n) != "Ann Author/The Book" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("series", func(t *testing.T) {
		root := t.TempDir()
		p, n, ok := layout(root, "Ann", "Saga", "1.5", "Part", nil)
		if !ok || rel(root, p, n) != "Ann/Saga/01.5 - Part" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("unknown names", func(t *testing.T) {
		root := t.TempDir()
		p, n, _ := layout(root, "", "", "", "", nil)
		if rel(root, p, n) != "Unknown author/Untitled book" {
			t.Errorf("got %q", rel(root, p, n))
		}
	})
	t.Run("ancestor holds audio: fall back to the top", func(t *testing.T) {
		root := t.TempDir()
		mk(t, root, "Ann/loose.m4b")
		p, n, ok := layout(root, "Ann", "", "", "T", nil)
		if !ok || rel(root, p, n) != "Ann - T" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("ancestor is a link", func(t *testing.T) {
		root := t.TempDir()
		other := t.TempDir()
		if err := os.Symlink(other, filepath.Join(root, "Ann")); err != nil {
			t.Fatal(err)
		}
		p, n, ok := layout(root, "Ann", "", "", "T", nil)
		if !ok || rel(root, p, n) != "Ann - T" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("ancestor is a file", func(t *testing.T) {
		root := t.TempDir()
		mk(t, root, "Ann")
		p, n, ok := layout(root, "Ann", "", "", "T", nil)
		if !ok || rel(root, p, n) != "Ann - T" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("ancestor is inside a book", func(t *testing.T) {
		root := t.TempDir()
		mk(t, root, "Ann/")
		p, n, ok := layout(root, "Ann", "", "", "T", []string{filepath.Join(root, "Ann")})
		if !ok || rel(root, p, n) != "Ann - T" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("series ancestor unusable falls back to Author/Title", func(t *testing.T) {
		root := t.TempDir()
		mk(t, root, "Ann/Saga/x.mp3")
		p, n, ok := layout(root, "Ann", "Saga", "2", "T", nil)
		if !ok || rel(root, p, n) != "Ann/T" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("a file at the name", func(t *testing.T) {
		root := t.TempDir()
		mk(t, root, "Ann/T")
		p, n, ok := layout(root, "Ann", "", "", "T", nil)
		if !ok || rel(root, p, n) != "Ann/T (2)" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
	})
	t.Run("suffixes up to 99, then another shape, then failure", func(t *testing.T) {
		root := t.TempDir()
		mk(t, root, "Ann/T/")
		for i := 2; i <= 99; i++ {
			mk(t, root, "Ann/T ("+itoa(i)+")/")
		}
		p, n, ok := layout(root, "Ann", "", "", "T", nil)
		if !ok || rel(root, p, n) != "Ann - T" {
			t.Errorf("got %q %q %v", p, n, ok)
		}
		mk(t, root, "Ann - T/")
		for i := 2; i <= 99; i++ {
			mk(t, root, "Ann - T ("+itoa(i)+")/")
		}
		if _, _, ok := layout(root, "Ann", "", "", "T", nil); ok {
			t.Error("layout found a place when every name was taken")
		}
	})
}

func itoa(i int) string { return strconv.Itoa(i) }
