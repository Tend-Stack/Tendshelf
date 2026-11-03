package media

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The write engine: the commands behind "write details into the file" (M4-5). These functions
// never rename or delete anything. They write only to the dst path the caller names, plus one
// ffmetadata file in the caller's metaDir (outside the library), which is removed afterwards.
// Nothing calls them on library files yet; the job and ledger that do come in a later package.

// Write limits: 2 minutes plus 1 minute per GiB of input; the output goes to a file, so stdout
// only ever carries a hash line.
const (
	writeBase      = 2 * time.Minute
	writePerGiB    = time.Minute
	writeStdoutCap = 4 << 10
)

// TagWrite is what to write: the muxer, the tags, and optionally the chapter list.
type TagWrite struct {
	Format        string // "mp4", "mp3" or "flac", from WriteFormat
	Tags          map[string]string
	Chapters      []WriteChapter
	WriteChapters bool // replace the chapters with Chapters (not for flac); false keeps the file's own
}

// WriteChapter is one chapter in milliseconds.
type WriteChapter struct {
	StartMs, EndMs int64
	Title          string
}

// WriteFormat names the ffmpeg muxer that can write tags into a file of this container and
// extension, or false when Tendshelf does not write that kind of file. container is
// Probe.Container; path supplies the extension.
func WriteFormat(container, path string) (string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	switch {
	case (ext == "m4b" || ext == "m4a" || ext == "mp4") && (container == "mp4" || container == "mov"):
		return "mp4", true
	case ext == "mp3" && container == "mp3":
		return "mp3", true
	case ext == "flac" && container == "flac":
		return "flac", true
	}
	return "", false
}

// escapeFFMeta escapes the characters that are special in an ffmetadata file: = ; # \ and newline.
func escapeFFMeta(s string) string {
	return strings.NewReplacer(`\`, `\\`, "=", `\=`, ";", `\;`, "#", `\#`, "\n", "\\\n").Replace(s)
}

// ffmetadataChapters renders the chapters as an ffmetadata file with millisecond timebase.
func ffmetadataChapters(chs []WriteChapter) string {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	for _, c := range chs {
		fmt.Fprintf(&b, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", c.StartMs, c.EndMs, escapeFFMeta(c.Title))
	}
	return b.String()
}

// writeTagsArgs builds the argument list. metaPath is the ffmetadata file, "" when the chapters
// are kept as they are.
func writeTagsArgs(src, dst, metaPath string, w TagWrite) []string {
	args := append([]string{}, ffmpegCommon...)
	args = append(args, "-i", "file:"+src)
	chapterInput := "0"
	if metaPath != "" {
		args = append(args, "-f", "ffmetadata", "-i", "file:"+metaPath)
		chapterInput = "1"
	}
	args = append(args, "-map", "0", "-map_metadata", "0", "-map_chapters", chapterInput, "-c", "copy")
	tags := w.Tags
	if w.Format == "flac" {
		// The flac muxer stores "description" as COMMENT (and merges it with comment), so it is
		// written under the name it is read back by.
		tags = map[string]string{}
		for k, v := range w.Tags {
			if k == "description" {
				k = "comment"
			}
			tags[k] = v
		}
	}
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "-metadata", k+"="+tags[k])
	}
	switch w.Format {
	case "mp4":
		args = append(args, "-movflags", "+use_metadata_tags")
	case "mp3":
		args = append(args, "-id3v2_version", "4")
	}
	return append(args, "-f", w.Format, "file:"+dst)
}

func writeTimeout(src string) time.Duration {
	d := writeBase
	if st, err := os.Stat(src); err == nil {
		d += time.Duration(st.Size()/(1<<30)) * writePerGiB
	}
	return d
}

// WriteTags stream-copies src to dst with the tags (and chapters) of w. src is only read; the
// caller names dst, which must not be src. The audio is not re-encoded, so AudioHash(src) equals
// AudioHash(dst) when it works.
func (t Tools) WriteTags(ctx context.Context, src, dst, metaDir string, w TagWrite) error {
	switch w.Format {
	case "mp4", "mp3", "flac":
	default:
		return fmt.Errorf("media: cannot write tags to format %q", w.Format)
	}
	if !filepath.IsAbs(dst) || filepath.Clean(dst) == filepath.Clean(src) {
		return errors.New("media: the tag output must be an absolute path other than the input")
	}
	metaPath := ""
	if w.WriteChapters && w.Format != "flac" {
		f, err := os.CreateTemp(metaDir, "tendshelf-chapters-*.ffmeta")
		if err != nil {
			return err
		}
		metaPath = f.Name()
		defer os.Remove(metaPath)
		_, werr := f.WriteString(ffmetadataChapters(w.Chapters))
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		if metaPath, err = filepath.Abs(metaPath); err != nil {
			return err
		}
	}
	_, err := t.ffmpeg(ctx, src, writeTimeout(src), writeStdoutCap, writeTagsArgs(src, dst, metaPath, w))
	return err
}

// AudioHash is the SHA-256 of the audio packets only, so changing tags, chapters or pictures does
// not change it.
func (t Tools) AudioHash(ctx context.Context, path string) (string, error) {
	args := ffmpegArgs(path, "-map", "0:a", "-c", "copy", "-f", "hash", "-hash", "sha256", "-")
	out, err := t.ffmpeg(ctx, path, writeTimeout(path), writeStdoutCap, args)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if hex, ok := strings.CutPrefix(strings.TrimSpace(line), "SHA256="); ok && len(hex) == 64 {
			return strings.ToLower(hex), nil
		}
	}
	return "", newError(CodeFFmpegFailed, "ffmpeg printed no SHA256 line", nil)
}
