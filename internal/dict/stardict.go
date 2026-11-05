package dict

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// StarDict, read from its published description (the "StarDictFileFormat" notes): a dictionary is a set of files
// with one base name. <name>.ifo says what it is; <name>.idx lists the headwords, each as the word, a NUL byte, the
// offset and the size of its definition in <name>.dict, big-endian, 32 bits or 64 (idxoffsetbits = 64); <name>.syn
// lists other spellings, each as a word, a NUL byte and the number of the headword it stands for.

// sdInfo is what the .ifo file says.
type sdInfo struct {
	name       string
	sameType   string
	idxBytes   int64 // idxfilesize; -1 when absent
	offsetBits int
}

// parseIfo reads the .ifo file: a first line naming the format, then key=value lines.
func parseIfo(data []byte) (sdInfo, error) {
	info := sdInfo{idxBytes: -1, offsetBits: 32}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimPrefix(lines[0], "\xef\xbb\xbf"), "StarDict's dict ifo file") {
		return info, errors.New("not a StarDict .ifo file")
	}
	for _, line := range lines[1:] {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "bookname":
			info.name = v
		case "sametypesequence":
			info.sameType = v
		case "idxfilesize":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return info, errors.New("bad idxfilesize")
			}
			info.idxBytes = n
		case "idxoffsetbits":
			if v != "32" && v != "64" {
				return info, errors.New("bad idxoffsetbits")
			}
			info.offsetBits = int(v[0]-'0')*10 + int(v[1]-'0')
		}
	}
	if info.name == "" {
		return info, errors.New("no bookname")
	}
	if utf8.RuneCountInString(info.name) > 100 {
		info.name = string([]rune(info.name)[:100])
	}
	return info, nil
}

// openRegular opens a file below dir the way routes_ebooks_download.go does: Lstat says it is a regular file, and
// the opened file must be the same one, so a link swapped in between is refused.
func openRegular(path string) (*os.File, os.FileInfo, error) {
	lfi, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !lfi.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(lfi, fi) {
		f.Close()
		return nil, nil, errors.New("not the file that was listed")
	}
	return f, fi, nil
}

// readLimited reads a whole regular file (or the unpacked form of a .gz one) of at most limit bytes.
func readLimited(path string, limit int64, gz bool) ([]byte, error) {
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var r io.Reader = f
	if !gz && fi.Size() > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	if gz {
		zr, err := gzip.NewReader(io.LimitReader(f, limit)) // the packed form is no bigger than its limit either
		if err != nil {
			return nil, err
		}
		r = zr
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return data, nil
}

// pick returns the first of the names that exists in the folder listing.
func pick(have map[string]bool, names ...string) string {
	for _, n := range names {
		if have[n] {
			return n
		}
	}
	return ""
}

// loadStarDict reads <base>.ifo and the files beside it.
func loadStarDict(dir, base string, have map[string]bool) (*dictionary, error) {
	ifo, err := readLimited(filepath.Join(dir, base+".ifo"), maxIfoBytes, false)
	if err != nil {
		return nil, err
	}
	info, err := parseIfo(ifo)
	if err != nil {
		return nil, err
	}
	idxName := pick(have, base+".idx", base+".idx.gz")
	dictName := pick(have, base+".dict", base+".dict.dz")
	if idxName == "" || dictName == "" {
		return nil, errors.New("the .idx or the .dict file is missing")
	}
	idx, err := readLimited(filepath.Join(dir, idxName), maxIndexBytes, strings.HasSuffix(idxName, ".gz"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", idxName, err)
	}
	if info.idxBytes >= 0 && info.idxBytes != int64(len(idx)) {
		return nil, fmt.Errorf("%s is %d bytes, the .ifo says %d", idxName, len(idx), info.idxBytes)
	}
	ctx, cancel := deadline()
	defer cancel()
	d := &dictionary{name: info.name}
	heads, err := parseIdx(ctx, idx, info.offsetBits, d)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", idxName, err)
	}
	if synName := pick(have, base+".syn"); synName != "" {
		if syn, err := readLimited(filepath.Join(dir, synName), maxSynBytes, false); err == nil {
			parseSyn(ctx, syn, heads, d) // a damaged .syn costs the synonyms, not the dictionary
		}
	}
	path := filepath.Join(dir, dictName)
	body, err := openBody(path, strings.HasSuffix(dictName, ".dz"))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", dictName, err)
	}
	d.read = func(src source) (string, error) {
		raw, err := body(src.off, src.size)
		if err != nil {
			return "", err
		}
		return decodeFields(raw, info.sameType), nil
	}
	return finish(d), nil
}

