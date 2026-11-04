package ebooktest

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/text/encoding/charmap"
)

// MOBIOptions describes a synthetic MOBI or AZW3 file. Markup is the book body markup (the writer
// wraps nothing around it); empty gives a small default.
type MOBIOptions struct {
	Title, Author, ISBN, ASIN, Lang string
	Markup                          string
	// Compression is 1 (none), 2 (PalmDOC, default) or 17480 (HUFF/CDIC; the records are not really
	// Huffman coded, the reader refuses on the header).
	Compression int
	// Encryption is the PalmDOC header's encryption type (0, 1 or 2).
	Encryption int
	// Version is 6 (default) or 8 (KF8).
	Version int
	// CP1252 writes the text as Windows-1252 (encoding 1252) instead of UTF-8 (65001).
	CP1252 bool
	// Trailing adds trailing entries to every text record (extra flags 0b11: one sized entry and the
	// multibyte overlap byte).
	Trailing bool
	// Combined writes a MOBI 6 part, a BOUNDARY record and a KF8 part (EXTH 121 points at the KF8 part).
	// KF8Markup is the KF8 part's text; Markup is the MOBI 6 part's.
	Combined  bool
	KF8Markup string
	// Hostile knobs.
	RecordOffsetPastEnd bool   // the last text record's offset lies beyond the file
	BadBackref          bool   // the first text record starts with a back reference to nothing
	DeclaredTextLength  uint32 // when not zero, replaces the true text length in the header
}

const defaultMOBIMarkup = `<html><head><title>Default</title></head><body><h1>Chapter One</h1><p>The lamp was lit.</p></body></html>`

// lz77 encodes b with the PalmDOC scheme: literals, runs of 1-8 raw bytes, back references and the
// space-plus-character byte. Deterministic.
func lz77(b []byte) []byte {
	var out []byte
	var run []byte
	flush := func() {
		for len(run) > 0 {
			n := len(run)
			if n > 8 {
				n = 8
			}
			out = append(out, byte(n))
			out = append(out, run[:n]...)
			run = run[n:]
		}
	}
	for i := 0; i < len(b); {
		// Back reference: the longest match of 3-10 bytes within the previous 2047.
		bestLen, bestDist := 0, 0
		for d := 1; d <= 2047 && d <= i; d++ {
			n := 0
			for n < 10 && i+n < len(b) && b[i+n-d] == b[i+n] {
				n++
			}
			if n > bestLen {
				bestLen, bestDist = n, d
			}
		}
		switch {
		case bestLen >= 3:
			flush()
			pair := 0x8000 | bestDist<<3 | (bestLen - 3)
			out = append(out, byte(pair>>8), byte(pair))
			i += bestLen
		case b[i] == ' ' && i+1 < len(b) && b[i+1] >= 0x40 && b[i+1] <= 0x7f:
			flush()
			out = append(out, b[i+1]^0x80)
			i += 2
		case b[i] == 0 || (b[i] >= 9 && b[i] <= 0x7f):
			flush()
			out = append(out, b[i])
			i++
		default:
			run = append(run, b[i])
			i++
			if len(run) == 8 {
				flush()
			}
		}
	}
	flush()
	return out
}

// backwardVarint encodes n as the trailing-entry size: the first byte has bit 7 set.
func backwardVarint(n int) []byte {
	var groups []byte
	for {
		groups = append([]byte{byte(n & 0x7f)}, groups...)
		n >>= 7
		if n == 0 {
			break
		}
	}
	groups[0] |= 0x80
	return groups
}

func be32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

func exth(records [][2]any) []byte {
	var body bytes.Buffer
	for _, r := range records {
		data := r[1].([]byte)
		body.Write(be32(uint32(r[0].(int))))
		body.Write(be32(uint32(8 + len(data))))
		body.Write(data)
	}
	n := len(records)
	pad := (4 - (12+body.Len())%4) % 4
	var out bytes.Buffer
	out.WriteString("EXTH")
	out.Write(be32(uint32(12 + body.Len() + pad)))
	out.Write(be32(uint32(n)))
	out.Write(body.Bytes())
	out.Write(make([]byte, pad))
	return out.Bytes()
}

type mobiPart struct {
	rec0    []byte
	records [][]byte
}

