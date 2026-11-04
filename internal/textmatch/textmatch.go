// Package textmatch compares titles, names and identifiers: the one normaliser behind online lookups, EPUB
// identifiers and ebook pairing. Ratio ports Python difflib.SequenceMatcher.ratio; see NOTICES.md.
// It imports only the standard library.
package textmatch

import (
	"strings"
	"unicode"
)

// foldTable maps accented Latin letters to plain ones; used only to compare, never to store.
var foldTable = buildFold()

func buildFold() map[rune]string {
	groups := map[string]string{
		"a": "àáâãäåāăą", "c": "çćĉč", "d": "ďđð", "e": "èéêëēĕėęě", "g": "ĝğġģ", "h": "ĥħ",
		"i": "ìíîïĩīĭįı", "j": "ĵ", "k": "ķ", "l": "ĺļľł", "n": "ñńņň", "o": "òóôõöøōŏő",
		"r": "ŕř", "s": "śŝşš", "t": "ţťŧ", "u": "ùúûüũūŭůűų", "w": "ŵ", "y": "ýÿŷ", "z": "źżž",
	}
	m := map[rune]string{'ß': "ss", 'æ': "ae", 'œ': "oe", 'þ': "th"}
	for plain, accented := range groups {
		for _, r := range accented {
			m[r] = plain
		}
	}
	return m
}

// Fold returns the plain-letter form of r (possibly two letters), or r itself.
func Fold(r rune) string {
	if s, ok := foldTable[r]; ok {
		return s
	}
	return string(r)
}

// Norm prepares text for comparing: lower case, accents folded, "&" as "and", only letters,
// digits and single spaces, at most 199 runes (so difflib's autojunk rule never applies).
func Norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '&':
			b.WriteString(" and ")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteString(Fold(r))
		case r == ' ':
			b.WriteRune(' ')
		default:
			// dropped
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(out); len(rs) > 199 {
		out = strings.TrimSpace(string(rs[:199]))
	}
	return out
}

// Ratio re-implements Python difflib.SequenceMatcher(None, a, b).Ratio() over runes.
func Ratio(a, b string) float64 {
	ar, br := []rune(a), []rune(b)
	total := len(ar) + len(br)
	if total == 0 {
		return 1
	}
	b2j := map[rune][]int{}
	for j, r := range br {
		b2j[r] = append(b2j[r], j)
	}
	matches := 0
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, len(ar), 0, len(br)}}
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := longestMatch(ar, b2j, s.alo, s.ahi, s.blo, s.bhi)
		if k == 0 {
			continue
		}
		matches += k
		if s.alo < i && s.blo < j {
			queue = append(queue, span{s.alo, i, s.blo, j})
		}
		if i+k < s.ahi && j+k < s.bhi {
			queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
		}
	}
	return 2 * float64(matches) / float64(total)
}

// longestMatch finds the longest common block in a[alo:ahi] and b[blo:bhi]; ties go to the
// earliest start in a, then in b, as difflib does.
func longestMatch(a []rune, b2j map[rune][]int, alo, ahi, blo, bhi int) (besti, bestj, bestsize int) {
	besti, bestj = alo, blo
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range b2j[a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	return
}

// SplitNames splits "A, B & C" style author strings for comparing.
func SplitNames(s string) []string {
	s = strings.NewReplacer("&", ",", ";", ",", " and ", ",").Replace(s)
	var out []string
	for _, p := range strings.Split(s, ",") {
		if n := Norm(p); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// ISBN returns the bare ISBN-10 or ISBN-13 in s when its check digit is right, else "".
func ISBN(s string) string {
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "URN:ISBN:")
	s = strings.TrimPrefix(s, "ISBN:")
	s = strings.NewReplacer("-", "", " ", "").Replace(s)
	switch len(s) {
	case 10:
		sum := 0
		for i := 0; i < 10; i++ {
			c := s[i]
			v := 0
			switch {
			case c >= '0' && c <= '9':
				v = int(c - '0')
			case c == 'X' && i == 9:
				v = 10
			default:
				return ""
			}
			sum += v * (10 - i)
		}
		if sum%11 == 0 {
			return s
		}
	case 13:
		sum := 0
		for i := 0; i < 13; i++ {
			if s[i] < '0' || s[i] > '9' {
				return ""
			}
			v := int(s[i] - '0')
			if i%2 == 1 {
				v *= 3
			}
			sum += v
		}
		if sum%10 == 0 {
			return s
		}
	}
	return ""
}
