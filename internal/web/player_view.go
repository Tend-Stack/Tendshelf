package web

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
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
	set, err := s.users.LoadSettings(ctx, u.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	// This book's own speed, if the person set one, beats their usual speed.
	if sp, err := s.users.BookSpeed(ctx, u.ID, book.ID); err == nil {
		set.Speed = sp
	} else if !errors.Is(err, users.ErrNoBookSpeed) {
		return components.PlayerDataView{}, err
	}
	v, err := s.bookPlayerView(ctx, sc, book, set, func(sha string) string { return coverURL(book.ID, sha, 512) })
	if err != nil {
		return v, err
	}
	p, err := s.progress.Get(ctx, u.ID, book.ID)
	if err != nil && !errors.Is(err, progress.ErrNotFound) {
		return components.PlayerDataView{}, err
	}
	v.PositionMs, v.Finished, v.UpdatedAt = p.PositionMs, p.Finished(), p.ClientUpdatedAt
	v.DeviceID, v.DeviceLabel = p.DeviceID, p.DeviceLabel
	// The book icon of the player: only with the right to read and a ready edition. Share links never get one.
	if ok, err := s.mayReadBook(ctx, u, book); err != nil {
		return components.PlayerDataView{}, err
	} else if ok {
		v.ReadHref = readHref(book.ID)
	}
	return v, nil
}

// linkPlayerView gathers what the player needs on a share link's page: the one book of the link,
// default settings, a cover under the link's own prefix and no place (the browser keeps it). It
// reads nothing about any person.
func (s *server) linkPlayerView(ctx context.Context, l share.Link, book library.Book, base string) (components.PlayerDataView, error) {
	books, libs := share.LinkScope(l)
	return s.bookPlayerView(ctx, library.Scope{Books: books, Libraries: libs}, book, users.DefaultSettings, func(sha string) string {
		return base + "/cover?w=512&v=" + sha[:min(8, len(sha))]
	})
}

// bookPlayerView is the part of the player's data that depends on the book and the settings only:
// title, files, chapters, cover and tint. The caller adds the place.
func (s *server) bookPlayerView(ctx context.Context, sc library.Scope, book library.Book, set users.Settings, cover func(sha string) string) (components.PlayerDataView, error) {
	d, err := s.lib.BookDetail(ctx, sc, book.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	files, err := s.lib.AudioFiles(ctx, book.ID)
	if err != nil {
		return components.PlayerDataView{}, err
	}
	names := make([]string, 0, len(d.Card.Authors))
	for _, a := range d.Card.Authors {
		names = append(names, a.Name)
	}
	v := components.PlayerDataView{
		BookID:     book.ID,
		Title:      d.Book.Title,
		Author:     strings.Join(names, ", "),
		DurationMs: book.DurationMs,
		ServerNow:  s.now().UnixMilli(),
		Speed:      strconv.FormatFloat(set.Speed, 'f', 2, 64),
		SkipBackS:  set.SkipBackS, SkipForwardS: set.SkipForwardS,
		AutoRewind: set.AutoRewind, Rewind: rewindAttr(),
	}
	if c := d.Card.Cover; c != nil {
		v.CoverURL = cover(c.SHA)
		if set.PlayerTint {
			v.AccentDark, v.AccentLight, v.AccentSepia = c.Accent["dark"], c.Accent["light"], c.Accent["sepia"]
			v.Tint = c.Tint
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