// mobiPartOf builds record 0 and the text records of one part.
func mobiPartOf(t testing.TB, o MOBIOptions, version int, markup string, boundary int) mobiPart {
	t.Helper()
	text := []byte(markup)
	enc := uint32(65001)
	if o.CP1252 {
		enc = 1252
		b, err := charmap.Windows1252.NewEncoder().Bytes(text)
		if err != nil {
			t.Fatalf("mobi cp1252: %v", err)
		}
		text = b
	}
	comp := o.Compression
	if comp == 0 {
		comp = 2
	}
	var recs [][]byte
	for off := 0; off < len(text) || len(recs) == 0; off += 4096 {
		end := off + 4096
		if end > len(text) {
			end = len(text)
		}
		chunk := text[off:end]
		var rec []byte
		if comp == 2 {
			rec = lz77(chunk)
		} else {
			rec = append([]byte(nil), chunk...)
		}
		if o.Trailing {
			// Bit 0 entry: one byte (value 0 means length 1). Bit 1 entry, outermost: two payload
			// bytes and a backward varint giving the whole size, 3.
			rec = append(rec, 0x00)
			rec = append(rec, 'a', 'b')
			rec = append(rec, backwardVarint(3)...)
		}
		recs = append(recs, rec)
	}
	if o.BadBackref && len(recs) > 0 {
		recs[0] = append([]byte{0x80, 0x08}, recs[0]...)
	}
	extra := uint32(0)
	if o.Trailing {
		extra = 3
	}

	title := o.Title
	var ex [][2]any
	if o.Author != "" {
		ex = append(ex, [2]any{100, []byte(o.Author)})
	}
	if o.ISBN != "" {
		ex = append(ex, [2]any{104, []byte(o.ISBN)})
	}
	if o.ASIN != "" {
		ex = append(ex, [2]any{113, []byte(o.ASIN)})
	}
	if title != "" {
		ex = append(ex, [2]any{503, []byte(title)})
	}
	if o.Lang != "" {
		ex = append(ex, [2]any{524, []byte(o.Lang)})
	}
	if boundary > 0 {
		ex = append(ex, [2]any{121, be32(uint32(boundary))})
	}
	exthBytes := exth(ex)

	const hl = 232
	r0 := make([]byte, 16+hl)
	comp16 := uint16(comp)
	binary.BigEndian.PutUint16(r0[0:], comp16)
	tl := uint32(len(text))
	if o.DeclaredTextLength != 0 {
		tl = o.DeclaredTextLength
	}
	binary.BigEndian.PutUint32(r0[4:], tl)
	binary.BigEndian.PutUint16(r0[8:], uint16(len(recs)))
	binary.BigEndian.PutUint16(r0[10:], 4096)
	binary.BigEndian.PutUint16(r0[12:], uint16(o.Encryption))
	copy(r0[16:], "MOBI")
	binary.BigEndian.PutUint32(r0[20:], hl)
	binary.BigEndian.PutUint32(r0[24:], 2)
	binary.BigEndian.PutUint32(r0[28:], enc)
	binary.BigEndian.PutUint32(r0[36:], uint32(version))
	binary.BigEndian.PutUint32(r0[128:], 0x40)
	binary.BigEndian.PutUint32(r0[168:], 0xffffffff)
	binary.BigEndian.PutUint32(r0[240:], extra)
	// Full name after the EXTH block.
	nameOff := len(r0) + len(exthBytes)
	binary.BigEndian.PutUint32(r0[84:], uint32(nameOff))
	binary.BigEndian.PutUint32(r0[88:], uint32(len(title)))
	r0 = append(r0, exthBytes...)
	r0 = append(r0, title...)
	r0 = append(r0, make([]byte, 4)...)
	return mobiPart{rec0: r0, records: recs}
}

// MOBI builds the file bytes.
func MOBI(t testing.TB, o MOBIOptions) []byte {
	t.Helper()
	markup := o.Markup
	if markup == "" {
		markup = defaultMOBIMarkup
	}
	version := o.Version
	if version == 0 {
		version = 6
	}
	var parts []mobiPart
	if o.Combined {
		kf8 := o.KF8Markup
		if kf8 == "" {
			kf8 = markup
		}
		p6 := mobiPartOf(t, o, 6, markup, 0)
		// Records so far: p6 record 0 + its text records + BOUNDARY; the KF8 part starts right after.
		kfIndex := 1 + len(p6.records) + 1
		p6 = mobiPartOf(t, o, 6, markup, kfIndex)
		p8 := mobiPartOf(t, o, 8, kf8, 0)
		parts = []mobiPart{p6, {records: [][]byte{[]byte("BOUNDARY")}}, p8}
	} else {
		parts = []mobiPart{mobiPartOf(t, o, version, markup, 0)}
	}
	var recs [][]byte
	for _, p := range parts {
		if p.rec0 != nil {
			recs = append(recs, p.rec0)
		}
		recs = append(recs, p.records...)
	}
	n := len(recs)
	head := make([]byte, 78+8*n+2)
	name := o.Title
	if len(name) > 31 {
		name = name[:31]
	}
	copy(head[0:], name)
	copy(head[60:], "BOOKMOBI")
	binary.BigEndian.PutUint16(head[76:], uint16(n))
	off := len(head)
	for i, r := range recs {
		recOff := off
		if o.RecordOffsetPastEnd && i == n-1 {
			recOff = 1 << 28
		}
		binary.BigEndian.PutUint32(head[78+8*i:], uint32(recOff))
		head[78+8*i+5] = byte(i >> 16)
		head[78+8*i+6] = byte(i >> 8)
		head[78+8*i+7] = byte(i)
		off += len(r)
	}
	var out bytes.Buffer
	out.Write(head)
	for _, r := range recs {
		out.Write(r)
	}
	return out.Bytes()
}
