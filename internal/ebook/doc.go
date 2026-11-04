package ebook

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Recipe names the parsing rules that made a document; a change to any rule bumps it so editions are
// read again. Segmenter names the sentence and word rules.
const (
	Recipe    = "tendshelf-ebook-v2"
	Segmenter = "tendshelf-segment-v1"
	DocV      = 1
)

// Doc is the canonical document "tendshelf-doc-v1". Field order is the JSON order.
type Doc struct {
	V            int       `json:"v"`
	Recipe       string    `json:"recipe"`
	Format       string    `json:"format"`
	SourceSHA256 string    `json:"sourceSha256"`
	Layout       string    `json:"layout"`
	Meta         Meta      `json:"meta"`
	PageCount    int       `json:"pageCount"`
	Chapters     []Chapter `json:"chapters"`
}

// Meta is what the file says about itself.
type Meta struct {
	Title       string      `json:"title"`
	Subtitle    string      `json:"subtitle"`
	Authors     []string    `json:"authors"`
	Language    string      `json:"language"`
	Identifiers Identifiers `json:"identifiers"`

	// Added by recipe v2; all omitted when empty, so a v1 document still loads and hashes.
	Description string `json:"description,omitempty"`
	Publisher   string `json:"publisher,omitempty"`
	Series      string `json:"series,omitempty"`
	SeriesIndex string `json:"seriesIndex,omitempty"`
	Year        int    `json:"year,omitempty"`
	CoverPart   string `json:"coverPart,omitempty"` // zip path of the cover picture (EPUB)
}

// Identifiers are the book numbers found in the file.
type Identifiers struct {
	ISBN string `json:"isbn,omitempty"`
	ASIN string `json:"asin,omitempty"`
}

// Chapter is a run of blocks. Idx is 1-based.
type Chapter struct {
	Idx    int     `json:"idx"`
	Title  string  `json:"title"`
	Front  bool    `json:"front"`
	Src    string  `json:"src"`
	Blocks []Block `json:"blocks"`
}

// Block kinds.
const (
	KindP     = "p"
	KindH     = "h"
	KindQuote = "quote"
	KindLI    = "li"
	KindImg   = "img"
	KindNote  = "note"
	KindPre   = "pre"
	KindHR    = "hr"
)

// Block is one paragraph-sized piece of text. All offsets are UTF-16 units in Text, half-open.
type Block struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Level     int      `json:"level,omitempty"`
	Text      string   `json:"text"`
	Img       string   `json:"img,omitempty"`
	Runs      []Run    `json:"runs"`
	Sentences [][2]int `json:"sentences"`
	Words     [][2]int `json:"words"`
	Src       *SrcRef  `json:"src,omitempty"`
	Links     []Link   `json:"links,omitempty"`
	ImgW      int      `json:"imgW,omitempty"`
	ImgH      int      `json:"imgH,omitempty"`

	// anchors are the source ids (part#id) that point into this block; only Finish reads them.
	anchors []string
}

// Link is a clickable span of a block's text, UTF-16 units, half-open. To is the ID of the block it
// goes to inside the book; URL is an outside address (http, https or mailto). Note marks a link to a
// footnote, which the reader opens as a popup.
type Link struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	To    string `json:"to,omitempty"`
	URL   string `json:"url,omitempty"`
	Note  bool   `json:"note,omitempty"`
}

// Run is a styled span: style is em, strong or code.
type Run struct {
	Start, End int
	Style      string
}

// MarshalJSON writes [start,end,"style"].
func (r Run) MarshalJSON() ([]byte, error) {
	return json.Marshal([]any{r.Start, r.End, r.Style})
}

// UnmarshalJSON reads [start,end,"style"].
func (r *Run) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if err := json.Unmarshal(b, &a); err != nil || len(a) != 3 {
		return errors.New("ebook: bad run")
	}
	if err := json.Unmarshal(a[0], &r.Start); err != nil {
		return err
	}
	if err := json.Unmarshal(a[1], &r.End); err != nil {
		return err
	}
	return json.Unmarshal(a[2], &r.Style)
}

// SrcRef points a block back at the source: the part (zip entry), the byte range of its element and a
// map of [canonical UTF-16 offset, source byte offset] pairs at each text token.
type SrcRef struct {
	Part  string   `json:"part"`
	Start int      `json:"start"`
	End   int      `json:"end"`
	Map   [][2]int `json:"map"`
}

// Encode returns the canonical JSON of d: struct field order, no HTML escaping, no indent, no
// trailing newline.
func Encode(d *Doc) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// DocSHA is the content address of an encoded document.
func DocSHA(jsonBytes []byte) string {
	sum := sha256.Sum256(jsonBytes)
	return hex.EncodeToString(sum[:])
}

// DocPath is where a document lives under dir.
func DocPath(dir, sha string) string { return filepath.Join(dir, sha+".json.gz") }

// WriteDoc stores the encoded document as dir/<sha>.json.gz (gzip with an empty header, best
// compression) and returns the SHA-256 of the uncompressed JSON. It writes a temporary file in dir,
// syncs it and renames it; a file that already has the name is kept as it is.
func WriteDoc(dir string, jsonBytes []byte) (string, error) {
	sha := DocSHA(jsonBytes)
	final := DocPath(dir, sha)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if _, err := os.Stat(final); err == nil {
		return sha, nil
	}
	tmp, err := os.CreateTemp(dir, ".doc-*.tmp")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	zw, err := gzip.NewWriterLevel(tmp, gzip.BestCompression)
	if err != nil {
		tmp.Close()
		cleanup()
		return "", err
	}
	if _, err = zw.Write(jsonBytes); err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(name, 0o600)
	}
	if err == nil {
		err = os.Rename(name, final)
	}
	if err != nil {
		cleanup()
		return "", fmt.Errorf("ebook: write document: %w", err)
	}
	return sha, nil
}
