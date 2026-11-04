// Package align turns a canonical document and its narration into per-sentence times (read-along, R3).
//
// normalize.go: the number and abbreviation rules follow TendVocal's internal/review/number_forms.go
// (smallCardinal and the "leave identifiers alone" rule) and fidelity_normalize.go (number words as
// atoms); the direction here is digits to spoken words, so the code is new. See NOTICES.md.
package align

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// NormalizeRecipe names the rules below; a change to any rule bumps it so alignments are made again.
const NormalizeRecipe = "tendshelf-normalize-v1"

// Unit is one spoken word the aligner looks for. Src is the index of the source word it came from (several
// units can share one source word: "etc." is two). A Wildcard has no known reading (a year, a symbol), so
// the aligner skips it and never times it.
type Unit struct {
	Src      int
	Norm     string
	Wildcard bool
}

var (
	englishOrdinal = regexp.MustCompile(`^([0-9]+)(st|nd|rd|th)$`)
	// digitsOnly: one or more ASCII digits. Anything else with a digit in it ("1º", "5k", "3.5") is an
	// identifier or mixed token and becomes a wildcard.
	digitsOnly = regexp.MustCompile(`^[0-9]+$`)
)

// Normalize returns the units for words in language lang ("en", "es"; anything else is only lower-cased).
// Every source word yields at least one unit.
func Normalize(lang string, words []ebook.WordSpan) []Unit {
	lang = strings.ToLower(lang)
	if i := strings.IndexAny(lang, "-_"); i >= 0 {
		lang = lang[:i]
	}
	var out []Unit
	for i, w := range words {
		before := len(out)
		text := norm.NFC.String(w.Text)
		text = strings.ToLower(text)
		text = strings.NewReplacer("’", "'", "ʼ", "'", "‘", "'").Replace(text)
		for _, part := range splitDashes(text) {
			out = appendWord(out, lang, i, part, w.DotAfter)
		}
		if len(out) == before {
			out = append(out, Unit{Src: i, Wildcard: true})
		}
	}
	return out
}

// splitDashes splits at hyphens and dashes and drops empty parts.
func splitDashes(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case '-', '‐', '‑', '‒', '–', '—', '―':
			return true
		}
		return false
	})
}

func appendWord(out []Unit, lang string, src int, w string, dotAfter bool) []Unit {
	wild := func() []Unit { return append(out, Unit{Src: src, Wildcard: true}) }
	say := func(words ...string) []Unit {
		for _, s := range words {
			out = append(out, Unit{Src: src, Norm: s})
		}
		return out
	}
	if lang != "en" && lang != "es" {
		return say(w)
	}
	if strings.ContainsAny(w, "0123456789") {
		return appendNumber(out, lang, src, w)
	}
	if dotAfter {
		if forms, ok := abbreviations[lang][w]; ok {
			if forms == nil {
				return wild()
			}
			return say(forms...)
		}
	}
	if lang == "es" {
		if folded, ok := spanishFold[w]; ok {
			return say(folded)
		}
	}
	return say(w)
}

// abbreviations apply only when a full stop follows the word; a nil entry is a wildcard.
var abbreviations = map[string]map[string][]string{
	"en": {
		"mr": {"mister"}, "mrs": {"missus"}, "dr": {"doctor"}, "vs": {"versus"},
		"etc": {"et", "cetera"}, "st": nil,
	},
	"es": {
		"sr": {"señor"}, "sra": {"señora"}, "dr": {"doctor"}, "dra": {"doctora"},
		"ud": {"usted"}, "uds": {"ustedes"}, "etc": {"etcétera"},
	},
}

// spanishFold maps spelled-out forms that differ only by gender or number to one reading, so the same
// number written in digits and in words meets the same unit.
var spanishFold = map[string]string{
	"un": "uno", "una": "uno", "veintiún": "veintiuno", "veintiuna": "veintiuno",
	"cien": "ciento", "millones": "millón",
	"doscientas": "doscientos", "trescientas": "trescientos", "cuatrocientas": "cuatrocientos",
	"quinientas": "quinientos", "seiscientas": "seiscientos", "setecientas": "setecientos",
	"ochocientas": "ochocientos", "novecientas": "novecientos",
}

