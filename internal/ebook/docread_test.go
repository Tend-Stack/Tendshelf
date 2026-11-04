package ebook

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readDocFixture(t *testing.T) (string, string, *Doc) {
	t.Helper()
	d := mustRead(t, "a.md", []byte(mdSample))
	js, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "text")
	sha, err := WriteDoc(dir, js)
	if err != nil {
		t.Fatal(err)
	}
	return dir, sha, d
}

func TestReadDocRoundTrip(t *testing.T) {
	dir, sha, d := readDocFixture(t)
	got, err := ReadDoc(dir, sha)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, d) {
		t.Error("document read back differs")
	}
	if _, err := ReadDoc(dir, hexOf('0')); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v", err)
	}
}

func hexOf(c byte) string { return string(bytes.Repeat([]byte{c}, 64)) }

func TestReadDocCorrupt(t *testing.T) {
	dir, sha, _ := readDocFixture(t)
	raw, err := os.ReadFile(DocPath(dir, sha))
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := gzip.NewReader(bytes.NewReader(raw))
	var plain bytes.Buffer
	_, _ = plain.ReadFrom(zr)
	edited := bytes.Replace(plain.Bytes(), []byte(`"v":1`), []byte(`"v":2`), 1)
	cases := []struct {
		name string
		body func() []byte
	}{
		{"edited content", func() []byte { return gz(t, edited) }},
		{"not json", func() []byte { return gz(t, []byte("nope")) }},
		{"not gzip", func() []byte { return []byte("plain text") }},
		{"truncated gzip", func() []byte { return raw[:len(raw)/2] }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.WriteFile(DocPath(dir, sha), c.body(), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadDoc(dir, sha); !errors.Is(err, ErrDocCorrupt) {
				t.Errorf("err = %v, want ErrDocCorrupt", err)
			}
		})
	}
}

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func TestReadDocTooLarge(t *testing.T) {
	dir, sha, _ := readDocFixture(t)
	old := maxDocBytes
	maxDocBytes = 64
	defer func() { maxDocBytes = old }()
	if _, err := ReadDoc(dir, sha); !errors.Is(err, ErrDocTooLarge) {
		t.Errorf("err = %v, want ErrDocTooLarge", err)
	}
}

func TestWordSpans(t *testing.T) {
	cases := []struct {
		name string
		b    Block
		want []WordSpan
	}{
		{"ascii", Block{Text: "Hi you. Go", Words: [][2]int{{0, 2}, {3, 6}, {8, 10}}, Sentences: [][2]int{{0, 7}, {8, 10}}},
			[]WordSpan{{"Hi", 0, false}, {"you", 0, true}, {"Go", 1, false}}},
		{"accent", Block{Text: "é a", Words: [][2]int{{0, 1}, {2, 3}}, Sentences: [][2]int{{0, 3}}},
			[]WordSpan{{"é", 0, false}, {"a", 0, false}}},
		{"emoji surrogate pair", Block{Text: "😀 ok", Words: [][2]int{{3, 5}}, Sentences: [][2]int{{0, 5}}},
			[]WordSpan{{"ok", 0, false}}},
		{"abbreviation", Block{Text: "Mr. Fox", Words: [][2]int{{0, 2}, {4, 7}}, Sentences: [][2]int{{0, 7}}},
			[]WordSpan{{"Mr", 0, true}, {"Fox", 0, false}}},
		{"outside sentences", Block{Text: "ab cd", Words: [][2]int{{0, 2}, {3, 5}}, Sentences: [][2]int{{0, 2}}},
			[]WordSpan{{"ab", 0, false}, {"cd", -1, false}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := WordSpans(c.b); !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
	if got := SentenceID("b7", 3); got != "b7.s3" {
		t.Errorf("SentenceID = %q", got)
	}
}
