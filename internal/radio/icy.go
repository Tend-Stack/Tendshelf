package radio

import (
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxTitleRunes is the longest now-playing title kept.
const MaxTitleRunes = 200

// Splitter removes the in-stream title blocks of a Shoutcast/Icecast stream. After every Metaint audio
// bytes the stream carries one length byte (n, in units of 16 bytes) and then n*16 bytes of text such as
// StreamTitle='Artist - Song';. Read returns only audio. OnTitle, when set, is called with each cleaned
// title a block carries (an empty one included, so a station that stops naming songs clears it).
type Splitter struct {
	R       io.Reader
	Metaint int
	OnTitle func(title string)

	begun bool
	left  int // audio bytes until the next block
	err   error
}

// Read implements io.Reader. A Metaint of 0 passes the stream through unchanged.
func (s *Splitter) Read(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.Metaint <= 0 {
		return s.R.Read(p)
	}
	if len(p) == 0 {
		return 0, nil
	}
	if !s.begun {
		s.begun, s.left = true, s.Metaint
	}
	if s.left == 0 {
		if err := s.block(); err != nil {
			s.err = err
			return 0, err
		}
		s.left = s.Metaint
	}
	if len(p) > s.left {
		p = p[:s.left]
	}
	n, err := s.R.Read(p)
	s.left -= n
	return n, err
}

// block reads and handles one title block. Its size is one byte times 16, at most 4080 bytes, so a
// hostile station cannot make it large.
func (s *Splitter) block() error {
	var l [1]byte
	if _, err := io.ReadFull(s.R, l[:]); err != nil {
		return err
	}
	n := int(l[0]) * 16
	if n == 0 {
		return nil
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.R, buf); err != nil {
		return err
	}
	if t, ok := ParseTitle(buf); ok && s.OnTitle != nil {
		s.OnTitle(t)
	}
	return nil
}

// ParseTitle reads the StreamTitle out of one block (padded with zero bytes). ok is false when the block
// has none, so junk never changes the title. The title may itself hold apostrophes: it ends at the first
// "';" after the opening quote, or at the end of the block.
func ParseTitle(block []byte) (title string, ok bool) {
	text := strings.TrimRight(string(block), "\x00 ")
	const key = "StreamTitle='"
	i := strings.Index(text, key)
	if i < 0 {
		return "", false
	}
	rest := text[i+len(key):]
	if j := strings.Index(rest, "';"); j >= 0 {
		rest = rest[:j]
	} else {
		rest = strings.TrimSuffix(rest, "'")
	}
	return CleanTitle(rest), true
}

// CleanTitle makes a station's text safe to show: text that is not UTF-8 is read as Latin-1 (what old
// stations send), control and format characters are dropped, white space is collapsed, and the result is cut
// to MaxTitleRunes.
func CleanTitle(s string) string {
	if !utf8.ValidString(s) {
		r := make([]rune, 0, len(s))
		for i := 0; i < len(s); i++ {
			r = append(r, rune(s[i]))
		}
		s = string(r)
	}
	return squeezeTitle(unwrapPairs(s))
}

// squeezeTitle drops control characters, collapses white space and cuts to MaxTitleRunes.
func squeezeTitle(s string) string {
	var b strings.Builder
	space := false
	n := 0
	for _, r := range s {
		if unicode.IsSpace(r) {
			space = b.Len() > 0
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			continue
		}
		if space {
			if n+1 >= MaxTitleRunes {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		if n >= MaxTitleRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

// unwrapPairs reads the shape iHeart streams send: "Artist - text="Song" song_spot="M" MediaBaseId="1" ...". When
// the text after the first " - " (or the whole text) starts with key="value" pairs, the title becomes
// "Artist - <text>", and a title with no text, an advert (song_spot "T" or "A") or nothing but pairs becomes "".
// A bare trailing " - " is dropped. Anything else is returned as it came. It runs before the length cut, because
// the pairs are long.
func unwrapPairs(s string) string {
	s = strings.TrimSpace(s)
	artist, rest := "", s
	if i := strings.Index(s, " - "); i >= 0 {
		artist, rest = strings.TrimSpace(s[:i]), strings.TrimSpace(s[i+3:])
	}
	if pairs, ok := readPairs(rest); ok {
		text := strings.TrimSpace(pairs["text"])
		if sp := pairs["song_spot"]; text == "" || sp == "T" || sp == "A" {
			return ""
		}
		if artist == "" {
			return text
		}
		return artist + " - " + text
	}
	return strings.TrimSpace(strings.TrimSuffix(s, " -"))
}

// readPairs parses key="value" pairs from the start of s. ok is false unless s starts with one. A value cut off
// by the end of the text (an unterminated quote) keeps what there is.
func readPairs(s string) (map[string]string, bool) {
	out := map[string]string{}
	for {
		s = strings.TrimLeft(s, " ")
		if s == "" {
			return out, len(out) > 0
		}
		eq := strings.Index(s, `="`)
		if eq <= 0 || !isKey(s[:eq]) {
			return out, len(out) > 0
		}
		key, val := s[:eq], s[eq+2:]
		if end := strings.IndexByte(val, '"'); end >= 0 {
			out[key], s = val[:end], val[end+1:]
		} else {
			out[key], s = val, ""
		}
	}
}

func isKey(k string) bool {
	for i, r := range k {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return k != ""
}
