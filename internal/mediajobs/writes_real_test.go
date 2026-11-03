package mediajobs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// TestFileWritesRealRoundTrip runs the whole write on files made by real ffmpeg (opt-in with
// TENDSHELF_TEST_FFMPEG, `make test-mediajobs-real`): for m4b, mp3 and flac the audio hash survives
// the write, Undo gives back the original bytes, a second write works, and Looks good removes the
// backup.
func TestFileWritesRealRoundTrip(t *testing.T) {
	tools := mediatest.RequireFFmpeg(t)
	cases := []struct {
		name, file string
		gen        func(path string)
	}{
		{"m4b", "book.m4b", func(p string) {
			mediatest.GenM4B(t, tools, p, mediatest.Synth{Title: "Old", Author: "Old Author", DurationMs: 3000, Cover: true,
				Chapters: []mediatest.FakeChapter{mediatest.Chapter(0, 1500, "One"), mediatest.Chapter(1500, 3000, "Two")}})
		}},
		{"mp3", "book.mp3", func(p string) {
			realSynth(t, tools.FFmpeg, p, "mp3", "-c:a", "libmp3lame", "-b:a", "32k", "-id3v2_version", "3")
		}},
		{"flac", "book.flac", func(p string) { realSynth(t, tools.FFmpeg, p, "flac", "-c:a", "flac") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newWEnv(t, tools, "")
			rel := "Book " + c.name + "/" + c.file
			p := e.path(rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			c.gen(p)
			oldFile(t, p)
			e.scanAll()
			book := e.book("Book " + c.name)
			dir := filepath.Dir(p)
			before := snapshot(t, dir)
			hash, err := tools.AudioHash(bg, p)
			if err != nil {
				t.Fatal(err)
			}
			_, backup := hiddenNames(p)

			j, err := e.start(book)
			if err != nil || j.State != jobs.StateDone {
				t.Fatalf("write job = %+v, %v", j, err)
			}
			if h, err := tools.AudioHash(bg, p); err != nil || h != hash {
				t.Errorf("audio hash %q (%v), want %q", h, err, hash)
			}
			if h, err := tools.AudioHash(bg, backup); err != nil || h != hash {
				t.Errorf("backup audio hash %q (%v)", h, err)
			}
			pr, err := tools.Probe(bg, p)
			if err != nil {
				t.Fatal(err)
			}
			if pr.Tags["album"] != book.Title && pr.Tags["title"] != book.Title {
				t.Errorf("tags = %v, want the book title %q", pr.Tags, book.Title)
			}

			if n, err := e.w.Undo(bg, book.ID, ""); err != nil || n != 1 {
				t.Fatalf("Undo = %d, %v", n, err)
			}
			after := snapshot(t, dir)
			if !sameStrings(names(after), names(before)) || after[c.file] != before[c.file] {
				t.Errorf("after Undo: %v, original identical = %v", names(after), after[c.file] == before[c.file])
			}

			if j, err := e.start(book); err != nil || j.State != jobs.StateDone {
				t.Fatalf("second write job = %+v, %v", j, err)
			}
			if n, err := e.w.Confirm(bg, book.ID, ""); err != nil || n != 1 {
				t.Fatalf("Confirm = %d, %v", n, err)
			}
			if got := names(snapshot(t, dir)); !sameStrings(got, []string{c.file}) {
				t.Errorf("after Looks good: %v", got)
			}
			if h, err := tools.AudioHash(bg, p); err != nil || h != hash {
				t.Errorf("audio hash after Looks good %q (%v)", h, err)
			}
		})
	}
}

// realSynth makes a 3 second sine file with ffmpeg (stream settings in codec).
func realSynth(t *testing.T, ffmpeg, path, format string, codec ...string) {
	t.Helper()
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration=3", "-ac", "1"}
	args = append(args, codec...)
	args = append(args, "-metadata", "title=Old", "-metadata", "artist=Old Author", "-f", format, path)
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, out)
	}
}
