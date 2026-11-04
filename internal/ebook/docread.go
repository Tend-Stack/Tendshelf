package ebook

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Errors from ReadDoc.
var (
	// ErrDocTooLarge: the document is larger than maxDocBytes once uncompressed.
	ErrDocTooLarge = errors.New("ebook: document is too large")
	// ErrDocCorrupt: the stored file is not a valid document with the address it is filed under.
	ErrDocCorrupt = errors.New("ebook: document is corrupt")
)

// maxDocBytes caps the uncompressed size ReadDoc accepts. A variable so a test can lower it.
var maxDocBytes int64 = 512 << 20

// ReadDoc loads the document filed under sha in dir. It decompresses at most maxDocBytes, decodes the
// JSON, encodes it again and requires the same SHA-256, so a file edited or damaged on disk is refused.
func ReadDoc(dir, sha string) (*Doc, error) {
	f, err := os.Open(DocPath(dir, sha))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDocCorrupt, err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, maxDocBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDocCorrupt, err)
	}
	if int64(len(raw)) > maxDocBytes {
		return nil, ErrDocTooLarge
	}
	var d Doc
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDocCorrupt, err)
	}
	again, err := Encode(&d)
	if err != nil || DocSHA(again) != sha {
		return nil, ErrDocCorrupt
	}
	return &d, nil
}

// WordSpan is one word of a block as text. Sentence is the index into Block.Sentences whose [start,end)
// holds the word's start (-1 if none). DotAfter says the next code unit is a full stop, so "Mr." can be
// told from "Mr".
type WordSpan struct {
	Text     string
	Sentence int
	DotAfter bool
}

// WordSpans returns the words of b in order. Offsets in a block are UTF-16 code units.
func WordSpans(b Block) []WordSpan {
	units := utf16.Encode([]rune(b.Text))
	out := make([]WordSpan, 0, len(b.Words))
	for _, w := range b.Words {
		s, e := w[0], w[1]
		if s < 0 || e > len(units) || s > e {
			continue
		}
		sp := WordSpan{Text: string(utf16.Decode(units[s:e])), Sentence: -1}
		for k, se := range b.Sentences {
			if s >= se[0] && s < se[1] {
				sp.Sentence = k
				break
			}
		}
		sp.DotAfter = e < len(units) && units[e] == '.'
		out = append(out, sp)
	}
	return out
}

// SentenceID names sentence k of a block: the block id, ".s", the index.
func SentenceID(blockID string, k int) string { return blockID + ".s" + strconv.Itoa(k) }

// ParseSID splits a sentence id "c<chapter>.p<block>.s<sentence>" (see SentenceID) into its numbers. Chapters and
// blocks count from 1, sentences from 0; at most 5, 6 and 5 digits, no leading zeros.
func ParseSID(sid string) (ch, p, s int, ok bool) {
	parts := strings.Split(sid, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var oks [3]bool
	ch, oks[0] = sidNumber(parts[0], 'c', 5, false)
	p, oks[1] = sidNumber(parts[1], 'p', 6, false)
	s, oks[2] = sidNumber(parts[2], 's', 5, true)
	if !oks[0] || !oks[1] || !oks[2] {
		return 0, 0, 0, false
	}
	return ch, p, s, true
}

// sidNumber reads prefix then 1..maxDigits ASCII digits without a leading zero (a lone "0" only when zeroOK).
func sidNumber(part string, prefix byte, maxDigits int, zeroOK bool) (int, bool) {
	if len(part) < 2 || part[0] != prefix || len(part)-1 > maxDigits {
		return 0, false
	}
	digits := part[1:]
	n := 0
	for i := 0; i < len(digits); i++ {
		c := digits[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	if digits[0] == '0' && !(zeroOK && len(digits) == 1) {
		return 0, false
	}
	return n, true
}

// CompareSID orders two sentence ids in document order (chapter, block, sentence): -1, 0 or 1. An id that does not
// parse sorts before every valid one, and two that do not parse compare as strings.
func CompareSID(a, b string) int {
	ac, ap, asn, aok := ParseSID(a)
	bc, bp, bsn, bok := ParseSID(b)
	if !aok || !bok {
		if aok == bok {
			return strings.Compare(a, b)
		}
		if !aok {
			return -1
		}
		return 1
	}
	for _, d := range [3]int{ac - bc, ap - bp, asn - bsn} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}
