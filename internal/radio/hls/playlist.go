package hls

import (
	"bytes"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Variant is one stream a master playlist offers.
type Variant struct {
	URI        string // absolute
	Bandwidth  int    // bits per second, 0 when the playlist does not say
	Codecs     []string
	Resolution string // set only for video
}

// Master is a parsed master playlist (RFC 8216 section 4.3.4). An EXT-X-MEDIA TYPE=AUDIO entry with a
// URI is appended as a Variant whose Codecs come from the STREAM-INF of its group.
type Master struct{ Variants []Variant }

// Segment is one media segment (RFC 8216 section 4.3.2).
type Segment struct {
	Seq           int64
	URI           string // absolute
	Duration      time.Duration
	Title         string
	Discontinuity bool
	Map           string // absolute URI of the initialisation section in force, or ""
}

// Media is a parsed media playlist (RFC 8216 section 4.3.3).
type Media struct {
	Target   time.Duration
	Sequence int64
	Segments []Segment
	EndList  bool
}

// Parse reads one playlist. Exactly one of the results is non-nil on success. Every URI in them is
// absolute, resolved against base (the final address of this playlist). Unknown tags are ignored, as
// RFC 8216 section 4.1 asks.
func Parse(body []byte, base *url.URL) (*Master, *Media, error) {
	if len(body) > maxPlaylist {
		return nil, nil, ErrTooLarge
	}
	body = bytes.TrimPrefix(body, []byte("\xEF\xBB\xBF"))
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r \t") != "#EXTM3U" {
		return nil, nil, ErrNotPlaylist
	}
	p := &parser{base: base, media: &Media{}}
	for _, line := range lines[1:] {
		if len(line) > maxLine {
			return nil, nil, ErrTooLarge
		}
		if err := p.line(strings.TrimSpace(line)); err != nil {
			return nil, nil, err
		}
	}
	return p.finish()
}

type parser struct {
	base *url.URL

	// media playlist state
	media         *Media
	haveTarget    bool
	haveSegment   bool // an EXTINF waits for its URI
	pending       Segment
	discontinuity bool
	mapURI        string
	sawMediaTag   bool

	// master playlist state
	variants   []Variant
	streamInf  map[string]string // STREAM-INF waiting for its URI
	infs       []map[string]string
	audioMedia []map[string]string
	sawMaster  bool
}

func (p *parser) resolve(ref string) (string, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", ErrUnsupported
	}
	if p.base != nil {
		u = p.base.ResolveReference(u)
	}
	return u.String(), nil
}

func (p *parser) line(line string) error {
	switch {
	case line == "":
		return nil
	case !strings.HasPrefix(line, "#"):
		return p.uri(line)
	case !strings.HasPrefix(line, "#EXT"):
		return nil // comment
	}
	name, val, _ := strings.Cut(line, ":")
	switch name {
	case "#EXT-X-KEY", "#EXT-X-SESSION-KEY":
		// Any method but NONE is encryption (invariant 4); nothing is decrypted.
		if m, ok := attrs(val)["METHOD"]; !ok || m != "NONE" {
			return ErrLocked
		}
	case "#EXT-X-BYTERANGE":
		return ErrUnsupported
	case "#EXT-X-STREAM-INF":
		p.sawMaster = true
		p.streamInf = attrs(val)
	case "#EXT-X-MEDIA":
		p.sawMaster = true
		a := attrs(val)
		if strings.EqualFold(a["TYPE"], "AUDIO") && a["URI"] != "" {
			p.audioMedia = append(p.audioMedia, a)
		}
	case "#EXT-X-TARGETDURATION":
		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 1 || n > 30 {
			return ErrUnsupported
		}
		p.haveTarget = true
		p.media.Target = time.Duration(n) * time.Second
	case "#EXT-X-MEDIA-SEQUENCE":
		n, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
		if err != nil || n < 0 {
			return ErrUnsupported
		}
		p.media.Sequence = n
	case "#EXT-X-DISCONTINUITY":
		p.discontinuity = true
	case "#EXT-X-MAP":
		a := attrs(val)
		if a["BYTERANGE"] != "" {
			return ErrUnsupported
		}
		u, err := p.resolve(a["URI"])
		if err != nil || a["URI"] == "" {
			return ErrUnsupported
		}
		p.mapURI = u
	case "#EXTINF":
		dur, title, _ := strings.Cut(val, ",")
		secs, err := strconv.ParseFloat(strings.TrimSpace(dur), 64)
		if err != nil || secs < 0 || secs > 3600 || math.IsNaN(secs) {
			return ErrUnsupported
		}
		p.sawMediaTag = true
		p.haveSegment = true
		p.pending = Segment{Duration: time.Duration(secs * float64(time.Second)), Title: extinfTitle(title)}
	case "#EXT-X-ENDLIST":
		p.media.EndList = true
	}
	return nil
}

// uri handles a non-tag line: the address of the segment or variant named by the tag before it.
func (p *parser) uri(line string) error {
	abs, err := p.resolve(line)
	if err != nil {
		return err
	}
	switch {
	case p.streamInf != nil:
		a := p.streamInf
		p.streamInf = nil
		bw, _ := strconv.Atoi(a["BANDWIDTH"])
		p.variants = append(p.variants, Variant{
			URI: abs, Bandwidth: bw, Codecs: splitCodecs(a["CODECS"]), Resolution: a["RESOLUTION"],
		})
		p.infs = append(p.infs, a)
	case p.haveSegment:
		if len(p.media.Segments) >= MaxSegments {
			return ErrTooLarge
		}
		s := p.pending
		s.URI, s.Discontinuity, s.Map = abs, p.discontinuity, p.mapURI
		s.Seq = p.media.Sequence + int64(len(p.media.Segments))
		p.media.Segments = append(p.media.Segments, s)
		p.haveSegment, p.discontinuity = false, false
	}
	return nil
}

