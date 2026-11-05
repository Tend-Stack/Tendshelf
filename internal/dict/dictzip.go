package dict

import (
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
)

// dictzip, read from its published description: a gzip file whose header has an extra field "RA" listing the
// size of the text each compressed chunk unpacks to (chunkLen, the same for every chunk but the last) and the
// compressed size of every chunk. That lets one entry be read by unpacking only the chunks it touches, never
// the whole file. Each chunk is a raw deflate stream.

const (
	maxChunkLen  = 1 << 20 // the text one chunk may unpack to; real files use about 58,000
	gzipFixedLen = 10
)

// dictzip is the chunk table of one .dict.dz file.
type dictzip struct {
	chunkLen int
	start    int64   // where the first chunk begins in the file
	ends     []int64 // for chunk i, the offset in the file just after it
	size     int64   // the file's size
}

// parseDictzip reads the gzip header of f and its chunk table.
func parseDictzip(f io.ReaderAt, size int64) (*dictzip, error) {
	head := make([]byte, 12)
	if _, err := f.ReadAt(head, 0); err != nil {
		return nil, errors.New("not a dictzip file")
	}
	const fextra, fname, fcomment, fhcrc = 4, 8, 16, 2
	if head[0] != 0x1f || head[1] != 0x8b || head[2] != 8 || head[3]&fextra == 0 {
		return nil, errors.New("not a dictzip file")
	}
	flags := head[3]
	extraLen := int64(binary.LittleEndian.Uint16(head[10:]))
	extra := make([]byte, extraLen)
	if _, err := f.ReadAt(extra, gzipFixedLen+2); err != nil {
		return nil, errors.New("truncated dictzip header")
	}
	pos := int64(gzipFixedLen + 2 + extraLen)
	var table []byte
	var chunkLen, chunks int
	for e := extra; len(e) >= 4; {
		n := int(binary.LittleEndian.Uint16(e[2:]))
		if 4+n > len(e) {
			break
		}
		if e[0] == 'R' && e[1] == 'A' && n >= 6 && binary.LittleEndian.Uint16(e[4:]) == 1 {
			chunkLen = int(binary.LittleEndian.Uint16(e[6:]))
			chunks = int(binary.LittleEndian.Uint16(e[8:]))
			table = e[10 : 4+n]
		}
		e = e[4+n:]
	}
	if chunkLen < 1 || chunkLen > maxChunkLen || chunks < 1 || len(table) < 2*chunks {
		return nil, errors.New("no usable chunk table")
	}
	skipZero := func() error { // a NUL-terminated name or comment
		b := make([]byte, 1)
		for i := 0; i < 4096; i++ {
			if _, err := f.ReadAt(b, pos); err != nil {
				return errors.New("truncated dictzip header")
			}
			pos++
			if b[0] == 0 {
				return nil
			}
		}
		return errors.New("dictzip header name too long")
	}
	for _, flag := range []byte{fname, fcomment} {
		if flags&flag != 0 {
			if err := skipZero(); err != nil {
				return nil, err
			}
		}
	}
	if flags&fhcrc != 0 {
		pos += 2
	}
	dz := &dictzip{chunkLen: chunkLen, start: pos, size: size, ends: make([]int64, chunks)}
	at := pos
	for i := range dz.ends {
		at += int64(binary.LittleEndian.Uint16(table[2*i:]))
		if at > size {
			return nil, errors.New("chunk table reaches past the end of the file")
		}
		dz.ends[i] = at
	}
	return dz, nil
}

// read returns size bytes (at most MaxEntryRead) at off of the unpacked text.
func (dz *dictzip) read(path string, off uint64, size uint32) ([]byte, error) {
	n := int64(min(int64(size), MaxEntryRead))
	if n == 0 {
		return nil, nil
	}
	if off > 1<<40 {
		return nil, errors.New("entry reaches past the end of the file")
	}
	first := int64(off) / int64(dz.chunkLen)
	last := (int64(off) + n - 1) / int64(dz.chunkLen)
	if last >= int64(len(dz.ends)) {
		return nil, errors.New("entry reaches past the end of the file")
	}
	f, _, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var text []byte
	for c := first; c <= last; c++ {
		from := dz.start
		if c > 0 {
			from = dz.ends[c-1]
		}
		fr := flate.NewReader(io.NewSectionReader(f, from, dz.ends[c]-from))
		chunk, err := io.ReadAll(io.LimitReader(fr, int64(dz.chunkLen))) // a chunk never unpacks past its stated size
		fr.Close()
		if err != nil {
			return nil, err
		}
		if len(chunk) < dz.chunkLen && c != int64(len(dz.ends))-1 {
			return nil, errors.New("a chunk is shorter than the table says")
		}
		text = append(text, chunk...)
	}
	lo := int64(off) - first*int64(dz.chunkLen)
	if lo+n > int64(len(text)) {
		return nil, errors.New("entry reaches past the end of the text")
	}
	return text[lo : lo+n], nil
}
