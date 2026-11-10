package hls

import (
	"context"
	"errors"
	"io"
	"net/url"
	"time"
)

// Fetcher is the network side of a session. podnet.Client satisfies it, so every request keeps the
// publisher guards (address checks, redirects, size caps, no cookies); this package never creates an
// HTTP client itself (plan invariant 1).
type Fetcher interface {
	Playlist(ctx context.Context, rawURL string) (body []byte, finalURL string, err error)
	Segment(ctx context.Context, rawURL string, timeout time.Duration) ([]byte, error)
}

// Options tune a session. A nil Sleep waits on a timer.
type Options struct {
	OnTitle func(string)
	Sleep   func(context.Context, time.Duration) error
}

// Session turns a live HLS station into one continuous ADTS or MPEG audio stream. It is read by one
// goroutine; Close may be called from another.
type Session struct {
	ctx    context.Context
	cancel context.CancelFunc
	f      Fetcher
	o      Options

	mediaURL string // final address of the media playlist; reloaded each round
	media    *Media
	format   Format
	haveFmt  bool

	last      int64 // newest sequence number taken (or skipped)
	buf       []byte
	err       error // sticky
	idle      bool  // the last reload brought no new segment
	segFails  int
	listFails int

	initURI   string
	initBytes []byte
	title     string
}

// Open resolves the variant, loads the media playlist and the first segment, so Format is known and
// a locked, video-only or unplayable station is refused before any byte is promised to a listener.
func Open(ctx context.Context, f Fetcher, playlistURL string, o Options) (*Session, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &Session{ctx: ctx, cancel: cancel, f: f, o: o, last: -1}
	if o.Sleep == nil {
		s.o.Sleep = sleepTimer
	}
	if err := s.start(playlistURL); err != nil {
		cancel()
		return nil, err
	}
	if err := s.fill(); err != nil {
		cancel()
		return nil, err
	}
	return s, nil
}

// start loads the playlist, follows a master to its chosen variant and picks the first segment.
func (s *Session) start(rawURL string) error {
	master, media, final, err := s.load(rawURL)
	if err != nil {
		return err
	}
	if master != nil {
		v, err := ChooseVariant(master)
		if err != nil {
			return err
		}
		var m2 *Master
		m2, media, final, err = s.load(v.URI)
		if err != nil {
			return err
		}
		if m2 != nil || media == nil {
			return ErrUnsupported
		}
	}
	s.mediaURL = final
	s.media = media
	s.restart()
	return nil
}

// load fetches and parses one playlist. final is its address after redirects.
func (s *Session) load(rawURL string) (*Master, *Media, string, error) {
	body, final, err := s.f.Playlist(s.ctx, rawURL)
	if err != nil {
		return nil, nil, "", s.fail(err)
	}
	base, err := url.Parse(final)
	if err != nil {
		return nil, nil, "", ErrUnsupported
	}
	m, md, err := Parse(body, base)
	if err != nil {
		return nil, nil, "", err
	}
	return m, md, final, nil
}

// fail prefers the context's error when the session was cancelled.
func (s *Session) fail(err error) error {
	if cerr := s.ctx.Err(); cerr != nil {
		return cerr
	}
	return err
}

// restart places the cursor StartBack segments before the end of the current playlist.
func (s *Session) restart() {
	segs := s.media.Segments
	if len(segs) == 0 {
		return
	}
	i := max(0, len(segs)-StartBack)
	s.last = segs[i].Seq - 1
}

// Format is the audio format of the stream. It is fixed for the life of the session.
func (s *Session) Format() Format { return s.format }

// Recording reports whether the playlist carried EXT-X-ENDLIST when it was last loaded, so it is a finished
// recording and not a live station.
func (s *Session) Recording() bool { return s.media != nil && s.media.EndList }

// Close stops the session and any request in flight.
func (s *Session) Close() error {
	s.cancel()
	return nil
}

// Read gives the next bytes of audio frames. io.EOF follows the end of a finished playlist,
// ErrChanged a change of codec, rate or channels, and the context's error a cancel.
func (s *Session) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		if s.err != nil {
			return 0, s.err
		}
		if err := s.ctx.Err(); err != nil {
			s.err = err
			return 0, err
		}
		if err := s.fill(); err != nil {
			s.err = err
			return 0, err
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	if len(s.buf) == 0 {
		s.buf = nil
	}
	return n, nil
}

