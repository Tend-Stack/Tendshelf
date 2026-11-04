// Ports the Extractor extractor_online.py (_norm, score) and Python difflib.SequenceMatcher.ratio; see NOTICES.md.

package online

import (
	"math"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// The text normaliser lives in internal/textmatch; these keep the local names.
func norm(s string) string         { return textmatch.Norm(s) }
func ratio(a, b string) float64    { return textmatch.Ratio(a, b) }
func splitNames(s string) []string { return textmatch.SplitNames(s) }
func fold(r rune) string           { return textmatch.Fold(r) }

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
