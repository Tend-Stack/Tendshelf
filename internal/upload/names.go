package upload

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// maxNameBytes is the longest file or folder name Tendshelf creates. Most file systems allow 255
// bytes; staying well below leaves room for the " (99)" suffix and for backup tools.
const maxNameBytes = 120

var (
	reservedWindows = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true}
	sequenceRe      = regexp.MustCompile(`^(\d+)(\.\d+)?$`)
)

func isReserved(base string) bool {
	b := strings.ToUpper(strings.TrimRight(base, " "))
	if reservedWindows[b] {
		return true
	}
	if len(b) == 4 && (strings.HasPrefix(b, "COM") || strings.HasPrefix(b, "LPT")) && b[3] >= '1' && b[3] <= '9' {
		return true
	}
	return false
}

// SafeName turns text into one file or folder name that is safe on every common file system: no
// path separators, no control characters, no characters Windows forbids, no leading dot (so the
// scanner never skips it as hidden), no trailing dot or space, no reserved Windows device name,
// and at most 120 bytes. A name the scanner would treat specially (a junk folder, or one named
// like a disc folder, CD1) gets the prefix "Book ". An empty result becomes fallback.
func SafeName(s, fallback string) string { return safeName(s, fallback, true) }

// safeInner is SafeName for a name inside a book, where CD1 and Disc 2 must stay what they are.
func safeInner(s, fallback string) string { return safeName(s, fallback, false) }

func safeName(s, fallback string, discPrefix bool) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "_")
	}
	s = strings.ReplaceAll(s, ": ", " - ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsControl(r):
			// dropped
		case r == ':':
			b.WriteByte('-')
		case strings.ContainsRune(`/\*?"<>|`, r):
			b.WriteByte('_')
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	out = strings.TrimLeft(out, ". ")
	out = strings.TrimRight(out, ". ")
	if out == "" {
		out = fallback
	}
	// A reserved device name is reserved with or without an extension ("con.txt").
	base, rest := out, ""
	if i := strings.IndexByte(out, '.'); i >= 0 {
		base, rest = out[:i], out[i:]
	}
	if isReserved(base) {
		out = base + "_" + rest
	}
	if scan.SkipName(out) || (discPrefix && scan.IsDiscDir(out)) {
		out = "Book " + out
	}
	out = cutName(out)
	if out == "" {
		out = fallback
	}
	return out
}

// cutName shortens name to maxNameBytes on a rune boundary, keeping a short file extension.
func cutName(name string) string {
	if len(name) <= maxNameBytes {
		return name
	}
	ext := ""
	if i := strings.LastIndexByte(name, '.'); i > 0 && len(name)-i <= 8 && !strings.ContainsAny(name[i:], " ") {
		ext = name[i:]
	}
	stem := name[:len(name)-len(ext)]
	if limit := maxNameBytes - len(ext); len(stem) > limit {
		stem = stem[:limit]
		for !utf8.ValidString(stem) { // step back to a rune boundary
			stem = stem[:len(stem)-1]
		}
	}
	stem = strings.TrimRight(stem, ". ")
	return stem + ext
}

// within reports whether p is root or lies below it, after cleaning both. Every path the package
// creates or deletes is checked with it first.
func within(root, p string) bool {
	root, p = filepath.Clean(root), filepath.Clean(p)
	if !filepath.IsAbs(root) || !filepath.IsAbs(p) {
		return false
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

func underAny(p string, roots []string) bool {
	for _, r := range roots {
		if within(r, p) {
			return true
		}
	}
	return false
}

// seqPrefix formats a series number: integers get two digits, "1.5" becomes "01.5". It returns ""
// for anything else.
func seqPrefix(seq string) string {
	m := sequenceRe.FindStringSubmatch(strings.TrimSpace(seq))
	if m == nil {
		return ""
	}
	n := strings.TrimLeft(m[1], "0")
	if n == "" {
		n = "0"
	}
	if len(n) < 2 {
		n = "0" + n
	}
	return n + m[2]
}

// layout chooses where a new book goes below root: Author/Series/NN - Title, then Author/Title,
// then "Author - Title" at the top. A candidate is taken only when every directory of it that
// already exists is a real directory (not a link) that is not inside or equal to an existing book
// and holds no audio directly, so a new folder never turns a book into a container nor splits it.
// The last name gets " (2)" to " (99)" until it does not exist. It returns the parent directory
// (which may not exist yet) and the leaf name; ok is false when no candidate works.
func layout(lr *lroot, author, series, seq, title string, roots []string) (parent, name string, ok bool) {
	return layoutIn(lr, lr.path, author, series, seq, title, roots)
}

// layoutIn is layout starting at base, the top of the upload folder or a contributor's own folder inside it
// (decision 0029), instead of the upload folder itself.
func layoutIn(lr *lroot, base, author, series, seq, title string, roots []string) (parent, name string, ok bool) {
	root := base
	a := SafeName(author, metadata.UnknownAuthorFolder)
	t := SafeName(title, "Untitled book")
	type cand struct {
		dirs []string // directories below root, outermost first
		leaf string
	}
	var cands []cand
	if series != "" {
		leaf := t
		if p := seqPrefix(seq); p != "" {
			leaf = SafeName(p+" - "+title, t)
		}
		cands = append(cands, cand{[]string{a, SafeName(series, "Series")}, leaf})
	}
	cands = append(cands, cand{[]string{a}, t}, cand{nil, SafeName(author+" - "+title, a+" - "+t)})
	for _, c := range cands {
		dir := root
		good := true
		for _, d := range c.dirs {
			dir = filepath.Join(dir, d)
			if !usableAncestor(lr, dir, roots) {
				good = false
				break
			}
		}
		if !good {
			continue
		}
		for n := 1; n <= 99; n++ {
			leaf := c.leaf
			if n > 1 {
				leaf = suffixed(c.leaf, n)
			}
			if _, err := lr.Lstat(filepath.Join(dir, leaf)); err != nil && errorsIsNotExist(err) {
				return dir, leaf, true
			}
		}
	}
	return "", "", false
}

func errorsIsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// suffixed adds " (n)" to a folder name, within the length limit.
func suffixed(name string, n int) string {
	suf := fmt.Sprintf(" (%d)", n)
	if len(name)+len(suf) > maxNameBytes {
		name = cutName(name[:maxNameBytes-len(suf)])
	}
	return name + suf
}

// suffixedFile adds " (n)" before a file's extension.
func suffixedFile(name string, n int) string {
	ext := filepath.Ext(name)
	return suffixed(strings.TrimSuffix(name, ext), n) + ext
}

// usableAncestor reports whether the directory p may be reused or created as part of a new book's
// path. A path that does not exist is fine. One that exists must be a real directory, not a book
// or inside a book, and hold no audio file directly.
func usableAncestor(lr *lroot, p string, roots []string) bool {
	fi, err := lr.Lstat(p)
	if err != nil {
		return errorsIsNotExist(err)
	}
	if !fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0 {
		return false
	}
	if underAny(p, roots) {
		return false
	}
	entries, err := lr.ReadDir(p)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || scan.SkipName(e.Name()) {
			continue
		}
		if k := media.Classify(e.Name()); k == media.KindAudio || k == media.KindProtected {
			return false
		}
	}
	return true
}
