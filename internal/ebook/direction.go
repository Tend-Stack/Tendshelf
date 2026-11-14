package ebook

import (
	"strings"
	"unicode"
)

// Direction of the text (recipe v4). A left-to-right book carries no direction at all: Meta.Dir and
// Block.Dir are omitted, so its document is the same as before apart from the recipe string.

const (
	dirMinLetters = 200 // strong letters the text needs before it may decide the direction alone
	dirRatio      = 2   // one direction must hold this many times the other's letters
)

// isRTLLetter reports whether r is a letter of a right-to-left script.
func isRTLLetter(r rune) bool {
	return unicode.IsLetter(r) && unicode.In(r, unicode.Arabic, unicode.Hebrew, unicode.Syriac, unicode.Thaana,
		unicode.Nko, unicode.Samaritan, unicode.Mandaic, unicode.Adlam)
}

// letterDirs counts the letters of rs: nR right-to-left, nL every other letter.
func letterDirs(rs []rune) (nR, nL int) {
	for _, r := range rs {
		switch {
		case isRTLLetter(r):
			nR++
		case unicode.IsLetter(r):
			nL++
		}
	}
	return nR, nL
}

var rtlScripts = map[string]bool{"arab": true, "hebr": true, "syrc": true, "thaa": true, "nkoo": true,
	"adlm": true, "rohg": true, "samr": true, "mand": true}

var rtlLangs = map[string]bool{"ar": true, "arc": true, "azb": true, "bal": true, "bqi": true, "ckb": true,
	"dv": true, "fa": true, "glk": true, "he": true, "iw": true, "ji": true, "khw": true, "ks": true,
	"lrc": true, "mzn": true, "nqo": true, "prs": true, "ps": true, "sd": true, "syr": true, "ug": true,
	"ur": true, "yi": true}

// LangDir gives "rtl" for a language tag whose text runs right to left and "" otherwise. A script subtag
// (az-Arab, az-Latn) wins over the language.
func LangDir(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	parts := strings.FieldsFunc(tag, func(r rune) bool { return r == '-' || r == '_' })
	if len(parts) == 0 {
		return ""
	}
	for _, p := range parts[1:] {
		if len(p) == 4 {
			if rtlScripts[p] {
				return "rtl"
			}
			return ""
		}
	}
	if rtlLangs[parts[0]] {
		return "rtl"
	}
	return ""
}

// decideDir picks the book's direction ("rtl" or "ltr") in this order: the letters when decisive, the
// declared hint (spine or html dir), the language tag, whichever letters are more.
func decideDir(hint, lang string, nR, nL int) string {
	if nR+nL >= dirMinLetters {
		if nR >= dirRatio*nL && nR > 0 {
			return "rtl"
		}
		if nL >= dirRatio*nR && nL > 0 {
			return "ltr"
		}
	}
	if hint == "rtl" || hint == "ltr" {
		return hint
	}
	if LangDir(lang) == "rtl" {
		return "rtl"
	}
	if nR > nL {
		return "rtl"
	}
	return "ltr"
}

// blockDir is a block's own direction: the one holding 2:1 with at least one letter, else "".
func blockDir(nR, nL int) string {
	switch {
	case nR > 0 && nR >= dirRatio*nL:
		return "rtl"
	case nL > 0 && nL >= dirRatio*nR:
		return "ltr"
	}
	return ""
}

// Direction is the document's direction, "rtl" or "" (left to right). A document of recipe v1 to v3
// has no stored direction, so its language tag stands in until it is read again.
func Direction(d *Doc) string {
	if d == nil {
		return ""
	}
	if d.Meta.Dir == "rtl" {
		return "rtl"
	}
	if d.Recipe != Recipe {
		return LangDir(d.Meta.Language)
	}
	return ""
}
