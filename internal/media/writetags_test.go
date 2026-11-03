package media_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func TestWriteTagsFake(t *testing.T) {
	tools := mediatest.FakeTools(t)
	ctx := context.Background()
	dir, meta := t.TempDir(), t.TempDir()
	src, dst := filepath.Join(dir, "a.m4b"), filepath.Join(dir, ".a.m4b.tendshelf-new")
	mediatest.WriteFake(t, src, mediatest.FakeFile{
		Tags: map[string]string{"title": "Old", "comment": "kept"}, PadBytes: 5000,
		Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 1000, "Old chapter")},
	})
	before, _ := os.ReadFile(src)

	w := media.TagWrite{
		Format: "mp4", Tags: map[string]string{"title": "New; title=1", "album": "Book"},
		WriteChapters: true,
		Chapters:      []media.WriteChapter{{0, 2000, "A=1"}, {2000, 6000, "B\nline"}},
	}
	if err := tools.WriteTags(ctx, src, dst, meta, w); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(src); string(after) != string(before) {
		t.Error("the source file changed")
	}
	if left, _ := os.ReadDir(meta); len(left) != 0 {
		t.Errorf("metadata file left in metaDir: %v", left)
	}
	p, err := tools.Probe(ctx, dst)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tags["title"] != "New; title=1" || p.Tags["album"] != "Book" || p.Tags["comment"] != "kept" {
		t.Errorf("tags = %v", p.Tags)
	}
	if len(p.Chapters) != 2 || p.Chapters[0].Title != "A=1" || p.Chapters[1].Title != "B\nline" || p.Chapters[1].StartMs != 2000 {
		t.Errorf("chapters = %+v", p.Chapters)
	}
	h1, err1 := tools.AudioHash(ctx, src)
	h2, err2 := tools.AudioHash(ctx, dst)
	if err1 != nil || err2 != nil || len(h1) != 64 || h1 != h2 {
		t.Errorf("audio hash %q (%v) vs %q (%v)", h1, err1, h2, err2)
	}

	// Without WriteChapters the file's own chapters stay.
	dst2 := filepath.Join(dir, "b.m4b")
	if err := tools.WriteTags(ctx, src, dst2, meta, media.TagWrite{Format: "mp4", Tags: map[string]string{"album": "X"}}); err != nil {
		t.Fatal(err)
	}
	if p, _ := tools.Probe(ctx, dst2); len(p.Chapters) != 1 || p.Chapters[0].Title != "Old chapter" {
		t.Errorf("chapters were not kept: %+v", p.Chapters)
	}
}

func TestWriteTagsRefusals(t *testing.T) {
	tools := mediatest.FakeTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "a.m4b")
	mediatest.WriteFake(t, src, mediatest.FakeFile{})
	ctx := context.Background()
	if err := tools.WriteTags(ctx, src, src, dir, media.TagWrite{Format: "mp4"}); err == nil {
		t.Error("writing over the source was allowed")
	}
	if err := tools.WriteTags(ctx, src, filepath.Join(dir, "x"), dir, media.TagWrite{Format: "ogg"}); err == nil {
		t.Error("an unsupported format was allowed")
	}
	if err := tools.WriteTags(ctx, filepath.Join(dir, "a.aax"), filepath.Join(dir, "x"), dir, media.TagWrite{Format: "mp4"}); err == nil {
		t.Error("a protected file was allowed")
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Error("an output file appeared after a refusal")
	}
}

func TestWriteTagsFreeBytesAndFileHash(t *testing.T) {
	dir := t.TempDir()
	if n, err := media.FreeBytes(dir); err != nil || n == 0 {
		t.Errorf("FreeBytes = %d, %v", n, err)
	}
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := media.FileSHA256(p)
	if want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"; err != nil || got != want {
		t.Errorf("FileSHA256 = %q, %v", got, err)
	}
	if _, err := media.FileSHA256(filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing file hashed")
	}
}