func appendNumber(out []Unit, lang string, src int, w string) []Unit {
	wild := func() []Unit { return append(out, Unit{Src: src, Wildcard: true}) }
	spoken := func(s string) []Unit {
		for _, f := range strings.Fields(s) {
			out = append(out, Unit{Src: src, Norm: f})
		}
		return out
	}
	if lang == "es" {
		if !digitsOnly.MatchString(w) || !plainNumber(w, 9) {
			return wild()
		}
		n, _ := strconv.ParseUint(w, 10, 64)
		return spoken(spanishCardinal(n))
	}
	if m := englishOrdinal.FindStringSubmatch(w); m != nil {
		if !plainNumber(m[1], 6) {
			return wild()
		}
		n, _ := strconv.ParseUint(m[1], 10, 64)
		return spoken(englishOrdinalWords(n))
	}
	if !digitsOnly.MatchString(w) || !plainNumber(w, 9) {
		return wild()
	}
	n, _ := strconv.ParseUint(w, 10, 64)
	return spoken(englishCardinal(n))
}

// plainNumber reports whether digits can be read aloud as a quantity: no leading zero (an identifier), at
// most maxLen digits, and not exactly four digits (a year or a code; its reading depends on context).
func plainNumber(digits string, maxLen int) bool {
	if len(digits) > maxLen || len(digits) == 4 {
		return false
	}
	return len(digits) == 1 || digits[0] != '0'
}

var (
	enUnits = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten",
		"eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}
	enTens = []string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}
)

// englishCardinal reads n  in words: 121 is "one hundred
// twenty one".
func englishCardinal(n uint64) string {
	if n < 100 {
		if n < 20 {
			return enUnits[n]
		}
		if n%10 == 0 {
			return enTens[n/10]
		}
		return enTens[n/10] + " " + enUnits[n%10]
	}
	if n < 1000 {
		s := enUnits[n/100] + " hundred"
		if n%100 != 0 {
			s += " " + englishCardinal(n%100)
		}
		return s
	}
	for _, sc := range []struct {
		v    uint64
		name string
	}{{1e9, "billion"}, {1e6, "million"}, {1e3, "thousand"}} {
		if n >= sc.v {
			s := englishCardinal(n/sc.v) + " " + sc.name
			if n%sc.v != 0 {
				s += " " + englishCardinal(n%sc.v)
			}
			return s
		}
	}
	return ""
}

// englishOrdinalWords turns the last word of the cardinal into its ordinal: 3 is "third", 21 "twenty first".
func englishOrdinalWords(n uint64) string {
	words := strings.Fields(englishCardinal(n))
	last := words[len(words)-1]
	switch last {
	case "one":
		last = "first"
	case "two":
		last = "second"
	case "three":
		last = "third"
	case "five":
		last = "fifth"
	case "eight":
		last = "eighth"
	case "nine":
		last = "ninth"
	case "twelve":
		last = "twelfth"
	default:
		if strings.HasSuffix(last, "y") {
			last = strings.TrimSuffix(last, "y") + "ieth"
		} else {
			last += "th"
		}
	}
	words[len(words)-1] = last
	return strings.Join(words, " ")
}

var (
	esUnits = []string{"cero", "uno", "dos", "tres", "cuatro", "cinco", "seis", "siete", "ocho", "nueve", "diez",
		"once", "doce", "trece", "catorce", "quince", "dieciséis", "diecisiete", "dieciocho", "diecinueve", "veinte",
		"veintiuno", "veintidós", "veintitrés", "veinticuatro", "veinticinco", "veintiséis", "veintisiete",
		"veintiocho", "veintinueve"}
	esTens     = []string{"", "", "", "treinta", "cuarenta", "cincuenta", "sesenta", "setenta", "ochenta", "noventa"}
	esHundreds = []string{"", "ciento", "doscientos", "trescientos", "cuatrocientos", "quinientos", "seiscientos",
		"setecientos", "ochocientos", "novecientos"}
)

// spanishCardinal reads n (0 to 999 999 999) in words with the folds of spanishFold: 100 is "ciento", 21
// "veintiuno", 1 000 "mil", 2 000 000 "dos millón".
func spanishCardinal(n uint64) string {
	switch {
	case n < 30:
		return esUnits[n]
	case n < 100:
		if n%10 == 0 {
			return esTens[n/10]
		}
		return esTens[n/10] + " y " + esUnits[n%10]
	case n < 1000:
		s := esHundreds[n/100]
		if n%100 != 0 {
			s += " " + spanishCardinal(n%100)
		}
		return s
	case n < 1000000:
		s := "mil"
		if n/1000 != 1 {
			s = spanishCardinal(n/1000) + " mil"
		}
		if n%1000 != 0 {
			s += " " + spanishCardinal(n%1000)
		}
		return s
	}
	s := spanishCardinal(n/1000000) + " millón"
	if n%1000000 != 0 {
		s += " " + spanishCardinal(n%1000000)
	}
	return s
}
