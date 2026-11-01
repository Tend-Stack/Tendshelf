// Port of Audiobookshelf server/scanner/AudioFileScanner.js (getBookChaptersFromAudioFiles); see NOTICES.md.

package scan

import (
	"reflect"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// minChapterMs drops slivers when per-file chapter lists are joined.
const minChapterMs = 100

// timed is one playable file on the book's timeline.
type timed struct {
	OffsetMs, DurationMs int64
	Name, TagTitle       string
	Chapters             []media.ProbeChapter
}

func clamp(v, lo, hi int64) int64 { return max(lo, min(v, hi)) }

// bookChapters builds the chapter list of a book from its playable files in play order.
//
//   - One file: its own chapters, or none.
//   - Several files that all carry chapters: if every file repeats the same list and that list runs
//     past the first file, it is the whole book's list; otherwise each file's chapters are shifted to
//     the file's place on the timeline and joined.
//   - Otherwise one chapter per file, titled by the file's tag title when every file has its own,
//     else by the file name.
func bookChapters(bookTitle string, files []timed) []library.Chapter {
	if len(files) == 0 {
		return nil
	}
	if len(files) == 1 {
		f := files[0]
		return number(shift(f.Chapters, 0, f.DurationMs, 0))
	}
	all := true
	for _, f := range files {
		if len(f.Chapters) == 0 {
			all = false
		}
	}
	if all {
		var total int64
		same := true
		for _, f := range files {
			total += f.DurationMs
			same = same && reflect.DeepEqual(f.Chapters, files[0].Chapters)
		}
		first := files[0]
		if last := first.Chapters[len(first.Chapters)-1]; same && last.EndMs > first.DurationMs+1000 {
			return number(shift(first.Chapters, 0, total, 0))
		}
		var out []library.Chapter
		for _, f := range files {
			out = append(out, shift(f.Chapters, f.OffsetMs, f.DurationMs, minChapterMs)...)
		}
		return number(out)
	}
	titles := tagTitlesUsable(bookTitle, files)
	out := make([]library.Chapter, 0, len(files))
	for _, f := range files {
		title := stem(f.Name)
		if titles {
			title = f.TagTitle
		}
		out = append(out, library.Chapter{StartMs: f.OffsetMs, EndMs: f.OffsetMs + f.DurationMs, Title: title, Source: library.ChapterFiles})
	}
	return number(out)
}

// tagTitlesUsable reports whether every file has a tag title that differs from the book's title and
// from every other file's: only then do the tags say more than the file names.
func tagTitlesUsable(bookTitle string, files []timed) bool {
	seen := map[string]bool{}
	for _, f := range files {
		if f.TagTitle == "" || f.TagTitle == bookTitle || seen[f.TagTitle] {
			return false
		}
		seen[f.TagTitle] = true
	}
	return true
}

// shift moves a file's chapters by offset onto the book timeline, clamping them into
// [offset, offset+dur], and drops those shorter than minLen (or empty).
func shift(chs []media.ProbeChapter, offset, dur, minLen int64) []library.Chapter {
	out := make([]library.Chapter, 0, len(chs))
	for _, c := range chs {
		hi := offset + dur
		if dur <= 0 {
			hi = offset + max(c.EndMs, c.StartMs)
		}
		start, end := clamp(offset+c.StartMs, offset, hi), clamp(offset+c.EndMs, offset, hi)
		if end <= start || end-start < minLen {
			continue
		}
		out = append(out, library.Chapter{StartMs: start, EndMs: end, Title: c.Title, Source: library.ChapterFile})
	}
	return out
}

func number(chs []library.Chapter) []library.Chapter {
	for i := range chs {
		chs[i].Idx = i
	}
	return chs
}
