package ebook

import (
	"strings"
	"unicode"
)

// segmenter "tendshelf-segment-v1": sentences and words over a normalised block text.

const terminators = ".!?…"
const closers = "”’\"')]»"
const openers = "“‘\"'«([¿¡—–"

var abbreviations = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "dr": true, "st": true, "sr": true, "sra": true, "srta": true,
	"jr": true, "prof": true, "vs": true, "etc": true, "e.g": true, "i.e": true, "no": true, "vol": true,
	"ch": true, "fig": true, "p": true, "pp": true, "approx": true, "ud": true, "uds": true, "dña": true,
}

// u16Index returns, for each rune index (and one past the end), the UTF-16 offset.
func u16Index(rs []rune) []int {
	idx := make([]int, len(rs)+1)
	n := 0
	for i, r := range rs {
		idx[i] = n
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	idx[len(rs)] = n
	return idx
}

func u16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func isSpaceRune(r rune) bool { return unicode.IsSpace(r) }

// startsSentence reports whether r may begin a sentence.
func startsSentence(r rune) bool {
	return unicode.IsUpper(r) || unicode.IsDigit(r) || strings.ContainsRune(openers, r)
}

// abbreviationBefore reports whether the single '.' at rs[dot] ends an abbreviation or an initial.
func abbreviationBefore(rs []rune, dot int) bool {
	i := dot
	for i > 0 && !isSpaceRune(rs[i-1]) {
		i--
	}
	tok := rs[i:dot]
	for len(tok) > 0 && strings.ContainsRune(openers, tok[0]) {
		tok = tok[1:]
	}
	if len(tok) == 0 {
		return false
	}
	if len(tok) == 1 && unicode.IsUpper(tok[0]) {
		return true
	}
	return abbreviations[strings.ToLower(string(tok))]
}

// sentenceSpans returns half-open [start,end) rune ranges.
func sentenceSpans(rs []rune) [][2]int {
	var out [][2]int
	start := 0
	for start < len(rs) && isSpaceRune(rs[start]) {
		start++
	}
	i := start
	for i < len(rs) {
		if !strings.ContainsRune(terminators, rs[i]) {
			i++
			continue
		}
		run := i
		for i < len(rs) && strings.ContainsRune(terminators, rs[i]) {
			i++
		}
		j := i
		for j < len(rs) && strings.ContainsRune(closers, rs[j]) {
			j++
		}
		if j >= len(rs) {
			i = j
			break
		}
		k := j
		for k < len(rs) && isSpaceRune(rs[k]) {
			k++
		}
		if k == j || k >= len(rs) || !startsSentence(rs[k]) {
			i = j
			continue
		}
		if i-run == 1 && rs[run] == '.' && abbreviationBefore(rs, run) {
			i = j
			continue
		}
		out = append(out, [2]int{start, j})
		start = k
		i = k
	}
	if start < len(rs) {
		end := len(rs)
		for end > start && isSpaceRune(rs[end-1]) {
			end--
		}
		if end > start {
			out = append(out, [2]int{start, end})
		}
	}
	return out
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r)
}

// wordSpans returns maximal runs of letters, marks and digits, joined by an inner ' ’ - or ‐.
func wordSpans(rs []rune) [][2]int {
	var out [][2]int
	i := 0
	for i < len(rs) {
		if !isWordRune(rs[i]) {
			i++
			continue
		}
		s := i
		for i < len(rs) {
			if isWordRune(rs[i]) {
				i++
				continue
			}
			if (rs[i] == '\'' || rs[i] == '’' || rs[i] == '-' || rs[i] == '‐') && i+1 < len(rs) && isWordRune(rs[i+1]) {
				i++
				continue
			}
			break
		}
		out = append(out, [2]int{s, i})
	}
	return out
}

// segmentText returns sentence and word spans in UTF-16 units. oneSentence makes the whole text a
// single sentence (headings, pre, img).
func segmentText(text string, oneSentence bool) (sentences, words [][2]int) {
	rs := []rune(text)
	idx := u16Index(rs)
	sentences = [][2]int{}
	words = [][2]int{}
	if len(rs) == 0 {
		return
	}
	if oneSentence {
		sentences = append(sentences, [2]int{0, idx[len(rs)]})
	} else {
		for _, s := range sentenceSpans(rs) {
			sentences = append(sentences, [2]int{idx[s[0]], idx[s[1]]})
		}
	}
	for _, w := range wordSpans(rs) {
		words = append(words, [2]int{idx[w[0]], idx[w[1]]})
	}
	return
}