func (p *parser) finish() (*Master, *Media, error) {
	if p.sawMaster {
		if p.sawMediaTag {
			return nil, nil, ErrUnsupported // master and media tags in one playlist
		}
		m := &Master{Variants: p.variants}
		for _, a := range p.audioMedia {
			u, err := p.resolve(a["URI"])
			if err != nil {
				return nil, nil, ErrUnsupported
			}
			m.Variants = append(m.Variants, Variant{URI: u, Codecs: p.groupCodecs(a["GROUP-ID"])})
		}
		return m, nil, nil
	}
	if !p.sawMediaTag && !p.haveTarget {
		return nil, nil, ErrNotPlaylist // #EXTM3U and nothing HLS in it
	}
	if !p.haveTarget {
		return nil, nil, ErrUnsupported
	}
	return nil, p.media, nil
}

// groupCodecs returns the audio codecs named by the STREAM-INF lines that point at an audio group
// (their CODECS list also holds the video codec, which is left out).
func (p *parser) groupCodecs(group string) []string {
	if group == "" {
		return nil
	}
	for _, a := range p.infs {
		if a["AUDIO"] != group {
			continue
		}
		var out []string
		for _, c := range splitCodecs(a["CODECS"]) {
			if !isVideoCodec(c) {
				out = append(out, c)
			}
		}
		return out
	}
	return nil
}

func splitCodecs(s string) []string {
	var out []string
	for c := range strings.SplitSeq(s, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// extinfTitle takes the text after the comma of an EXTINF line. Some stations put
// `title="..."` and `artist="..."` there (a text line is otherwise used as it is).
func extinfTitle(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, `title="`) {
		return s
	}
	a := attrs(s)
	t := a["TITLE"]
	if t == "" {
		return s
	}
	if artist := a["ARTIST"]; artist != "" {
		return artist + " - " + t
	}
	return t
}

// attrs reads an attribute list (RFC 8216 section 4.2): NAME=value pairs separated by commas, where
// a quoted value may hold commas. Names are upper-cased, quotes removed. Malformed pieces are skipped.
func attrs(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 && len(out) < 64 {
		s = strings.TrimLeft(s, ", \t")
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		name := strings.ToUpper(strings.TrimSpace(s[:eq]))
		s = s[eq+1:]
		var val string
		if strings.HasPrefix(s, `"`) {
			end := strings.IndexByte(s[1:], '"')
			if end < 0 {
				val, s = s[1:], ""
			} else {
				val, s = s[1:1+end], s[2+end:]
			}
		} else {
			end := strings.IndexByte(s, ',')
			if end < 0 {
				end = len(s)
			}
			val, s = strings.TrimSpace(s[:end]), s[end:]
		}
		if name != "" && !strings.ContainsAny(name, " \t") {
			out[name] = val
		}
	}
	return out
}

// isVideoCodec reports whether a CODECS entry names video (RFC 6381 four-character codes).
func isVideoCodec(c string) bool {
	c = strings.ToLower(c)
	for _, p := range []string{"avc1", "avc3", "hvc1", "hev1", "dvh1", "dvhe", "vp08", "vp09", "av01", "mp4v"} {
		if strings.HasPrefix(c, p) {
			return true
		}
	}
	return false
}

// supportedCodec reports whether a CODECS entry is AAC or MP3 audio the demuxers can handle.
func supportedCodec(c string) bool {
	switch strings.ToLower(c) {
	case "mp4a.40.2", "mp4a.40.5", "mp4a.40.29", "mp4a.40.34", "mp4a.6b", "mp3":
		return true
	}
	return false
}

// ChooseVariant picks the highest audio bandwidth up to 320 kbit/s, or else the lowest audio one.
// Video variants are never picked. ErrVideo means the master offers no audio variant at all;
// ErrCodec means it offers audio, but none the demuxers handle.
func ChooseVariant(m *Master) (Variant, error) {
	var ok, unsupported []Variant
	for _, v := range m.Variants {
		switch classify(v) {
		case audioOK:
			ok = append(ok, v)
		case audioOther:
			unsupported = append(unsupported, v)
		}
	}
	if len(ok) == 0 {
		if len(unsupported) > 0 {
			return Variant{}, ErrCodec
		}
		return Variant{}, ErrVideo
	}
	const want = 320000
	best, low := -1, 0
	for i, v := range ok {
		if v.Bandwidth <= want && (best < 0 || v.Bandwidth > ok[best].Bandwidth) {
			best = i
		}
		if v.Bandwidth < ok[low].Bandwidth {
			low = i
		}
	}
	if best < 0 {
		best = low
	}
	return ok[best], nil
}

type variantKind int

const (
	notAudio   variantKind = iota // video, or a resolution with no codec list
	audioOK                       // audio the demuxers handle (or a plain stream with no codec list)
	audioOther                    // audio in a codec they do not handle
)

func classify(v Variant) variantKind {
	if v.Resolution != "" {
		return notAudio
	}
	kind := audioOK
	for _, c := range v.Codecs {
		switch {
		case isVideoCodec(c):
			return notAudio
		case !supportedCodec(c):
			kind = audioOther
		}
	}
	return kind
}
