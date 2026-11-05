// Package dict reads dictionary files the owner put in the data folder, so a reader can look a word up (M5 G1).
//
// Nothing is bundled, nothing is fetched and nothing is written: the package opens no network connection and
// only reads regular files below one folder. Every file is treated as hostile (it came from the owner, but it
// came from somewhere): sizes are bounded, a damaged dictionary is skipped and logged, a panic in a parser is
// recovered, and a definition reaches the caller as plain text, whatever markup the file held.
//
// Two formats are read, from their published descriptions: StarDict (.ifo with .idx or .idx.gz, optional
// .syn, and .dict or dictzip .dict.dz) and a tab-separated text file (<name>.tsv, "word<TAB>definition").
package dict

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// The bounds. A dictionary that goes past one of them is skipped, or its entry is cut; none can use more memory
// or time than the number beside it.
const (
	MaxDictionaries  = 8                // dictionaries read from the folder; the rest are ignored
	MaxHitsPerDict   = 3                // entries one dictionary answers with
	MaxEntryRead     = 256 << 10        // bytes read from one entry in a .dict file
	MaxDefinition    = 4000             // runes of one definition
	maxSynBytes      = 64 << 20         // a StarDict .syn
	maxIfoBytes      = 64 << 10         // a StarDict .ifo
	maxTSVBytes      = 64 << 20         // a TSV file
	maxTSVLine       = 1 << 20          // one TSV line
	maxIndexEntries  = 2_000_000        // headwords and synonyms of one dictionary
	maxHeadword      = 255              // bytes; StarDict's own limit
	loadDeadline     = 30 * time.Second // reading one dictionary's index
	maxFolderEntries = 256              // directory entries looked at
)

// maxIndexBytes is the most a StarDict .idx may be, after unpacking. A variable so a test can lower it.
var maxIndexBytes int64 = 256 << 20

// Entry is one definition found for a word.
type Entry struct {
	Dictionary string // the dictionary's name
	Word       string // the headword as the dictionary spells it
	Text       string // plain text, paragraphs separated by a newline
}

// source is one headword (or synonym) of one dictionary and where its definition is.
type source struct {
	key  string // folded, what is searched
	word string // as the dictionary spells it
	off  uint64 // StarDict: offset in the .dict
	size uint32 // StarDict: bytes
	text string // TSV: the definition itself
}

// dictionary answers Lookup for one file set.
type dictionary struct {
	name  string
	index []source // sorted by key
	read  func(source) (string, error)
}

// lookup returns up to MaxHitsPerDict entries whose folded headword equals key.
func (d *dictionary) lookup(key string) []Entry {
	i := sort.Search(len(d.index), func(i int) bool { return d.index[i].key >= key })
	var out []Entry
	seen := map[uint64]bool{}
	for ; i < len(d.index) && d.index[i].key == key && len(out) < MaxHitsPerDict; i++ {
		src := d.index[i]
		if src.text == "" && seen[src.off] { // a synonym that points to the entry a headword already gave
			continue
		}
		seen[src.off] = true
		text, err := d.read(src)
		if err != nil {
			slog.Warn("dictionary entry unreadable", "dictionary", d.name, "err", err)
			continue
		}
		if text = clean(text); text != "" {
			out = append(out, Entry{Dictionary: d.name, Word: src.word, Text: text})
		}
	}
	return out
}

// Set is the dictionaries of one folder. It is safe for concurrent use.
type Set struct {
	dir string

	mu    sync.Mutex
	stamp string // what the folder looked like when it was read
	dicts []*dictionary
}

// Open returns the set for dir (<data>/dictionaries). Nothing is read until the first Find or Count, and the
// folder is read again when a file in it changes. A folder that does not exist is an empty set.
func Open(dir string) *Set { return &Set{dir: dir} }

// Count is how many dictionaries could be read.
func (s *Set) Count() int { return len(s.current()) }

// Find returns the definitions of word: folded on both sides, up to MaxHitsPerDict from each dictionary.
// Only the exact folded word is searched; Forms gives the other spellings a caller may try.
func (s *Set) Find(word string) []Entry {
	key := Fold(word)
	if key == "" {
		return nil
	}
	var out []Entry
	for _, d := range s.current() {
		out = append(out, d.lookup(key)...)
	}
	return out
}

// current returns the dictionaries, reading the folder first when it changed.
func (s *Set) current() []*dictionary {
	s.mu.Lock()
	defer s.mu.Unlock()
	files := s.listing()
	stamp := fmt.Sprint(files)
	if s.stamp == stamp && s.stamp != "" {
		return s.dicts
	}
	s.stamp, s.dicts = stamp, s.load(files)
	return s.dicts
}