// parseIdx reads the headwords into d.index and returns them in file order (a .syn file counts them).
func parseIdx(ctx context.Context, idx []byte, bits int, d *dictionary) ([]source, error) {
	width := 4 + bits/8
	var heads []source
	for pos := 0; pos < len(idx); {
		if len(heads)&1023 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		nul := bytes.IndexByte(idx[pos:], 0)
		if nul < 0 || nul > maxHeadword || pos+nul+1+width > len(idx) {
			return nil, errors.New("truncated or damaged index")
		}
		word := string(idx[pos : pos+nul])
		rest := idx[pos+nul+1:]
		var src source
		if bits == 64 {
			src.off, src.size = binary.BigEndian.Uint64(rest), binary.BigEndian.Uint32(rest[8:])
		} else {
			src.off, src.size = uint64(binary.BigEndian.Uint32(rest)), binary.BigEndian.Uint32(rest[4:])
		}
		pos += nul + 1 + width
		if !utf8.ValidString(word) {
			heads = append(heads, source{}) // keeps the numbering of the .syn file
			continue
		}
		src.word, src.key = word, Fold(word)
		heads = append(heads, src)
		if src.key != "" {
			d.index = append(d.index, src)
		}
		if len(heads) > maxIndexEntries {
			return nil, errors.New("too many headwords")
		}
	}
	return heads, nil
}

// parseSyn adds each synonym as another key of the headword it names. A bad record ends the file.
func parseSyn(ctx context.Context, syn []byte, heads []source, d *dictionary) {
	for pos, n := 0, 0; pos < len(syn); n++ {
		if n&1023 == 0 && ctx.Err() != nil {
			return
		}
		nul := bytes.IndexByte(syn[pos:], 0)
		if nul < 0 || nul > maxHeadword || pos+nul+5 > len(syn) || len(d.index) > maxIndexEntries {
			return
		}
		word := string(syn[pos : pos+nul])
		at := binary.BigEndian.Uint32(syn[pos+nul+1:])
		pos += nul + 5
		if int64(at) >= int64(len(heads)) || heads[at].word == "" || !utf8.ValidString(word) {
			continue
		}
		if key := Fold(word); key != "" {
			target := heads[at]
			target.key = key // the headword stays what is shown
			d.index = append(d.index, target)
		}
	}
}

// openBody returns a function that reads size bytes at the offset of the (unpacked) .dict file, at most
// MaxEntryRead of them. Each call opens the file and closes it, so no handle outlives a request.
func openBody(path string, packed bool) (func(off uint64, size uint32) ([]byte, error), error) {
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if !packed {
		return func(off uint64, size uint32) ([]byte, error) {
			return readPlain(path, fi.Size(), off, size)
		}, nil
	}
	dz, err := parseDictzip(f, fi.Size())
	if err != nil {
		return nil, err
	}
	return func(off uint64, size uint32) ([]byte, error) { return dz.read(path, off, size) }, nil
}

// readPlain reads from an unpacked .dict. An entry that reaches past the end of the file is an error.
func readPlain(path string, fileSize int64, off uint64, size uint32) ([]byte, error) {
	n := min(int64(size), MaxEntryRead)
	if off > uint64(fileSize) || int64(off)+n > fileSize {
		return nil, errors.New("entry reaches past the end of the file")
	}
	f, _, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, int64(off)); err != nil {
		return nil, err
	}
	return buf, nil
}

// decodeFields turns a definition record into text. With sametypesequence every record has the same fields in
// that order, the last one running to the end; without it each field starts with its type byte. A lower-case
// type ends with a NUL byte, an upper-case type starts with a 4-byte size. Only m (meaning), t (phonetics) and
// l are text; h (html), g (pango) and x (xdxf) are stripped of their tags; every other field is skipped.
func decodeFields(raw []byte, sameType string) string {
	var parts []string
	for i := 0; len(raw) > 0; i++ {
		var kind byte
		if sameType != "" {
			if i >= len(sameType) {
				break
			}
			kind = sameType[i]
		} else {
			kind, raw = raw[0], raw[1:]
		}
		last := sameType != "" && i == len(sameType)-1
		var field []byte
		switch {
		case kind >= 'A' && kind <= 'Z':
			if last {
				field, raw = raw, nil
			} else {
				if len(raw) < 4 {
					return strings.Join(parts, "\n")
				}
				n := binary.BigEndian.Uint32(raw)
				if uint64(n) > uint64(len(raw)-4) {
					return strings.Join(parts, "\n")
				}
				field, raw = raw[4:4+n], raw[4+n:]
			}
		default:
			if nul := bytes.IndexByte(raw, 0); last || nul < 0 {
				field, raw = raw, nil
			} else {
				field, raw = raw[:nul], raw[nul+1:]
			}
		}
		switch kind {
		case 'm', 'l', 't':
			parts = append(parts, string(field))
		case 'h', 'g', 'x':
			parts = append(parts, stripMarkup(string(field)))
		}
	}
	return strings.Join(parts, "\n")
}
