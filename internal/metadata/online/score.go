// Ports the Extractor extractor_online.py (_norm, score) and Python difflib.SequenceMatcher.ratio; see NOTICES.md.

package online

import (
	"math"
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

// fold returns the plain-letter form of r (possibly two letters), or r itself.
func fold(r rune) string {
	if s, ok := foldTable[r]; ok {
		return s
	}
	return string(r)
}

// norm prepares text for comparing: lower case, accents folded, "&" as "and", only letters,
// digits and single spaces, at most 199 runes (so difflib's autojunk rule never applies).
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '&':
			b.WriteString(" and ")
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteString(fold(r))
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

// ratio re-implements Python difflib.SequenceMatcher(None, a, b).ratio() over runes.
func ratio(a, b string) float64 {
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

// splitNames splits "A, B & C" style author strings for comparing.
func splitNames(s string) []string {
	s = strings.NewReplacer("&", ",", ";", ",", " and ", ",").Replace(s)
	var out []string
	for _, p := range strings.Split(s, ",") {
		if n := norm(p); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// confidentTitleRatio is how alike two titles must be, when they are not equal, to count as one.
const confidentTitleRatio = 0.95

// Confident says whether a candidate is safe to apply without a person looking: the title is the
// same (or the same with its subtitle, or nearly) and an author name matches. Score alone is not
// enough, since "Dune Messiah" scores high for "Dune". False when the title or author is blank.
func Confident(b Book, title, author string) bool {
	nt := norm(title)
	if nt == "" || norm(author) == "" {
		return false
	}
	ct := norm(b.Title)
	sameTitle := ct == nt || (b.Subtitle != "" && norm(b.Title+" "+b.Subtitle) == nt) || ratio(nt, ct) >= confidentTitleRatio
	if !sameTitle {
		return false
	}
	have := map[string]bool{}
	for _, p := range b.Authors {
		have[norm(p.Name)] = true
	}
	for _, w := range splitNames(author) {
		if have[w] {
			return true
		}
	}
	return false
}

// Score rates how well a candidate matches the wanted title and author, from 0 to 1.
func Score(b Book, title, author string) float64 {
	nt, ct := norm(title), norm(b.Title)
	t := 0.5
	if title != "" {
		t = ratio(nt, ct)
		if nt != "" && strings.Contains(ct, nt) {
			t = math.Max(t, 0.9)
		}
	}
	a := 0.5
	if author != "" && len(b.Authors) > 0 {
		var names []string
		for _, p := range b.Authors {
			names = append(names, p.Name)
		}
		have := map[string]bool{}
		for _, n := range names {
			have[norm(n)] = true
		}
		hit := false
		for _, w := range splitNames(author) {
			if have[w] {
				hit = true
			}
		}
		if hit {
			a = 1
		} else {
			a = ratio(norm(author), norm(strings.Join(names, " ")))
		}
	}
	return math.Round((0.7*t+0.3*a)*1000) / 1000
}
