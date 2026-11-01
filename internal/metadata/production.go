// Port of TendVox Extractor extractor_details.py (find_production, _FULL_CAST, _DRAMATIZED); see NOTICES.md.

package metadata

import (
	"regexp"
	"strings"
)

var (
	unabridgedRe = regexp.MustCompile(`(?i)\bunabridged\b`)
	abridgedRe   = regexp.MustCompile(`(?i)\babridged\b`)
	fullCastRe   = regexp.MustCompile(`(?i)\bfull[- ]cast\b|\bmulti[- ]?cast\b|\bcast of (?:voices|narrators|actors|performers)\b|\bperformed by a (?:full )?cast\b`)
	dramatizedRe = regexp.MustCompile(`(?i)\bdramati[sz]ed\b|\bdramati[sz]ation\b|\bgraphic ?audio\b|\baudio drama\b|\bradio play\b`)
)

// FindProduction looks for the words that say how a recording was made. "Unabridged" gives
// abridged=false, "abridged" gives true; "full cast" and the dramatized words give dramatized=true.
// A nil result means the words are not there, not that the answer is no.
func FindProduction(texts ...string) (abridged, dramatized *bool) {
	blob := strings.Join(texts, " ")
	switch {
	case unabridgedRe.MatchString(blob):
		abridged = boolp(false)
	case abridgedRe.MatchString(blob):
		abridged = boolp(true)
	}
	if fullCastRe.MatchString(blob) || dramatizedRe.MatchString(blob) {
		dramatized = boolp(true)
	}
	return abridged, dramatized
}

// fillProduction sets Abridged and Dramatized from the words of the other details (and any extra
// text), but only the ones still nil.
func (d *Details) fillProduction(extra ...string) {
	if d.Abridged != nil && d.Dramatized != nil {
		return
	}
	texts := []string{d.Title, d.Subtitle, strings.Join(d.Genres, " ")}
	for _, s := range d.Series {
		texts = append(texts, s.Name)
	}
	texts = append(texts, d.Description)
	texts = append(texts, extra...)
	a, dr := FindProduction(texts...)
	if d.Abridged == nil {
		d.Abridged = a
	}
	if d.Dramatized == nil {
		d.Dramatized = dr
	}
}
