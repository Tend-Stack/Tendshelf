package ebook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

func wantRefusal(t *testing.T, err error, reason, code string) {
	t.Helper()
	r, ok := err.(*Refusal)
	if !ok {
		t.Fatalf("err = %v (%T), want refusal %s/%s", err, err, reason, code)
	}
	if r.Reason != reason || (code != "" && r.Code != code) {
		t.Fatalf("refusal = %s/%s, want %s/%s", r.Reason, r.Code, reason, code)
	}
}

func epubWith(t *testing.T, mod func(*ebooktest.EPUBOptions)) []byte {
	o := ebooktest.EPUBOptions{Version: 3, Title: "T", Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"Body text."}}}}
	mod(&o)
	return ebooktest.EPUB(t, o)
}

func TestEbookRefusals(t *testing.T) {
	cases := []struct {
		name, file string
		data       []byte
		code       string
	}{
		{"adept rights.xml", "a.epub", epubWith(t, func(o *ebooktest.EPUBOptions) { o.Rights = true }), "adept"},
		{"apple sinf.xml", "a.epub", epubWith(t, func(o *ebooktest.EPUBOptions) { o.Apple = true }), "apple"},
		{"lcp license", "a.epub", epubWith(t, func(o *ebooktest.EPUBOptions) { o.LCP = true }), "lcp"},
		{"encryption.xml aes", "a.epub", epubWith(t, func(o *ebooktest.EPUBOptions) { o.EncryptAlg = "http://www.w3.org/2001/04/xmlenc#aes128-cbc" }), "epub_encryption"},
		{"ole docx", "a.docx", ebooktest.OLE(), "ole_office"},
		{"kfx by extension", "a.kfx", []byte("anything"), "kfx"},
		{"kfx-zip by extension", "a.kfx-zip", []byte("anything"), "kfx"},
		{"kfx by CONT magic", "a.txt", []byte("CONT\x02\x00 not text"), "kfx"},
		{"kfx by DRMION magic", "a.epub", append([]byte("\xeaDRMION\xee"), make([]byte, 64)...), "kfx"},
		{"encrypted zip entry", "a.epub", ebooktest.Zip(t, []ebooktest.Entry{
			{Name: "mimetype", Data: []byte("application/epub+zip")},
			{Name: "META-INF/container.xml", Data: []byte("<container/>"), Flags: 1}}), "zip_encrypted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := readBytes(t, c.file, c.data, DefaultLimits())
			wantRefusal(t, err, ReasonDRM, c.code)
		})
	}
	t.Run("ole without docx ext is unsupported", func(t *testing.T) {
		_, err := readBytes(t, "a.doc", ebooktest.OLE(), DefaultLimits())
		wantRefusal(t, err, ReasonUnsupported, "")
	})
	t.Run("font obfuscation only is accepted", func(t *testing.T) {
		d := mustRead(t, "a.epub", epubWith(t, func(o *ebooktest.EPUBOptions) { o.FontObfuscation = true }))
		if len(d.Chapters) != 1 {
			t.Errorf("chapters = %d", len(d.Chapters))
		}
	})
}

func TestEbookHostile(t *testing.T) {
	lim := DefaultLimits()
	t.Run("zip bomb", func(t *testing.T) {
		_, err := readBytes(t, "a.epub", ebooktest.ZipBomb(t), lim)
		wantRefusal(t, err, ReasonDamaged, "zip_bomb")
	})
	t.Run("too many entries", func(t *testing.T) {
		_, err := readBytes(t, "a.epub", ebooktest.ManyEntries(t, lim.MaxEntries+1), lim)
		wantRefusal(t, err, ReasonDamaged, "zip_entries")
	})
	t.Run("traversal names are not read", func(t *testing.T) {
		d := mustRead(t, "a.epub", ebooktest.TraversalEPUB(t))
		js, _ := Encode(d)
		if bytes.Contains(js, []byte(ebooktest.Sentinel)) {
			t.Fatal("a document outside the zip root reached the output")
		}
		if len(d.Chapters) != 1 || d.Chapters[0].Title != "Inside" {
			t.Errorf("chapters = %q", chapterTitles(d))
		}
	})
	t.Run("billion laughs", func(t *testing.T) {
		start := time.Now()
		d, err := readBytes(t, "a.epub", ebooktest.EntityEPUB(t), lim)
		if time.Since(start) > 2*time.Second {
			t.Fatalf("took %v", time.Since(start))
		}
		if err == nil {
			js, _ := Encode(d)
			if len(js) > 4096 {
				t.Errorf("document grew to %d bytes", len(js))
			}
		}
	})
	t.Run("lying size", func(t *testing.T) {
		_, err := readBytes(t, "a.epub", ebooktest.LyingSizeEPUB(t), lim)
		wantRefusal(t, err, ReasonDamaged, "")
	})
	t.Run("deep xml", func(t *testing.T) {
		_, err := readBytes(t, "a.epub", ebooktest.DeepEPUB(t, 300), lim)
		wantRefusal(t, err, ReasonDamaged, "xml_depth")
	})
	t.Run("oversize file", func(t *testing.T) {
		small := lim
		small.MaxFileBytes = 100
		_, err := readBytes(t, "a.txt", bytes.Repeat([]byte("word "), 100), small)
		wantRefusal(t, err, ReasonTooLarge, "file_too_big")
	})
	t.Run("too many words", func(t *testing.T) {
		small := lim
		small.MaxWords = 3
		_, err := readBytes(t, "a.txt", []byte("one two three four five"), small)
		wantRefusal(t, err, ReasonTooLarge, "too_many_words")
	})
	t.Run("too many blocks", func(t *testing.T) {
		small := lim
		small.MaxBlocks = 2
		_, err := readBytes(t, "a.txt", []byte("a\n\nb\n\nc\n\nd"), small)
		wantRefusal(t, err, ReasonDamaged, "too_many_blocks")
	})
	t.Run("entry too big", func(t *testing.T) {
		small := lim
		small.MaxEntryBytes = 50
		_, err := readBytes(t, "a.epub", epubWith(t, func(o *ebooktest.EPUBOptions) { o.Chapters[0].Paras = []string{strings.Repeat("long ", 40)} }), small)
		wantRefusal(t, err, ReasonDamaged, "entry_too_big")
	})
	t.Run("parser panic", func(t *testing.T) {
		parseHook = func(string) { panic("boom") }
		defer func() { parseHook = nil }()
		_, err := readBytes(t, "a.txt", []byte("text"), lim)
		wantRefusal(t, err, ReasonDamaged, "parser_panic")
	})
	t.Run("deadline", func(t *testing.T) {
		done := make(chan struct{})
		parseHook = func(string) { time.Sleep(100 * time.Millisecond); close(done) }
		// The abandoned read goroutine still runs the hook; wait for it before resetting the hook.
		defer func() { <-done; parseHook = nil }()
		short := lim
		short.ParseTimeout = time.Millisecond
		_, err := readBytes(t, "a.txt", []byte("text"), short)
		wantRefusal(t, err, ReasonDamaged, "timeout")
	})
	t.Run("library file is not modified", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "a.epub")
		data := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "T", Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"Body."}}}})
		if err := os.WriteFile(p, data, 0o400); err != nil {
			t.Fatal(err)
		}
		before, _ := os.Stat(p)
		if _, err := Read(context.Background(), p, "epub", lim); err != nil {
			t.Fatal(err)
		}
		after, _ := os.Stat(p)
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, data) || !before.ModTime().Equal(after.ModTime()) {
			t.Error("file changed")
		}
	})
}
