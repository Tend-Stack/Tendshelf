package media_test

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestWriteTagsReal runs the write commands on real ffmpeg (`make test-media`): the audio hash
// survives, the duration stays, every tag reads back, and chapters are written for m4b and mp3.
func TestWriteTagsReal(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	ctx := context.Background()
	dir, meta := t.TempDir(), t.TempDir()

	chapters := []media.WriteChapter{{0, 1000, "One, a=b"}, {1000, 2000, "Two; #3"}, {2000, 3000, "Three"}}
	info := media.BookInfo{
		Title: "Brontë's Book", Subtitle: "A Sub", Publisher: "Pub House", Description: "Line one; with = signs",
		Language: "eng", ASIN: "B000TEST01", ISBN: "9780000000002", Series: "The Series", Sequence: "2", Year: 2021,
		Authors: []string{"Ann Author", "Bo Writer"}, Narrators: []string{"Nia Voice"}, Genres: []string{"Fantasy", "Epic"},
	}

	cases := []struct {
		name, file string
		gen        func(path string)
		format     string
		chapters   bool
	}{
		{"m4b", "book.m4b", func(p string) {
			mediatest.GenM4B(t, tools, p, mediatest.Synth{Title: "Old", Author: "Old Author", DurationMs: 3000,
				Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 3000, "Only")}})
		}, "mp4", true},
		{"mp3", "book.mp3", func(p string) { synth(t, tools, p, "mp3", "-c:a", "libmp3lame", "-b:a", "32k", "-id3v2_version", "3") }, "mp3", true},
		{"flac", "book.flac", func(p string) { synth(t, tools, p, "flac", "-c:a", "flac") }, "flac", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := filepath.Join(dir, c.file)
			dst := filepath.Join(dir, "."+c.file+".tendshelf-new")
			c.gen(src)
			sp, err := tools.Probe(ctx, src)
			if err != nil {
				t.Fatal(err)
			}
			if f, ok := media.WriteFormat(sp.Container, src); !ok || f != c.format {
				t.Fatalf("WriteFormat(%q) = %q, %v", sp.Container, f, ok)
			}
			w := media.TagWrite{Format: c.format, Tags: media.BookTags(info, false), Chapters: chapters, WriteChapters: c.chapters}
			if err := tools.WriteTags(ctx, src, dst, meta, w); err != nil {
				t.Fatalf("WriteTags: %v", err)
			}
			h1, err1 := tools.AudioHash(ctx, src)
			h2, err2 := tools.AudioHash(ctx, dst)
			if err1 != nil || err2 != nil || h1 == "" || h1 != h2 {
				t.Errorf("audio hash: %q (%v) vs %q (%v)", h1, err1, h2, err2)
			}
			dp, err := tools.Probe(ctx, dst)
			if err != nil {
				t.Fatal(err)
			}
			if d := dp.DurationMs - sp.DurationMs; d > 50 || d < -50 {
				t.Errorf("duration %d -> %d", sp.DurationMs, dp.DurationMs)
			}
			for k, want := range media.BookTags(info, false) {
				if c.format == "flac" && k == "description" {
					k = "comment" // see writeTagsArgs
				}
				if got := dp.Tags[k]; got != want {
					t.Errorf("tag %s = %q, want %q", k, got, want)
				}
			}
			switch {
			case c.chapters:
				if len(dp.Chapters) != len(chapters) {
					t.Fatalf("chapters = %+v, want %d", dp.Chapters, len(chapters))
				}
				for i, ch := range dp.Chapters {
					if math.Abs(float64(ch.StartMs-chapters[i].StartMs)) > 1 || ch.Title != chapters[i].Title {
						t.Errorf("chapter %d = %+v, want %+v", i, ch, chapters[i])
					}
				}
			default:
				if len(dp.Chapters) != len(sp.Chapters) {
					t.Errorf("chapter count %d -> %d", len(sp.Chapters), len(dp.Chapters))
				}
			}
			if len(dp.Pictures) != len(sp.Pictures) || len(dp.Audio) != 1 {
				t.Errorf("streams changed: pictures %d -> %d, audio %d", len(sp.Pictures), len(dp.Pictures), len(dp.Audio))
			}
		})
	}
}

// synth makes a 3 second sine file with ffmpeg (stream settings in codec).
func synth(t *testing.T, tools media.Tools, path, format string, codec ...string) {
	t.Helper()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration=3", "-ac", "1"}
	args = append(args, codec...)
	args = append(args, "-metadata", "title=Old", "-metadata", "artist=Old Author", "-f", format, path)
	if out, err := exec.Command(tools.FFmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}
