package scan

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// settled reports whether a file has an answer: probe data or a recorded probe error.
func settled(f *library.AudioFile) bool { return f.ProbeJSON != nil || f.ProbeError != "" }

// playable reports whether a live file can be played: read fine, not encrypted.
func playable(f *library.AudioFile) bool {
	return f.MissingSince == 0 && f.ProbeJSON != nil && !f.Encrypted && f.ProbeError == ""
}

// relKey is the text files of one book are sorted by: the path below the book's folder.
func relKey(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.Base(path)
}

// finalize settles a book from the rows in the database: play order, offsets, duration, chapters
// and the refused reason. It is a pure function of those rows, writes only what differs, and does
// nothing while any live file still waits for its probe. It must run inside the write transaction
// that settled the book's last file.
func finalize(tx *library.Tx, bookID string, _ int64) error {
	book, err := tx.Book(bookID)
	if err != nil {
		return err
	}
	files, err := tx.AudioFiles(bookID)
	if err != nil {
		return err
	}
	var live []library.AudioFile
	for _, f := range files {
		if f.MissingSince != 0 {
			continue
		}
		if !settled(&f) {
			return nil
		}
		live = append(live, f)
	}
	if len(live) == 0 {
		return nil
	}

	var play, other []library.AudioFile
	for _, f := range live {
		if playable(&f) {
			play = append(play, f)
		} else {
			other = append(other, f)
		}
	}
	sort.SliceStable(other, func(i, j int) bool { return other[i].Path < other[j].Path })

	type parsed struct {
		probe media.Probe
		in    orderInput
	}
	byID := make(map[string]parsed, len(play))
	playByID := make(map[string]library.AudioFile, len(play))
	inputs := make([]orderInput, 0, len(play))
	for _, f := range play {
		var p media.Probe
		_ = json.Unmarshal(f.ProbeJSON, &p) // stored by the probe step; a bad blob just carries no tags
		key := relKey(book.RootPath, f.Path)
		in := orderInput{ID: f.ID, Path: f.Path, Key: key}
		in.NameDisc, in.NameTrack, in.HasNameDisc, in.HasNameTrack =
			trackNumbers(filepath.Base(f.Path), filepath.Dir(f.Path), book.Title)
		in.TagDisc, in.HasTagDisc = tagNumber(p.Tags, "disc", "discnumber")
		in.TagTrack, in.HasTagTrack = tagNumber(p.Tags, "track", "tracknumber")
		byID[f.ID] = parsed{p, in}
		playByID[f.ID] = f
		inputs = append(inputs, in)
	}
	ordered := smartOrder(inputs)

	var offset int64
	idx := 0
	var tl []timed
	want := make(map[string]library.AudioFile, len(live))
	for _, in := range ordered {
		f := playByID[in.ID]
		f.Idx, f.OffsetMs = idx, offset
		want[f.ID] = f
		pr := byID[f.ID].probe
		tl = append(tl, timed{OffsetMs: offset, DurationMs: f.DurationMs, Name: filepath.Base(f.Path),
			TagTitle: pr.Tags["title"], Chapters: pr.Chapters})
		offset += f.DurationMs
		idx++
	}
	total := offset
	for _, f := range other {
		f.Idx, f.OffsetMs = idx, total
		want[f.ID] = f
		idx++
	}
	for _, f := range live {
		w := want[f.ID]
		if w.Idx == f.Idx && w.OffsetMs == f.OffsetMs {
			continue
		}
		if err := tx.PutAudioFile(&w); err != nil {
			return err
		}
	}

	refused := ""
	if len(play) == 0 {
		refused = library.RefusedDamaged
		for _, f := range other {
			if f.Encrypted {
				refused = library.RefusedDRM
				break
			}
		}
	}
	if book.DurationMs != total || book.RefusedReason != refused {
		book.DurationMs, book.RefusedReason = total, refused
		if err := tx.UpdateBook(book); err != nil {
			return err
		}
	}

	chs := bookChapters(book.Title, tl)
	old, err := tx.Chapters(bookID)
	if err != nil {
		return err
	}
	if !sameChapters(old, chs) {
		return tx.ReplaceChapters(bookID, chs)
	}
	return nil
}

func sameChapters(a, b []library.Chapter) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
