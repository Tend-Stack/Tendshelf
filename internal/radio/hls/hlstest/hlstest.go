// Package hlstest is a fake HLS station for tests: a sliding-window live playlist over synthetic packed
// audio segments (ADTS AAC or MPEG audio frames; nothing here is real audio). It serves only through an
// http.Handler, so tests reach it through the guarded podnet client like any publisher.
package hlstest

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Station configures the fake. The zero value is a live AAC station with a three-segment window.
type Station struct {
	Prefix  string // path prefix the handler answers under, default "/hls/"
	MP3     bool   // segments hold MPEG audio frames instead of ADTS
	Key     string // EXT-X-KEY METHOD to announce ("" means none)
	EndList bool   // finish the playlist (a recording)
	Video   bool   // the master offers only a video variant
	Codecs  string // CODECS of the audio variant, default mp4a.40.2
	Title   string // EXTINF title of every segment
	Window  int    // segments in the playlist, default 3
	Frames  int    // audio frames per segment, default 8

	mu   sync.Mutex
	head int // sequence number of the newest segment
	// Hits counts requests per path, for assertions.
	Hits map[string]int
}

// Handler answers requests under Prefix. Every media playlist request moves the window on by one segment,
// as a live station does between reloads.
func (s *Station) Handler() http.HandlerFunc {
	prefix := s.Prefix
	if prefix == "" {
		prefix = "/hls/"
	}
	return func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		s.mu.Lock()
		if s.Hits == nil {
			s.Hits = map[string]int{}
		}
		s.Hits[name]++
		s.mu.Unlock()
		switch {
		case name == "master.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			s.master(w)
		case name == "a/a.m3u8" || name == "live.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			s.media(w)
		case strings.HasSuffix(name, ".seg"):
			n, err := strconv.Atoi(strings.TrimSuffix(name[strings.LastIndex(name, "/")+1:], ".seg"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(s.Segment(n))
		default:
			http.NotFound(w, r)
		}
	}
}

func (s *Station) master(w http.ResponseWriter) {
	codecs := s.Codecs
	if codecs == "" {
		codecs = "mp4a.40.2"
	}
	if s.MP3 && s.Codecs == "" {
		codecs = "mp4a.6b"
	}
	if s.Video {
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=640x360,CODECS=\"avc1.4d401e,mp4a.40.2\"\na/a.m3u8\n")
		return
	}
	fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=64000,CODECS=%q\na/a.m3u8\n", codecs)
}

func (s *Station) media(w http.ResponseWriter) {
	win := s.Window
	if win <= 0 {
		win = 3
	}
	s.mu.Lock()
	s.head++
	head := s.head
	s.mu.Unlock()
	first := max(1, head-win+1)
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n")
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", first)
	if s.Key != "" {
		fmt.Fprintf(&b, "#EXT-X-KEY:METHOD=%s,URI=\"k.key\"\n", s.Key)
	}
	for n := first; n <= head; n++ {
		fmt.Fprintf(&b, "#EXTINF:1.0,%s\n", s.Title)
		// Relative to the playlist: a/a.m3u8 -> a/N.seg, live.m3u8 -> N.seg.
		fmt.Fprintf(&b, "%d.seg\n", n)
	}
	if s.EndList {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	fmt.Fprint(w, b.String())
}

// Segment is the bytes of segment n: Frames frames whose payload bytes all equal n, so a test can tell
// which segment a frame came from.
func (s *Station) Segment(n int) []byte {
	frames := s.Frames
	if frames <= 0 {
		frames = 8
	}
	var out []byte
	for range frames {
		if s.MP3 {
			out = append(out, 0xFF, 0xFB, 0x90, 0x00)
			out = append(out, bytes.Repeat([]byte{byte(n)}, 417-4)...)
			continue
		}
		const payload = 100
		l := 7 + payload
		out = append(out, 0xFF, 0xF1, 0x50|byte(2>>2), byte(2&3)<<6|byte(l>>11), byte(l>>3), byte(l&7)<<5|0x1F, 0xFC)
		out = append(out, bytes.Repeat([]byte{byte(n)}, payload)...)
	}
	return out
}

// FrameLen is the size of one frame Segment writes.
func (s *Station) FrameLen() int {
	if s.MP3 {
		return 417
	}
	return 107
}
