// Port of Audiobookshelf server/utils/parsers/parseNameString.js, parseSeriesString.js and the TendVox
// Extractor extractor_details.py (parse_names, parse_series_string); see NOTICES.md.

package metadata

import (
	"regexp"
	"strings"
)

var particles = map[string]bool{
	"von": true, "van": true, "der": true, "de": true, "del": true, "della": true, "di": true, "da": true,
	"la": true, "le": true, "den": true, "ten": true, "bin": true, "al": true, "el": true,
}

var cjkRe = regexp.MustCompile(`[\x{4E00}-\x{9FFF}\x{3040}-\x{30FF}\x{31F0}-\x{31FF}\x{AC00}-\x{D7AF}]`)

// looksLikeLastNameOnly reports whether a chunk is one word, or a particle plus one word ("von Mises").
func looksLikeLastNameOnly(chunk string) bool {
	parts := strings.Fields(chunk)
	if len(parts) < 2 {
		return true
	}
	return len(parts) == 2 && particles[strings.ToLower(parts[0])]
}

func splitEach(parts []string, sep string) []string {
	var out []string
	for _, a := range parts {
		out = append(out, strings.Split(a, sep)...)
	}
	return out
}

// ParseNames turns "Smith, Jon & Smith, Rose", "Jon Smith and Jane Doe" or "A; B" into a list of
// "First Last" names, in order and without repeats. CJK names pass through unchanged.
func ParseNames(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	comma := strings.Contains(s, ",")
	var split []string
	switch {
	case strings.Contains(s, "&"):
		split = splitEach(strings.Split(s, "&"), ",")
	case strings.Contains(s, " and "):
		split = splitEach(strings.Split(s, " and "), ",")
	case strings.Contains(s, ";"):
		split = splitEach(strings.Split(s, ";"), ",")
	default:
		split = strings.Split(s, ",")
	}
	for i := range split {
		split[i] = strings.TrimSpace(split[i])
	}
	var names []string
	switch {
	case cjkRe.MatchString(split[0]):
		for _, c := range split {
			if c != "" {
				names = append(names, c)
			}
		}
	case len(split) == 1:
		names = []string{strings.Join(strings.Fields(s), " ")}
	case comma && looksLikeLastNameOnly(split[0]):
		if len(split)%2 == 1 {
			split = split[:len(split)-1]
		}
		for i := 0; i+1 < len(split); i += 2 {
			names = append(names, strings.TrimSpace(split[i+1]+" "+split[i]))
		}
	default:
		for _, c := range split {
			names = append(names, strings.Join(strings.Fields(c), " "))
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		if n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// SortName returns "Last, First Middle" for a "First Middle Last" name. A single word is returned
// as it is, and lowercase particles before the last word belong to it: "Ludwig von Mises" becomes
// "von Mises, Ludwig".
func SortName(name string) string {
	words := strings.Fields(name)
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	}
	last := len(words) - 1
	for last > 0 && particles[words[last-1]] {
		last--
	}
	if last == 0 {
		return strings.Join(words, " ")
	}
	return strings.Join(words[last:], " ") + ", " + strings.Join(words[:last], " ")
}

var seriesSeqRe = regexp.MustCompile(` #([^#\s]+)$`)

// ParseSeriesString splits "Name #1a" into ("Name", "1a"); "Name" gives ("Name", ""). ok is false
// for an empty string.
func ParseSeriesString(s string) (name, seq string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if m := seriesSeqRe.FindStringSubmatchIndex(s); m != nil {
		return strings.TrimSpace(s[:m[0]]), s[m[2]:m[3]], true
	}
	return s, "", true
}
