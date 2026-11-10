package podcast

import (
	"context"
	"os"
	"time"
)

// fileChaptersTimeout bounds one look inside a kept episode file (ffprobe has its own deadline too).
const fileChaptersTimeout = 30 * time.Second

// FileChaptersPossible reports whether an episode could get chapters from its kept file: the reader is
// wired, the publisher named no chapters file and none are stored, the server keeps the audio, and a look
// inside did not just fail or come up empty.
func (s *Service) FileChaptersPossible(ep Episode) bool {
	return s.ProbeChapters != nil && ep.ChaptersURL == "" && ep.ChaptersJSON == "" &&
		s.CacheFile(ep.CachePath) != "" && !s.ChapterFetchBlocked(ep.ID)
}

// ReadFileChapters reads the chapters inside an episode's kept file (ID3 CHAP frames, MP4 chapters) and
// stores them in the Podcasting 2.0 shape, start and title only. A publisher's chapters file always wins,
// so nothing is read when one is named or stored. Fewer than two chapters are not a table of contents and
// store nothing. A failed or empty look is remembered for a few minutes. It returns the stored JSON, or nil.
func (s *Service) ReadFileChapters(ctx context.Context, ep Episode) []byte {
	if !s.FileChaptersPossible(ep) {
		return nil
	}
	file := s.CacheFile(ep.CachePath)
	if fi, err := os.Stat(file); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, fileChaptersTimeout)
	defer cancel()
	found, err := s.ProbeChapters(ctx, file)
	if err != nil {
		s.Log.Debug("podcasts: could not read chapters from the kept file", "episode", ep.ID, "err", err)
		s.NoteChapterFailure(ep.ID)
		return nil
	}
	// The same cuts as a publisher's file: time order, repeated starts dropped, titles trimmed and capped.
	kept := ParseChapters(ChaptersJSON(found))
	if len(kept) < 2 {
		s.NoteChapterFailure(ep.ID)
		return nil
	}
	body := ChaptersJSON(kept)
	if err := s.SetChapters(ctx, ep.ID, body); err != nil {
		s.Log.Warn("podcasts: could not keep chapters", "err", err)
	}
	return body
}
