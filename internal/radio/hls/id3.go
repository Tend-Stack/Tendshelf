package hls

import (
	"bytes"
	"strings"
	"unicode/utf16"
)

// maxTitle caps a title taken from a stream; radio titles are short.
const maxTitle = 1024

// id3Len returns the whole size of the ID3v2 tag at the start of b (header, body and footer), or 0
// when b does not start with one. It does not check that b is long enough to hold it.
// Layout: ID3v2.4 section 3.1 (syncsafe size, footer flag 0x10).
func id3Len(b []byte) int {
	if len(b) < 10 || b[0] != 'I' || b[1] != 'D' || b[2] != '3' || b[3] == 0xFF || b[4] == 0xFF {
		return 0
	}
	size, ok := syncsafe(b[6:10])
	if !ok {
		return 0
	}
	n := 10 + size
	if b[5]&0x10 != 0 {
		n += 10
	}
	return n
}

// syncsafe reads four 7-bit bytes; ok is false when a byte has its top bit set.
func syncsafe(b []byte) (int, bool) {
	n := 0
	for _, c := range b[:4] {
		if c&0x80 != 0 {
			return 0, false
		}
		n = n<<7 | int(c)
	}
	return n, true
}

// id3Title returns "artist - title" from the TPE1 and TIT2 frames of an ID3v2.3 or v2.4 tag, or
// whichever of the two exists, or "". It stops at the end of the tag or of b and never panics.
func id3Title(tag []byte) string {
	n := id3Len(tag)
	if n == 0 {
		return ""
	}
	version, flags := tag[3], tag[5]
	if version != 3 && version != 4 {
		return ""
	}
	end := min(n, len(tag))
	if flags&0x10 != 0 && end == n { // the footer is only there when the whole tag is
		end -= 10
	}
	body := tag[10:end]
	if flags&0x80 != 0 { // unsynchronisation: FF 00 stands for FF
		body = bytes.ReplaceAll(body, []byte{0xFF, 0x00}, []byte{0xFF})
	}
	if flags&0x40 != 0 { // extended header
		if len(body) < 4 {
			return ""
		}
		var skip int
		if version == 4 {
			s, ok := syncsafe(body)
			if !ok {
				return ""
			}
			skip = s // v2.4: size includes itself
		} else {
			skip = 4 + (int(body[0])<<24 | int(body[1])<<16 | int(body[2])<<8 | int(body[3]))
		}
		if skip < 0 || skip > len(body) {
			return ""
		}
		body = body[skip:]
	}
	var title, artist string
	for len(body) >= 10 && body[0] != 0 { // a zero byte starts the padding
		id := string(body[:4])
		var size int
		if version == 4 {
			s, ok := syncsafe(body[4:8])
			if !ok {
				break
			}
			size = s
		} else {
			size = int(body[4])<<24 | int(body[5])<<16 | int(body[6])<<8 | int(body[7])
		}
		body = body[10:]
		if size < 0 || size > len(body) {
			size = len(body) // a truncated tag still yields what it holds
		}
		data := body[:size]
		body = body[size:]
		switch id {
		case "TIT2":
			title = id3Text(data)
		case "TPE1":
			artist = id3Text(data)
		}
	}
	switch {
	case artist != "" && title != "":
		return artist + " - " + title
	case title != "":
		return title
	default:
		return artist
	}
}

// id3Text decodes a text frame body (encoding byte then text) to its first string. Encodings:
// 0 ISO-8859-1, 1 UTF-16 with byte order mark, 2 UTF-16BE, 3 UTF-8 (ID3v2.4 section 4.2).
func id3Text(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	enc, text := data[0], data[1:]
	var s string
	switch enc {
	case 0:
		r := make([]rune, 0, len(text))
		for _, c := range text {
			if c == 0 {
				break
			}
			r = append(r, rune(c))
		}
		s = string(r)
	case 1, 2:
		bigEndian := enc == 2
		if enc == 1 && len(text) >= 2 {
			switch {
			case text[0] == 0xFE && text[1] == 0xFF:
				bigEndian, text = true, text[2:]
			case text[0] == 0xFF && text[1] == 0xFE:
				text = text[2:]
			}
		}
		u := make([]uint16, 0, len(text)/2)
		for i := 0; i+1 < len(text); i += 2 {
			c := uint16(text[i+1])<<8 | uint16(text[i])
			if bigEndian {
				c = uint16(text[i])<<8 | uint16(text[i+1])
			}
			if c == 0 {
				break
			}
			u = append(u, c)
		}
		s = string(utf16.Decode(u))
	case 3:
		if i := bytes.IndexByte(text, 0); i >= 0 {
			text = text[:i]
		}
		s = strings.ToValidUTF8(string(text), "�")
	default:
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) > maxTitle {
		s = strings.ToValidUTF8(s[:maxTitle], "")
	}
	return s
}
