package web

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// playerMime is the type the player gives canPlayType and MediaSource for a file. It names the codec
// when it is known, because a browser answers "maybe" for a bare container.
func playerMime(container, codec string) string {
	c, k := strings.ToLower(container), strings.ToLower(codec)
	switch {
	case c == "mp4" && k == "aac":
		return `audio/mp4; codecs="mp4a.40.2"`
	case c == "mp4" && k == "alac":
		return `audio/mp4; codecs="alac"`
	case c == "mp4":
		return "audio/mp4"
	case c == "mp3":
		return "audio/mpeg"
	case c == "flac":
		return "audio/flac"
	case c == "ogg" && k == "opus":
		return `audio/ogg; codecs="opus"`
	case c == "ogg" && k == "vorbis":
		return `audio/ogg; codecs="vorbis"`
	case c == "webm" && k == "opus":
		return `audio/webm; codecs="opus"`
	case c == "webm" && k == "vorbis":
		return `audio/webm; codecs="vorbis"`
	case c == "wav":
		return "audio/wav"
	case c == "aac":
		return "audio/aac"
	}
	return audioContentType(container, "")
}

// playTarget maps a position on the book's timeline to the file that plays it and the offset inside
// that file. A position past the end is clamped to the last millisecond. When the file holding the
// position cannot be streamed, or the position falls in a gap, playback starts at the beginning of the
// next streamable file. ok is false when no file can be played.
func playTarget(book library.Book, files []library.AudioFile, posMs int64) (fileID string, offsetMs int64, ok bool) {
	var total int64
	for _, f := range files {
		if f.DurationMs > 0 {
			total = max(total, f.OffsetMs+f.DurationMs)
		}
	}
	pos := min(max(posMs, 0), max(total-1, 0))
	var next *library.AudioFile
	for i := range files {
		f := &files[i]
		if f.DurationMs <= 0 || !streamable(book, *f) {
			continue
		}
		if f.OffsetMs <= pos && pos < f.OffsetMs+f.DurationMs {
			return f.ID, pos - f.OffsetMs, true
		}
		if f.OffsetMs > pos && (next == nil || f.OffsetMs < next.OffsetMs) {
			next = f
		}
	}
	if next != nil {
		return next.ID, 0, true
	}
	return "", 0, false
}

// playerView gathers what the person's player needs for a book they may listen to.
func (s *server) playerView(ctx context.Context, u users.User, sc library.Scope, book library.Book) (components.PlayerDataView, error) {
	d, err := s.lib.BookDetail(ctx, sc, book.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	files, err := s.lib.AudioFiles(ctx, book.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	p, err := s.progress.Get(ctx, u.ID, book.ID)
	if err != nil && !errors.Is(err, progress.ErrNotFound) {
		return components.PlayerDataView{}, err
	}
	names := make([]string, 0, len(d.Card.Authors))
	for _, a := range d.Card.Authors {
		names = append(names, a.Name)
	}
	v := components.PlayerDataView{
		BookID:      book.ID,
		Title:       d.Book.Title,
		Author:      strings.Join(names, ", "),
		DurationMs:  book.DurationMs,
		PositionMs:  p.PositionMs,
		Finished:    p.Finished(),
		UpdatedAt:   p.ClientUpdatedAt,
		DeviceID:    p.DeviceID,
		DeviceLabel: p.DeviceLabel,
		ServerNow:   s.now().UnixMilli(),
		Speed:       strconv.FormatFloat(set.Speed, 'f', 2, 64),
		SkipBackS:   set.SkipBackS, SkipForwardS: set.SkipForwardS,
		AutoRewind: set.AutoRewind,
	}
	if c := d.Card.Cover; c != nil {
		v.CoverURL = coverURL(book.ID, c.SHA, 512)
		if set.PlayerTint {
			v.AccentDark, v.AccentLight, v.AccentSepia = c.Accent["dark"], c.Accent["light"], c.Accent["sepia"]
		}
	}
	for _, f := range files {
		v.Files = append(v.Files, components.PlayerFileView{
			ID: f.ID, OffsetMs: f.OffsetMs, DurationMs: f.DurationMs, Mime: playerMime(f.Container, f.Codec),
			MayNeedCompat: media.MayNeedCompat(f.Container, f.Codec), Playable: streamable(book, f),
		})
	}
	for _, c := range d.Chapters {
		v.Chapters = append(v.Chapters, components.PlayerChapterView{Idx: c.Idx, StartMs: c.StartMs, EndMs: c.EndMs, Title: c.Title})
	}
	return v, nil
}
