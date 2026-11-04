package ebook

import (
	"archive/zip"
	"errors"
	"io"
	"strings"
)

// zipBox is a bounded, in-memory view of a zip file. Entry names are only ever looked up in the
// archive; they are never joined to a file system path (docs/plans/m5-r1-ebooks.md, invariant 3).
type zipBox struct {
	zr     *zip.Reader
	lim    Limits
	exact  map[string]*zip.File
	lower  map[string][]*zip.File
	budget int64 // bytes still allowed to be read in total
}

// openZip checks entry count, declared total, ratio and the encrypted flag before anything is read.
func openZip(ra io.ReaderAt, size int64, lim Limits) (*zipBox, error) {
	zr, err := zip.NewReader(ra, size)
	if err != nil && !(errors.Is(err, zip.ErrInsecurePath) && zr != nil) {
		return nil, damaged("bad_zip")
	}
	if len(zr.File) > lim.MaxEntries {
		return nil, damaged("zip_entries")
	}
	box := &zipBox{zr: zr, lim: lim, exact: map[string]*zip.File{}, lower: map[string][]*zip.File{}, budget: lim.MaxTotalUncompressed}
	var total uint64
	for _, f := range zr.File {
		if f.Flags&1 != 0 {
			return nil, refuse(ReasonDRM, "zip_encrypted")
		}
		if f.UncompressedSize64 > uint64(lim.MaxEntryBytes) {
			return nil, damaged("entry_too_big")
		}
		if f.UncompressedSize64 > RatioFloor && ratioOver(int64(f.UncompressedSize64), int64(f.CompressedSize64), lim.MaxRatio) {
			return nil, damaged("zip_bomb")
		}
		total += f.UncompressedSize64
		if total > uint64(lim.MaxTotalUncompressed) {
			return nil, damaged("zip_total")
		}
		if _, dup := box.exact[f.Name]; !dup {
			box.exact[f.Name] = f
		}
		l := strings.ToLower(f.Name)
		box.lower[l] = append(box.lower[l], f)
	}
	return box, nil
}

func ratioOver(uncompressed, compressed, max int64) bool {
	if compressed < 1 {
		compressed = 1
	}
	return uncompressed/compressed > max
}

// Has reports whether name resolves to an entry (exactly, or by one case-insensitive match).
func (z *zipBox) Has(name string) bool { return z.find(name) != nil }

// Names lists entry names in archive order, directories excluded.
func (z *zipBox) Names() []string {
	var out []string
	for _, f := range z.zr.File {
		if !strings.HasSuffix(f.Name, "/") {
			out = append(out, f.Name)
		}
	}
	return out
}

func (z *zipBox) find(name string) *zip.File {
	if f, ok := z.exact[name]; ok {
		return f
	}
	if m := z.lower[strings.ToLower(name)]; len(m) == 1 {
		return m[0]
	}
	return nil
}

// Read returns the bytes of an entry. It counts bytes actually read, not header sizes.
func (z *zipBox) Read(name string) ([]byte, error) {
	f := z.find(name)
	if f == nil {
		return nil, errNoEntry
	}
	rc, err := f.Open()
	if err != nil {
		return nil, damaged("bad_entry")
	}
	defer rc.Close()
	limit := z.lim.MaxEntryBytes
	byBudget := false
	if z.budget < limit {
		limit = z.budget
		byBudget = true
	}
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, damaged("bad_entry")
	}
	if int64(len(data)) > limit {
		if byBudget {
			return nil, damaged("zip_total")
		}
		return nil, damaged("entry_too_big")
	}
	z.budget -= int64(len(data))
	if int64(len(data)) > RatioFloor && ratioOver(int64(len(data)), int64(f.CompressedSize64), z.lim.MaxRatio) {
		return nil, damaged("zip_bomb")
	}
	return data, nil
}

var errNoEntry = errors.New("ebook: no such zip entry")