// file is one regular file of the folder.
type file struct {
	Name string
	Size int64
	Time int64
}

// listing is the regular files of the folder, by name. A symbolic link, a folder and anything else is left out.
func (s *Set) listing() []file {
	f, err := os.Open(s.dir)
	if err != nil {
		return []file{{}} // the stamp of "no folder" differs from "never read"
	}
	defer f.Close()
	names, _ := f.Readdirnames(maxFolderEntries)
	slices.Sort(names)
	out := []file{{}}
	for _, name := range names {
		fi, err := os.Lstat(filepath.Join(s.dir, name))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, file{name, fi.Size(), fi.ModTime().UnixNano()})
	}
	return out
}

// load reads every dictionary in files. A damaged one is skipped and logged.
func (s *Set) load(files []file) []*dictionary {
	have := map[string]bool{}
	for _, f := range files {
		have[f.Name] = true
	}
	var out []*dictionary
	for _, f := range files {
		if len(out) >= MaxDictionaries {
			slog.Warn("more dictionaries than Tendshelf reads; the rest are ignored", "limit", MaxDictionaries)
			break
		}
		var d *dictionary
		var err error
		switch {
		case strings.HasSuffix(f.Name, ".ifo"):
			d, err = s.guard(f.Name, func() (*dictionary, error) { return loadStarDict(s.dir, strings.TrimSuffix(f.Name, ".ifo"), have) })
		case strings.HasSuffix(f.Name, ".tsv"):
			d, err = s.guard(f.Name, func() (*dictionary, error) { return loadTSV(s.dir, f.Name) })
		default:
			continue
		}
		if err != nil {
			slog.Warn("dictionary skipped", "file", f.Name, "err", err)
			continue
		}
		out = append(out, d)
	}
	return out
}

// guard runs a parser and turns a panic in it into an error.
func (s *Set) guard(name string, parse func() (*dictionary, error)) (d *dictionary, err error) {
	defer func() {
		if r := recover(); r != nil {
			d, err = nil, fmt.Errorf("parser panic: %v", r)
		}
	}()
	return parse()
}

// finish sorts an index by key.
func finish(d *dictionary) *dictionary {
	slices.SortStableFunc(d.index, func(a, b source) int { return strings.Compare(a.key, b.key) })
	return d
}

// deadline is the context a dictionary is read under.
func deadline() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), loadDeadline)
}

// ---- Folding ----

// Fold is the form a word is compared in: lower case, accents taken off (textmatch.Fold), letters and digits
// kept, an apostrophe or hyphen kept inside a word, runs of other characters as one space, nothing at the ends.
func Fold(word string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.ToValidUTF8(word, "")) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteString(textmatch.Fold(r))
		case (r == '\'' || r == '’' || r == '-') && b.Len() > 0 && !space:
			if r == '’' {
				r = '\''
			}
			b.WriteRune(r)
		default:
			space = b.Len() > 0
		}
	}
	return strings.TrimRight(b.String(), "'-")
}

// Forms are the other spellings of an English word to try when the word itself is not there: one step of -s,
// -es, -ies, -ed, -ing or -ly taken off, with the usual repairs ("loved" to "love", "stopped" to "stop"). The
// word itself is not in the list, and no stem is shorter than three letters. Callers use it for English books only.
func Forms(word string) []string {
	w := Fold(word)
	var out []string
	add := func(stem string) {
		if utf8.RuneCountInString(stem) >= 3 && stem != w && !slices.Contains(out, stem) {
			out = append(out, stem)
		}
	}
	trim := func(suffix string) (string, bool) { return strings.CutSuffix(w, suffix) }
	if s, ok := trim("ies"); ok {
		add(s + "y")
	}
	if s, ok := trim("es"); ok {
		add(s)
	}
	if s, ok := trim("s"); ok && !strings.HasSuffix(w, "ss") {
		add(s)
	}
	for _, suffix := range []string{"ed", "ing"} {
		if s, ok := trim(suffix); ok {
			add(s)
			add(s + "e")
			if n := len(s); n >= 2 && s[n-1] == s[n-2] { // stopped, running
				add(s[:n-1])
			}
		}
	}
	if s, ok := trim("ly"); ok {
		add(s)
	}
	return out
}

// clean cuts a definition to MaxDefinition runes and drops control characters other than the newline.
func clean(text string) string {
	text = strings.ToValidUTF8(text, "")
	var b strings.Builder
	n := 0
	for _, r := range text {
		if n >= MaxDefinition {
			b.WriteString("…")
			break
		}
		if r == '\n' || !unicode.IsControl(r) {
			b.WriteRune(r)
			n++
		}
	}
	return strings.TrimSpace(b.String())
}
