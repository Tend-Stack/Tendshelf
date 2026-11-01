// Port of Audiobookshelf server/scanner/AudioFileScanner.js (runSmartTrackOrder,
// getTrackAndDiscNumberFromFilename) and TendVox Extractor extractor_details.py (track_numbers,
// order_parts, natural_key); see NOTICES.md.

package scan

import (
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var (
	discInNameRe = regexp.MustCompile(`(?i)\b(disc|cd) ?(\d\d?)\b`)
	trackNumRe   = regexp.MustCompile(`\d{1,4}`)
)

// trackNumbers reads the disc and track number a file name carries. The title is removed once from
// the name first, so "Book 3 - 04" in a book called "Book 3" is track 4. A disc folder name (CD1,
// Disc 02) overrides a disc number found in the file name.
func trackNumbers(name, parentDir, title string) (disc, track int, okDisc, okTrack bool) {
	base := stem(name)
	if title != "" {
		base = strings.Replace(base, title, "", 1)
	}
	if m := discInNameRe.FindStringSubmatchIndex(base); m != nil {
		if n, err := strconv.Atoi(base[m[4]:m[5]]); err == nil {
			disc, okDisc = n, true
		}
		base = base[:m[0]] + base[m[1]:]
	}
	if dir := filepath.Base(parentDir); parentDir != "" && discDirRe.MatchString(dir) {
		digits := strings.TrimLeftFunc(dir, func(r rune) bool { return !unicode.IsDigit(r) })
		if n, err := strconv.Atoi(digits); err == nil {
			disc, okDisc = n, true
		}
	}
	if m := trackNumRe.FindString(base); m != "" {
		if n, err := strconv.Atoi(m); err == nil {
			track, okTrack = n, true
		}
	}
	return
}

// naturalCmp compares two strings the way a person sorts file names: case-insensitive, and a run of
// digits by its number, so "2" comes before "10".
func naturalCmp(a, b string) int {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		ad, bd := isDigit(a[0]), isDigit(b[0])
		if ad != bd { // digits sort before letters and punctuation
			if ad {
				return -1
			}
			return 1
		}
		ra, restA := run(a, ad)
		rb, restB := run(b, bd)
		if ad {
			ta, tb := strings.TrimLeft(ra, "0"), strings.TrimLeft(rb, "0")
			if len(ta) != len(tb) {
				return cmpInt(len(ta), len(tb))
			}
			if c := strings.Compare(ta, tb); c != 0 {
				return c
			}
			if len(ra) != len(rb) { // 1 before 01
				return cmpInt(len(ra), len(rb))
			}
		} else if c := strings.Compare(ra, rb); c != 0 {
			return c
		}
		a, b = restA, restB
	}
	return cmpInt(len(a), len(b))
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// run splits off the leading run of digits (digits true) or of non-digits.
func run(s string, digits bool) (head, rest string) {
	i := 0
	for i < len(s) && isDigit(s[i]) == digits {
		i++
	}
	return s[:i], s[i:]
}

func cmpInt[T ~int | ~int64](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// provisionalOrder sorts files in place by the numbers in their names alone (disc, then track, then
// natural file name), which is enough to place them before ffprobe has read their tags.
func provisionalOrder(title string, files []Found) {
	type key struct {
		disc, track int
		name        string
	}
	keys := make(map[string]key, len(files))
	for _, f := range files {
		d, t, okD, okT := trackNumbers(f.Name, filepath.Join("/", f.RelDir), title)
		if !okD {
			d = 0
		}
		if !okT {
			t = math.MaxInt
		}
		keys[f.Path] = key{d, t, f.RelDir + "/" + f.Name}
	}
	sort.SliceStable(files, func(i, j int) bool {
		a, b := keys[files[i].Path], keys[files[j].Path]
		if a.disc != b.disc {
			return a.disc < b.disc
		}
		if a.track != b.track {
			return a.track < b.track
		}
		if c := naturalCmp(a.name, b.name); c != 0 {
			return c < 0
		}
		return files[i].Path < files[j].Path
	})
}

// orderInput is one file for smartOrder: the numbers its name carries and the numbers its tags carry.
type orderInput struct {
	ID, Path, Key             string // Key is the text sorted naturally (folder and file name)
	NameDisc, NameTrack       int
	HasNameDisc, HasNameTrack bool
	TagDisc, TagTrack         int
	HasTagDisc, HasTagTrack   bool
}

// tagNumber reads a leading whole number from the first present tag, such as "3" or "07/24".
func tagNumber(tags map[string]string, keys ...string) (int, bool) {
	for _, k := range keys {
		v := strings.TrimSpace(tags[k])
		if v == "" {
			continue
		}
		if i := strings.IndexByte(v, '/'); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n, true
		}
	}
	return 0, false
}

// sequential reports whether sorted values never jump by more than one.
func sequential(vals []int) bool {
	sort.Ints(vals)
	for i := 1; i < len(vals); i++ {
		if vals[i]-vals[i-1] > 1 {
			return false
		}
	}
	return true
}

func distinct(vals []int) int {
	seen := map[int]bool{}
	for _, v := range vals {
		seen[v] = true
	}
	return len(seen)
}

// smartOrder returns the files in play order. The disc comes from the tags when every file has one
// and they form a sequence, otherwise from the names under the same test, otherwise it is ignored.
// The track comes from the names when they hold more distinct numbers than the tags, otherwise from
// the tags. A file without a track number goes last; ties fall back to the natural file name.
func smartOrder(in []orderInput) []orderInput {
	out := append([]orderInput(nil), in...)
	var tagDiscs, nameDiscs, tagTracks, nameTracks []int
	for _, f := range out {
		if f.HasTagDisc {
			tagDiscs = append(tagDiscs, f.TagDisc)
		}
		if f.HasNameDisc {
			nameDiscs = append(nameDiscs, f.NameDisc)
		}
		if f.HasTagTrack {
			tagTracks = append(tagTracks, f.TagTrack)
		}
		if f.HasNameTrack {
			nameTracks = append(nameTracks, f.NameTrack)
		}
	}
	const (
		discNone = iota
		discTags
		discNames
	)
	discKey := discNone
	switch {
	case len(out) > 0 && len(tagDiscs) == len(out) && sequential(tagDiscs):
		discKey = discTags
	case len(out) > 0 && len(nameDiscs) == len(out) && sequential(nameDiscs):
		discKey = discNames
	}
	useNames := distinct(nameTracks) > distinct(tagTracks)

	disc := func(f orderInput) int {
		switch discKey {
		case discTags:
			return f.TagDisc
		case discNames:
			return f.NameDisc
		}
		return 0
	}
	track := func(f orderInput) int {
		n, ok := f.TagTrack, f.HasTagTrack
		if useNames {
			n, ok = f.NameTrack, f.HasNameTrack
		}
		if !ok {
			return math.MaxInt
		}
		return n
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if da, db := disc(a), disc(b); da != db {
			return da < db
		}
		if ta, tb := track(a), track(b); ta != tb {
			return ta < tb
		}
		if c := naturalCmp(a.Key, b.Key); c != 0 {
			return c < 0
		}
		return a.Path < b.Path
	})
	return out
}
