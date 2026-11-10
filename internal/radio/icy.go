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