// fill loads more frames into buf, waiting and reloading the playlist as needed.
func (s *Session) fill() error {
	for len(s.buf) == 0 {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		seg, ok := s.next()
		if !ok {
			if s.media.EndList {
				return io.EOF
			}
			if err := s.reload(); err != nil {
				return err
			}
			continue
		}
		if err := s.take(seg); err != nil {
			return err
		}
	}
	return nil
}

// next is the first segment of the playlist newer than the last one taken.
func (s *Session) next() (Segment, bool) {
	for _, g := range s.media.Segments {
		if g.Seq > s.last {
			return g, true
		}
	}
	return Segment{}, false
}

// reload waits (a target duration, half of one when the last reload brought nothing) and fetches the
// playlist again. Three failures in a row end the session.
func (s *Session) reload() error {
	wait := s.media.Target
	if s.idle {
		wait /= 2
	}
	if err := s.o.Sleep(s.ctx, wait); err != nil {
		return s.fail(err)
	}
	body, final, err := s.f.Playlist(s.ctx, s.mediaURL)
	var md *Media
	if err == nil {
		var base *url.URL
		var m *Master
		if base, err = url.Parse(final); err == nil {
			m, md, err = Parse(body, base)
			if err == nil && (m != nil || md == nil) {
				return ErrUnsupported
			}
		}
	}
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return err
		}
		if cerr := s.ctx.Err(); cerr != nil {
			return cerr
		}
		s.listFails++
		if s.listFails >= MaxFailures {
			return err
		}
		return nil
	}
	s.listFails = 0
	s.mediaURL = final
	// A playlist that ends before our cursor means the origin restarted its numbering.
	if n := len(md.Segments); n > 0 && md.Segments[n-1].Seq < s.last {
		s.media = md
		s.restart()
		s.idle = false
		return nil
	}
	s.media = md
	s.idle = true
	for _, g := range md.Segments {
		if g.Seq > s.last {
			s.idle = false
			break
		}
	}
	return nil
}

// take fetches, demuxes and buffers one segment. A segment that cannot be fetched is retried after
// straight away; one that cannot be read is skipped. Three failures in a row end the session.
func (s *Session) take(g Segment) error {
	timeout := 2 * s.media.Target
	var initSec []byte
	if g.Map != "" {
		if g.Map != s.initURI {
			b, err := s.f.Segment(s.ctx, g.Map, timeout)
			if err == nil && len(b) > MaxInit {
				err = ErrTooLarge
			}
			if err != nil {
				return s.segmentFailed(err, false)
			}
			s.initURI, s.initBytes = g.Map, b
		}
		initSec = s.initBytes
	}
	data, err := s.f.Segment(s.ctx, g.URI, timeout)
	if err == nil && len(data) > MaxSegment {
		err = ErrTooLarge
	}
	if err != nil {
		return s.segmentFailed(err, false)
	}
	frames, f, titles, err := Demux(data, initSec)
	if err == nil && len(frames) == 0 {
		err = ErrCodec
	}
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return err
		}
		return s.segmentFailed(err, true)
	}
	if !s.haveFmt {
		s.format, s.haveFmt = f, true
	} else if f != s.format {
		return ErrChanged
	}
	s.segFails = 0
	s.last = g.Seq
	s.idle = false
	s.buf = frames
	s.announce(g.Title, titles)
	return nil
}

// segmentFailed counts a failure. skip moves past the segment; otherwise it is tried again.
func (s *Session) segmentFailed(err error, skip bool) error {
	if cerr := s.ctx.Err(); cerr != nil {
		return cerr
	}
	s.segFails++
	if s.segFails >= MaxFailures {
		return err
	}
	if skip {
		if g, ok := s.next(); ok {
			s.last = g.Seq
		}
		return nil
	}
	// Give the origin a moment, then try the same segment again.
	if serr := s.o.Sleep(s.ctx, s.media.Target/2); serr != nil {
		return s.fail(serr)
	}
	return nil
}

// announce passes a new station title on, once per change. Titles inside the audio are newer than
// the playlist's own, so the last of them wins.
func (s *Session) announce(playlistTitle string, inband []string) {
	t := playlistTitle
	if n := len(inband); n > 0 {
		t = inband[n-1]
	}
	if t == "" || t == s.title {
		return
	}
	s.title = t
	if s.o.OnTitle != nil {
		s.o.OnTitle(t)
	}
}

func sleepTimer(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
