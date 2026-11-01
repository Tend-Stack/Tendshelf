package mediatest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

// RequireFFmpeg skips the test unless TENDSHELF_TEST_FFMPEG is set, then locates ffprobe and
// ffmpeg on PATH. If the variable is set but a tool is missing, the test fails.
func RequireFFmpeg(t testing.TB) media.Tools {
	t.Helper()
	if os.Getenv("TENDSHELF_TEST_FFMPEG") == "" {
		t.Skip("set TENDSHELF_TEST_FFMPEG=1 (make test-media) to run tests that need ffmpeg and ffprobe")
	}
	tools, err := media.Locate(media.Config{})
	if err != nil {
		t.Fatalf("TENDSHELF_TEST_FFMPEG is set but %v", err)
	}
	return tools
}

// Synth describes a file made with ffmpeg: a sine tone with tags, chapters and an optional cover.
type Synth struct {
	Title, Author, Narrator, Album, Genre, Year, Comment string
	Chapters                                             []FakeChapter
	DurationMs                                           int64 // 0 means 6000
	Cover                                                bool
}

// common is the start of every ffmpeg command: no stdin, quiet, file and pipe protocols only,
// overwrite the output (which always lives in a test's temporary directory).
var common = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe", "-y"}

func ffmpeg(t testing.TB, tools media.Tools, args ...string) {
	t.Helper()
	if tools.FFmpeg == "" {
		t.Fatal("mediatest: no ffmpeg")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tools.FFmpeg, append(append([]string{}, common...), args...)...)
	cmd.Env = []string{"LC_ALL=C"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func sec(ms int64) string { return fmt.Sprintf("%d.%03d", ms/1000, ms%1000) }

// escapeMeta escapes the characters that are special in an ffmetadata file.
func escapeMeta(s string) string {
	return strings.NewReplacer(`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", "\\\n").Replace(s)
}

func writeMeta(t testing.TB, path string, s Synth) {
	t.Helper()
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, kv := range [][2]string{{"title", s.Title}, {"artist", s.Author}, {"album_artist", s.Author},
		{"composer", s.Narrator}, {"album", s.Album}, {"genre", s.Genre}, {"date", s.Year}, {"comment", s.Comment}} {
		if kv[1] != "" {
			fmt.Fprintf(&b, "%s=%s\n", kv[0], escapeMeta(kv[1]))
		}
	}
	for _, c := range s.Chapters {
		fmt.Fprintf(&b, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", c.StartMs, c.EndMs, escapeMeta(c.Title))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// GenM4B writes an AAC audiobook in an MP4 container (extension .m4b is up to the caller) with the
// tags, chapters and cover of s.
func GenM4B(t testing.TB, tools media.Tools, path string, s Synth) {
	t.Helper()
	tmp := t.TempDir()
	dur := s.DurationMs
	if dur == 0 {
		dur = 6000
	}
	meta := filepath.Join(tmp, "meta.txt")
	writeMeta(t, meta, s)
	args := []string{"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration=" + sec(dur),
		"-f", "ffmetadata", "-i", meta}
	if s.Cover {
		cover := filepath.Join(tmp, "cover.jpg")
		ffmpeg(t, tools, "-f", "lavfi", "-i", "color=c=0x8a4b2f:s=64x64:d=1", "-frames:v", "1", "-f", "image2", "-c:v", "mjpeg", cover)
		args = append(args, "-i", cover)
	}
	args = append(args, "-map", "0:a")
	if s.Cover {
		args = append(args, "-map", "2:v")
	}
	args = append(args, "-map_metadata", "1", "-map_chapters", "1", "-c:a", "aac", "-b:a", "32k", "-ac", "1")
	if s.Cover {
		args = append(args, "-c:v", "copy", "-disposition:v:0", "attached_pic")
	}
	args = append(args, "-movflags", "+faststart", "-f", "mp4", path)
	ffmpeg(t, tools, args...)
}

// GenMP3Parts writes n two-second MP3 parts named "NN - Part.mp3" in dir, tagged track i/n, and
// returns their paths.
func GenMP3Parts(t testing.TB, tools media.Tools, dir string, n int, s Synth) []string {
	t.Helper()
	var paths []string
	for i := 1; i <= n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%02d - Part.mp3", i))
		ffmpeg(t, tools, "-f", "lavfi", "-i", "sine=frequency=330:sample_rate=22050:duration=2",
			"-c:a", "libmp3lame", "-b:a", "32k", "-ac", "1", "-id3v2_version", "3",
			"-metadata", "title="+s.Title, "-metadata", "artist="+s.Author, "-metadata", "album="+s.Album,
			"-metadata", fmt.Sprintf("track=%d/%d", i, n), "-f", "mp3", p)
		paths = append(paths, p)
	}
	return paths
}

// GenFLAC writes a two-second FLAC file with TITLE and ARTIST tags.
func GenFLAC(t testing.TB, tools media.Tools, path string, s Synth) {
	t.Helper()
	ffmpeg(t, tools, "-f", "lavfi", "-i", "sine=frequency=550:sample_rate=22050:duration=2",
		"-c:a", "flac", "-ac", "1", "-metadata", "TITLE="+s.Title, "-metadata", "ARTIST="+s.Author, "-f", "flac", path)
}

// GenEncryptedM4A writes a two-second MP4 audio file protected with CENC (a test key), the kind of
// file whose packets carry "Encryption info" side data.
func GenEncryptedM4A(t testing.TB, tools media.Tools, path string) {
	t.Helper()
	ffmpeg(t, tools, "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050:duration=2",
		"-c:a", "aac", "-b:a", "32k", "-ac", "1", "-encryption_scheme", "cenc-aes-ctr",
		"-encryption_key", "00112233445566778899aabbccddeeff", "-encryption_kid", "0123456789abcdef0123456789abcdef",
		"-f", "mp4", path)
}

// WriteProtected writes a stand-in for a copy-protected audiobook: a fake that would probe fine,
// under a protected extension. The runner must refuse it by name without running anything.
func WriteProtected(t testing.TB, path string) {
	t.Helper()
	if media.Classify(path) != media.KindProtected {
		t.Fatalf("mediatest: %s does not have a protected extension", path)
	}
	WriteFake(t, path, FakeFile{Chapters: []FakeChapter{{0, 1000, "One"}}, Cover: true})
}

// GenImage writes a solid-colour picture of w by h pixels with real ffmpeg. The format follows the
// extension of path: .png, .webp, or JPEG for anything else. hex is "#rrggbb".
func GenImage(t testing.TB, tools media.Tools, path, hex string, w, h int) {
	t.Helper()
	codec, format := "mjpeg", "image2"
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		codec = "png"
	case ".webp":
		codec, format = "libwebp", "webp"
	}
	ffmpeg(t, tools, "-f", "lavfi", "-i", fmt.Sprintf("color=c=0x%s:s=%dx%d:d=1", strings.TrimPrefix(hex, "#"), w, h),
		"-frames:v", "1", "-c:v", codec, "-f", format, path)
}
