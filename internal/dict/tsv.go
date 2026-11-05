package dict

import (
	"bufio"
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// loadTSV reads <name>.tsv: one entry a line, "word<TAB>definition", UTF-8. In a definition the two characters
// \n stand for a line break. A line with no tab, an empty word or a word over 255 bytes is skipped. The whole
// file is kept in memory (at most maxTSVBytes), cut to MaxDefinition runes an entry.
func loadTSV(dir, name string) (*dictionary, error) {
	path := filepath.Join(dir, name)
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if fi.Size() > maxTSVBytes {
		return nil, errors.New("larger than the 64 MiB a text dictionary may be")
	}
	ctx, cancel := deadline()
	defer cancel()
	d := &dictionary{name: strings.TrimSuffix(name, ".tsv")}
	d.read = func(src source) (string, error) { return src.text, nil }
	lines := bufio.NewScanner(f)
	lines.Buffer(make([]byte, 0, 64<<10), maxTSVLine)
	for n := 0; lines.Scan(); n++ {
		if n&1023 == 0 && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line := lines.Bytes()
		if n == 0 {
			line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
		}
		word, def, ok := bytes.Cut(line, []byte{'\t'})
		if !ok || len(word) == 0 || len(word) > maxHeadword || !utf8.Valid(word) || !utf8.Valid(def) {
			continue
		}
		key := Fold(string(word))
		text := strings.TrimSpace(strings.ReplaceAll(string(def), `\n`, "\n"))
		if key == "" || text == "" {
			continue
		}
		if len(text) > 4*MaxDefinition {
			text = string([]rune(text)[:MaxDefinition])
		}
		d.index = append(d.index, source{key: key, word: strings.TrimSpace(string(word)), text: text})
		if len(d.index) > maxIndexEntries {
			return nil, errors.New("too many entries")
		}
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}
	return finish(d), nil
}
