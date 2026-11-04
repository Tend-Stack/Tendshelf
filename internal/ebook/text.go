package ebook

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	xunicode "golang.org/x/text/encoding/unicode"
)

// decodeText returns UTF-8 text: a UTF-16 byte-order mark is decoded as UTF-16, invalid UTF-8 as
// Windows-1252, and a UTF-8 byte-order mark is removed.
func decodeText(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		if s, err := xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM).NewDecoder().Bytes(data); err == nil {
			return string(s)
		}
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		if s, err := xunicode.UTF16(xunicode.BigEndian, xunicode.UseBOM).NewDecoder().Bytes(data); err == nil {
			return string(s)
		}
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if utf8.Valid(data) {
		return string(data)
	}
	if s, err := charmap.Windows1252.NewDecoder().Bytes(data); err == nil {
		return string(s)
	}
	return strings.ToValidUTF8(string(data), "�")
}

var chapterLineRE = regexp.MustCompile(`(?i)^(chapter|capítulo|capitulo|part|parte|book|libro)\s+(\d+|[ivxlcdm]+|\pL+)\b`)

// textBlock is one paragraph-sized piece of a plain text or Markdown file.
type textBlock struct {
	kind  string
	level int
	segs  []seg
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

var (
	mdHeadingRE = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)
	mdHRRE      = regexp.MustCompile(`^\s*([-*_])(\s*[-*_]){2,}\s*$`)
	mdItemRE    = regexp.MustCompile(`^\s*([-*+]|\d+[.)])\s+(.*)$`)
)

// parseText reads txt (md false) or Markdown (md true).
func parseText(data []byte, md bool, lim Limits) (*Doc, error) {
	lines := splitLines(decodeText(data))
	var blocks []textBlock
	var para []string
	flushPara := func(kind string) {
		if len(para) == 0 {
			return
		}
		text := strings.Join(para, " ")
		para = nil
		if md {
			blocks = append(blocks, textBlock{kind: kind, segs: mdInline(text)})
		} else {
			blocks = append(blocks, textBlock{kind: kind, segs: []seg{{Text: text, ByteOff: -1}}})
		}
	}
	var quote []string
	flushQuote := func() {
		if len(quote) > 0 {
			blocks = append(blocks, textBlock{kind: KindQuote, segs: mdInline(strings.Join(quote, " "))})
			quote = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if md {
			if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
				flushPara(KindP)
				flushQuote()
				fence := trim[:3]
				var code []string
				for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), fence); i++ {
					code = append(code, lines[i])
				}
				blocks = append(blocks, textBlock{kind: KindPre, segs: []seg{{Text: strings.Join(code, "\n"), ByteOff: -1}}})
				continue
			}
			if strings.HasPrefix(trim, ">") {
				flushPara(KindP)
				quote = append(quote, strings.TrimSpace(strings.TrimPrefix(trim, ">")))
				continue
			}
			flushQuote()
			if m := mdHeadingRE.FindStringSubmatch(trim); m != nil {
				flushPara(KindP)
				blocks = append(blocks, textBlock{kind: KindH, level: len(m[1]), segs: mdInline(m[2])})
				continue
			}
			if trim != "" && mdHRRE.MatchString(trim) {
				flushPara(KindP)
				blocks = append(blocks, textBlock{kind: KindHR})
				continue
			}
			if m := mdItemRE.FindStringSubmatch(line); m != nil {
				flushPara(KindP)
				blocks = append(blocks, textBlock{kind: KindLI, segs: mdInline(m[2])})
				continue
			}
		}
		if trim == "" {
			flushPara(KindP)
			continue
		}
		para = append(para, trim)
	}
	flushPara(KindP)
	flushQuote()

	// Chapter starts.
	var levels []int
	for _, b := range blocks {
		if b.kind == KindH {
			levels = append(levels, b.level)
		}
	}
	split := 0
	if md {
		split = chapterLevel(levels, 6)
	}
	isChapterLine := func(b textBlock) bool {
		if md {
			return false
		}
		t := normalizeString(joinSegs(b.segs))
		return b.kind == KindP && utf8.RuneCountInString(t) <= 60 && chapterLineRE.MatchString(t)
	}
	bld := newBuilder(map[bool]string{true: "md", false: "txt"}[md], lim)
	title := ""
	if md {
		for _, b := range blocks {
			if b.kind == KindH && b.level == 1 {
				title = joinSegs(b.segs)
				break
			}
		}
	}
	for _, b := range blocks {
		switch {
		case md && split != 0 && b.kind == KindH && b.level == split:
			bld.Chapter(joinSegs(b.segs), "", nil)
		case isChapterLine(b):
			bld.Chapter(joinSegs(b.segs), "", nil)
			b = textBlock{kind: KindH, level: 1, segs: b.segs}
		}
		bld.Block(b.kind, b.level, b.segs, nil)
		if bld.err != nil {
			return nil, bld.err
		}
	}
	return bld.Finish(Meta{Title: title}, 0)
}

// mdInline turns the inline marks of one Markdown paragraph into styled segments: **strong**,
// __strong__, *em*, _em_ and `code`. Links keep their text; images keep their alt text.
func mdInline(s string) []seg {
	var out []seg
	var plain strings.Builder
	flush := func() {
		if plain.Len() > 0 {
			out = append(out, seg{Text: plain.String(), ByteOff: -1})
			plain.Reset()
		}
	}
	rs := []rune(s)
	prevWord := false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\' && i+1 < len(rs):
			i++
			plain.WriteRune(rs[i])
			prevWord = true
			continue
		case r == '`':
			if j := indexRune(rs, '`', i+1); j > i {
				flush()
				out = append(out, seg{Text: string(rs[i+1 : j]), Style: "code", ByteOff: -1})
				i = j
				prevWord = true
				continue
			}
		case r == '!' && i+1 < len(rs) && rs[i+1] == '[', r == '[':
			start := i
			if r == '!' {
				start++
			}
			if j := indexRune(rs, ']', start+1); j > 0 && j+1 < len(rs) && rs[j+1] == '(' {
				if k := indexRune(rs, ')', j+2); k > 0 {
					plain.WriteString(string(rs[start+1 : j]))
					i = k
					prevWord = true
					continue
				}
			}
		case r == '*' || r == '_':
			if r == '_' && prevWord {
				break
			}
			n := 1
			if i+1 < len(rs) && rs[i+1] == r {
				n = 2
			}
			marker := strings.Repeat(string(r), n)
			if j := indexMarker(rs, marker, i+n); j > i+n {
				flush()
				style := "em"
				if n == 2 {
					style = "strong"
				}
				out = append(out, seg{Text: string(rs[i+n : j]), Style: style, ByteOff: -1})
				i = j + n - 1
				prevWord = true
				continue
			}
		}
		plain.WriteRune(r)
		prevWord = unicode.IsLetter(r) || unicode.IsDigit(r)
	}
	flush()
	return out
}

func indexRune(rs []rune, r rune, from int) int {
	for i := from; i < len(rs); i++ {
		if rs[i] == r {
			return i
		}
	}
	return -1
}

func indexMarker(rs []rune, marker string, from int) int {
	m := []rune(marker)
	for i := from; i+len(m) <= len(rs); i++ {
		if rs[i] == m[0] && (len(m) == 1 || rs[i+1] == m[1]) {
			return i
		}
	}
	return -1
}
