package ebook

import (
	"bytes"
	"io"

	"golang.org/x/net/html/charset"
)

// chapterLevel picks the heading level that splits a document: the smallest level in [1,max] used at
// least twice, or 0 when none is (the document is then one chapter).
func chapterLevel(levels []int, max int) int {
	count := make([]int, 8)
	for _, l := range levels {
		if l >= 1 && l <= 7 {
			count[l]++
		}
	}
	for l := 1; l <= max; l++ {
		if count[l] >= 2 {
			return l
		}
	}
	return 0
}

// decodeHTML converts a standalone HTML file to UTF-8 using its declared or sniffed charset.
func decodeHTML(data []byte, lim Limits) ([]byte, error) {
	r, err := charset.NewReader(bytes.NewReader(data), "")
	if err != nil {
		return data, nil
	}
	out, err := io.ReadAll(io.LimitReader(r, lim.MaxEntryBytes+1))
	if err != nil {
		return nil, damaged("bad_charset")
	}
	if int64(len(out)) > lim.MaxEntryBytes {
		return nil, damaged("entry_too_big")
	}
	return out, nil
}

// parseHTMLFile reads a standalone HTML file. Chapters are cut at h1 when there are two or more,
// else at h2; otherwise the file is one chapter.
func parseHTMLFile(data []byte, lim Limits) (*Doc, error) {
	data, err := decodeHTML(data, lim)
	if err != nil {
		return nil, err
	}
	hd, err := walkHTML(data, "", lim)
	if err != nil {
		return nil, err
	}
	var levels []int
	for _, b := range hd.Blocks {
		if b.Kind == KindH {
			levels = append(levels, b.Level)
		}
	}
	split := chapterLevel(levels, 2)
	b := newBuilder("html", lim)
	if split == 0 {
		b.Chapter(hd.Title, "", nil)
	}
	for i, hb := range hd.Blocks {
		if split != 0 && hb.Kind == KindH && hb.Level == split {
			b.Chapter(joinSegs(hb.Segs), "", nil)
		}
		feed(b, &htmlDoc{Blocks: hd.Blocks[i : i+1]}, 0, func(string) string { return "" })
		if b.err != nil {
			return nil, b.err
		}
	}
	meta := Meta{Title: hd.Title}
	if a := normalizeString(hd.Meta["author"]); a != "" {
		meta.Authors = []string{a}
	}
	return b.Finish(meta, 0)
}
