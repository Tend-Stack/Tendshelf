package ebook

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
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
