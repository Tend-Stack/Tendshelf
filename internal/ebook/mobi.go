package ebook

import (
	"encoding/binary"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// MOBI and AZW3 (KF8): a PalmDB container whose record 0 holds the PalmDOC and MOBI headers and whose
// next records hold the markup, PalmDOC-compressed or plain. Only DRM-free files are read. The record
// layout is documented at https://wiki.mobileread.com/wiki/MOBI (read for understanding, no code taken).
// The text of rawML is used in order; the KF8 skeleton and fragment tables are not rebuilt.

const (
	palmHeaderLen   = 78
	mobiCompNone    = 1
	mobiCompPalmDoc = 2
	mobiCompHuff    = 17480
	mobiMaxRecords  = 1 << 16
)

var pageBreakRE = regexp.MustCompile(`(?i)<mbp:pagebreak[^>]*>`)

// palmDB is the record table of a PalmDB file.
type palmDB struct {
	ra      io.ReaderAt
	size    int64
	offsets []int64 // start of each record; record i ends at offsets[i+1] or at size
}

func openPalmDB(ra io.ReaderAt, size int64) (*palmDB, error) {
	if size < palmHeaderLen+2 {
		return nil, damaged("mobi_header")
	}
	head := make([]byte, palmHeaderLen)
	if _, err := ra.ReadAt(head, 0); err != nil {
		return nil, damaged("mobi_header")
	}
	n := int(binary.BigEndian.Uint16(head[76:78]))
	if n < 1 || int64(palmHeaderLen+8*n) > size {
		return nil, damaged("mobi_records")
	}
	tbl := make([]byte, 8*n)
	if _, err := ra.ReadAt(tbl, palmHeaderLen); err != nil {
		return nil, damaged("mobi_records")
	}
	db := &palmDB{ra: ra, size: size, offsets: make([]int64, n)}
	prev := int64(palmHeaderLen + 8*n)
	for i := 0; i < n; i++ {
		off := int64(binary.BigEndian.Uint32(tbl[8*i : 8*i+4]))
		if off < prev || off > size {
			return nil, damaged("mobi_records")
		}
		db.offsets[i] = off
		prev = off
	}
	return db, nil
}

func (db *palmDB) count() int { return len(db.offsets) }

// record returns record i, at most max bytes long.
func (db *palmDB) record(i int, max int64) ([]byte, error) {
	if i < 0 || i >= len(db.offsets) {
		return nil, damaged("mobi_records")
	}
	end := db.size
	if i+1 < len(db.offsets) {
		end = db.offsets[i+1]
	}
	n := end - db.offsets[i]
	if n < 0 || n > max {
		return nil, damaged("mobi_records")
	}
	buf := make([]byte, n)
	if _, err := db.ra.ReadAt(buf, db.offsets[i]); err != nil && int64(len(buf)) > 0 {
		return nil, damaged("mobi_records")
	}
	return buf, nil
}

// mobiHead is what a record 0 says about its part.
type mobiHead struct {
	compression int
	textLength  uint32
	textRecords int
	encryption  int
	encoding    uint32
	extraFlags  uint32
	fullName    string
	exth        map[uint32][][]byte
	exthOrder   []uint32
}

func u16(b []byte, off int) (int, bool) {
	if off < 0 || off+2 > len(b) {
		return 0, false
	}
	return int(binary.BigEndian.Uint16(b[off:])), true
}

func u32(b []byte, off int) (uint32, bool) {
	if off < 0 || off+4 > len(b) {
		return 0, false
	}
	return binary.BigEndian.Uint32(b[off:]), true
}

// decodeMobiText converts bytes in the file's encoding (65001 UTF-8, else cp1252) to UTF-8.
func decodeMobiText(b []byte, encoding uint32) string {
	if encoding == 65001 {
		return strings.ToValidUTF8(string(b), "�")
	}
	out, err := charmap.Windows1252.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}

func parseMobiHead(r0 []byte) (*mobiHead, error) {
	h := &mobiHead{exth: map[uint32][][]byte{}}
	comp, ok1 := u16(r0, 0)
	tl, ok2 := u32(r0, 4)
	rc, ok3 := u16(r0, 8)
	enc, ok4 := u16(r0, 12)
	if !ok1 || !ok2 || !ok3 || !ok4 || len(r0) < 24 || string(r0[16:20]) != "MOBI" {
		return nil, damaged("mobi_header")
	}
	h.compression, h.textLength, h.textRecords, h.encryption = comp, tl, rc, enc
	hl, _ := u32(r0, 20)
	if hl < 24 || int64(hl) > int64(len(r0))-16 {
		return nil, damaged("mobi_header")
	}
	h.encoding, _ = u32(r0, 28)
	if hl >= 228 {
		h.extraFlags, _ = u32(r0, 240)
	}
	// Full name.
	if no, ok := u32(r0, 84); ok {
		if nl, ok := u32(r0, 88); ok && nl > 0 && int64(no)+int64(nl) <= int64(len(r0)) {
			h.fullName = decodeMobiText(r0[no:no+nl], h.encoding)
		}
	}
	// EXTH.
	flags, _ := u32(r0, 128)
	start := 16 + int(hl)
	if flags&0x40 != 0 && start+12 <= len(r0) && string(r0[start:start+4]) == "EXTH" {
		cnt, _ := u32(r0, start+8)
		pos := start + 12
		for i := uint32(0); i < cnt && i < 1024; i++ {
			t, ok1 := u32(r0, pos)
			l, ok2 := u32(r0, pos+4)
			if !ok1 || !ok2 || l < 8 || pos+int(l) > len(r0) {
				break
			}
			if _, seen := h.exth[t]; !seen {
				h.exthOrder = append(h.exthOrder, t)
			}
			h.exth[t] = append(h.exth[t], r0[pos+8:pos+int(l)])
			pos += int(l)
		}
	}
	return h, nil
}

// checkMobiDRM refuses encrypted parts before any text is decoded.
func checkMobiDRM(h *mobiHead) error {
	if h.encryption == 1 || h.encryption == 2 {
		return refuse(ReasonDRM, "mobi_encrypted")
	}
	if h.encryption != 0 {
		return damaged("mobi_header")
	}
	return nil
}

// trailingSize is the number of bytes of trailing entries at the end of a text record.
func trailingSize(rec []byte, flags uint32) (int, bool) {
	num := 0
	for bit := 1; bit < 16; bit++ {
		if flags&(1<<bit) == 0 {
			continue
		}
		// A backward varint: read from the end; the byte with bit 7 set is the first byte of the number.
		size, shift := 0, 0
		pos := len(rec) - num
		for {
			if pos <= 0 {
				return 0, false
			}
			pos--
			v := int(rec[pos])
			size |= (v & 0x7f) << shift
			shift += 7
			if v&0x80 != 0 || shift >= 28 {
				break
			}
		}
		if size <= 0 {
			return 0, false
		}
		num += size
	}
	if flags&1 != 0 {
		pos := len(rec) - num - 1
		if pos < 0 {
			return 0, false
		}
		num += int(rec[pos]&3) + 1
	}
	if num > len(rec) {
		return 0, false
	}
	return num, true
}

// palmDocDecode expands one PalmDOC LZ77 record. It never writes beyond max bytes.
func palmDocDecode(in []byte, max int) ([]byte, error) {
	out := make([]byte, 0, len(in)*2)
	for i := 0; i < len(in); {
		c := in[i]
		i++
		switch {
		case c == 0 || (c >= 9 && c <= 0x7f):
			out = append(out, c)
		case c <= 8:
			if i+int(c) > len(in) {
				return nil, damaged("mobi_lz77")
			}
			out = append(out, in[i:i+int(c)]...)
			i += int(c)
		case c >= 0xc0:
			out = append(out, ' ', c^0x80)
		default:
			if i >= len(in) {
				return nil, damaged("mobi_lz77")
			}
			pair := int(c)<<8 | int(in[i])
			i++
			dist, n := (pair>>3)&0x7ff, (pair&7)+3
			if dist == 0 || dist > len(out) {
				return nil, damaged("mobi_lz77")
			}
			for k := 0; k < n; k++ {
				out = append(out, out[len(out)-dist])
			}
		}
		if len(out) > max {
			return nil, damaged("mobi_text_length")
		}
	}
	return out, nil
}

// mobiRawML reads and expands the text records of the part whose record 0 is base.
func mobiRawML(db *palmDB, base int, h *mobiHead, lim Limits) ([]byte, error) {
	switch h.compression {
	case mobiCompNone, mobiCompPalmDoc:
	case mobiCompHuff:
		return nil, refuse(ReasonUnsupported, "huffcdic")
	default:
		return nil, refuse(ReasonUnsupported, "mobi_compression")
	}
	if int64(h.textLength) > lim.MaxEntryBytes {
		return nil, damaged("entry_too_big")
	}
	if h.textRecords < 1 || base+h.textRecords >= db.count() {
		return nil, damaged("mobi_records")
	}
	want := int(h.textLength)
	out := make([]byte, 0, want)
	for i := 1; i <= h.textRecords; i++ {
		rec, err := db.record(base+i, 1<<20)
		if err != nil {
			return nil, err
		}
		cut, ok := trailingSize(rec, h.extraFlags)
		if !ok {
			return nil, damaged("mobi_trailing")
		}
		rec = rec[:len(rec)-cut]
		remain := want - len(out)
		if h.compression == mobiCompNone {
			if len(rec) > remain {
				return nil, damaged("mobi_text_length")
			}
			out = append(out, rec...)
			continue
		}
		dec, err := palmDocDecode(rec, remain)
		if err != nil {
			return nil, err
		}
		out = append(out, dec...)
	}
	if len(out) < want/2 {
		return nil, damaged("mobi_text_length")
	}
	return out, nil
}

// partStart finds the record index where the KF8 part begins (EXTH 121), or 0.
func partStart(db *palmDB, h *mobiHead) int {
	vs := h.exth[121]
	if len(vs) == 0 || len(vs[0]) != 4 {
		return 0
	}
	v := int(binary.BigEndian.Uint32(vs[0]))
	if v <= 0 || v >= db.count() {
		return 0
	}
	if rec, err := db.record(v, 1<<20); err != nil || len(rec) < 20 || string(rec[16:20]) != "MOBI" {
		return 0
	}
	return v
}

func exthString(h *mobiHead, t uint32) string {
	if vs := h.exth[t]; len(vs) > 0 {
		return normalizeString(decodeMobiText(vs[0], h.encoding))
	}
	return ""
}

func parseMOBI(ra io.ReaderAt, size int64, format string, lim Limits) (*Doc, error) {
	db, err := openPalmDB(ra, size)
	if err != nil {
		return nil, err
	}
	r0, err := db.record(0, 1<<20)
	if err != nil {
		return nil, err
	}
	h0, err := parseMobiHead(r0)
	if err != nil {
		return nil, err
	}
	if err := checkMobiDRM(h0); err != nil {
		return nil, err
	}
	base, h := 0, h0
	if kf8 := partStart(db, h0); kf8 > 0 {
		rec, err := db.record(kf8, 1<<20)
		if err != nil {
			return nil, err
		}
		hk, err := parseMobiHead(rec)
		if err != nil {
			return nil, err
		}
		if err := checkMobiDRM(hk); err != nil {
			return nil, err
		}
		base, h = kf8, hk
	}
	raw, err := mobiRawML(db, base, h, lim)
	if err != nil {
		return nil, err
	}
	markup := []byte(decodeMobiText(raw, h.encoding))

	b := newBuilder(format, lim)
	parts := pageBreakRE.Split(string(markup), -1)
	var docs []*htmlDoc
	var levels []int
	for k, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		hd, err := walkHTML([]byte(p), fmt.Sprintf("t%d", k), lim)
		if err != nil {
			return nil, err
		}
		docs = append(docs, hd)
		for _, hb := range hd.Blocks {
			if hb.Kind == KindH {
				levels = append(levels, hb.Level)
			}
		}
	}
	split := chapterLevel(levels, 2)
	multi := len(docs) > 1
	title := ""
	for di, hd := range docs {
		for i, hb := range hd.Blocks {
			first := i == 0
			switch {
			case first && (multi || (split != 0 && hb.Kind == KindH && hb.Level == split)):
				t := ""
				if hb.Kind == KindH {
					t = joinSegs(hb.Segs)
				}
				b.Chapter(t, hb.Src.Part, nil)
			case split != 0 && hb.Kind == KindH && hb.Level == split:
				b.Chapter(joinSegs(hb.Segs), hb.Src.Part, nil)
			}
			if title == "" && hb.Kind == KindH && hb.Level == 1 && di == 0 {
				title = joinSegs(hb.Segs)
			}
			feed(b, &htmlDoc{Blocks: hd.Blocks[i : i+1]}, 0, func(string) string { return "" })
			if b.err != nil {
				return nil, b.err
			}
		}
	}
	meta := Meta{Title: firstNonEmpty(exthString(h, 503), exthString(h0, 503), normalizeString(h.fullName), normalizeString(h0.fullName), title)}
	for _, a := range h.exth[100] {
		if s := normalizeString(decodeMobiText(a, h.encoding)); s != "" {
			meta.Authors = append(meta.Authors, s)
		}
	}
	meta.Identifiers.ISBN = exthString(h, 104)
	meta.Identifiers.ASIN = exthString(h, 113)
	meta.Language = exthString(h, 524)
	return b.Finish(meta, 0)
}
