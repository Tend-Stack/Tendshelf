package dict

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// word is one fixture entry: a headword and the definition bytes as they sit in the .dict file.
type word struct {
	head string
	def  string
}

// fixture describes a StarDict set the tests write into a temporary folder.
type fixture struct {
	base     string
	bits     int      // idxoffsetbits: 32 or 64
	same     string   // sametypesequence
	words    []word   // in index order (StarDict sorts them; the reader must not rely on it)
	syns     [][2]any // {synonym string, headword number int}
	gzIdx    bool
	dictzip  int // chunk length of a dictzip .dict.dz; 0 writes a plain .dict
	sizeLies bool
}

func be32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }

func (f fixture) write(t *testing.T, dir string) {
	t.Helper()
	var body, idx []byte
	for _, w := range f.words {
		idx = append(idx, w.head...)
		idx = append(idx, 0)
		if f.bits == 64 {
			idx = binary.BigEndian.AppendUint64(idx, uint64(len(body)))
		} else {
			idx = append(idx, be32(uint32(len(body)))...)
		}
		idx = append(idx, be32(uint32(len(w.def)))...)
		body = append(body, w.def...)
	}
	ifoSize := len(idx)
	if f.sizeLies {
		ifoSize++
	}
	ifo := fmt.Sprintf("StarDict's dict ifo file\nversion=3.0.0\nbookname=%s\nwordcount=%d\nidxfilesize=%d\nsametypesequence=%s\nidxoffsetbits=%d\n",
		strings.ToUpper(f.base[:1])+f.base[1:], len(f.words), ifoSize, f.same, f.bits)
	put := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, f.base+name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(".ifo", []byte(ifo))
	if f.gzIdx {
		var z bytes.Buffer
		w := gzip.NewWriter(&z)
		w.Write(idx)
		w.Close()
		put(".idx.gz", z.Bytes())
	} else {
		put(".idx", idx)
	}
	if len(f.syns) > 0 {
		var syn []byte
		for _, s := range f.syns {
			syn = append(syn, s[0].(string)...)
			syn = append(syn, 0)
			syn = append(syn, be32(uint32(s[1].(int)))...)
		}
		put(".syn", syn)
	}
	if f.dictzip > 0 {
		put(".dict.dz", dictzipOf(body, f.dictzip))
	} else {
		put(".dict", body)
	}
}

// dictzipOf packs body as a dictzip file with chunks of n bytes of text.
func dictzipOf(body []byte, n int) []byte {
	var chunks [][]byte
	for i := 0; i < len(body) || i == 0; i += n {
		part := body[i:min(i+n, len(body))]
		var z bytes.Buffer
		w, _ := flate.NewWriter(&z, flate.BestCompression)
		w.Write(part)
		if i+n >= len(body) {
			w.Close()
		} else {
			w.Flush()
		}
		chunks = append(chunks, z.Bytes())
	}
	extra := []byte{'R', 'A', 0, 0, 1, 0}
	extra = binary.LittleEndian.AppendUint16(extra, uint16(n))
	extra = binary.LittleEndian.AppendUint16(extra, uint16(len(chunks)))
	for _, c := range chunks {
		extra = binary.LittleEndian.AppendUint16(extra, uint16(len(c)))
	}
	binary.LittleEndian.PutUint16(extra[2:], uint16(len(extra)-4))
	out := []byte{0x1f, 0x8b, 8, 4, 0, 0, 0, 0, 0, 3}
	out = binary.LittleEndian.AppendUint16(out, uint16(len(extra)))
	out = append(out, extra...)
	for _, c := range chunks {
		out = append(out, c...)
	}
	return append(out, make([]byte, 8)...) // CRC and size: not read
}

func texts(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Dictionary+"/"+e.Word+"/"+e.Text)
	}
	return out
}

// TestDictStarDict: both offset widths, a packed index, synonyms, and the field types.
func TestDictStarDict(t *testing.T) {
	for _, bits := range []int{32, 64} {
		t.Run(fmt.Sprintf("%d bit", bits), func(t *testing.T) {
			dir := t.TempDir()
			fixture{base: "plain", bits: bits, same: "m", gzIdx: bits == 64, words: []word{
				{"Café", "a small restaurant"}, {"serene", "calm and untroubled"}, {"serene", "second sense"},
				{"serene", "third"}, {"serene", "fourth is cut"}, {"ünder", "below"},
			}, syns: [][2]any{{"cafe au lait", 0}, {"calm", 1}, {"bad", 99}}}.write(t, dir)
			set := Open(dir)
			if set.Count() != 1 {
				t.Fatalf("Count = %d, want 1", set.Count())
			}
			if got := texts(set.Find("CAFÉ")); !slices.Equal(got, []string{"Plain/Café/a small restaurant"}) {
				t.Errorf("accents and case: %v", got)
			}
			if got := texts(set.Find("cafe")); len(got) != 1 {
				t.Errorf("cafe: %v", got)
			}
			if got := texts(set.Find("Under")); len(got) != 1 {
				t.Errorf("under: %v", got)
			}
			if got := set.Find("serene"); len(got) != MaxHitsPerDict {
				t.Errorf("serene gave %d hits, want %d", len(got), MaxHitsPerDict)
			}
			if got := texts(set.Find("calm")); !slices.Contains(got, "Plain/serene/calm and untroubled") {
				t.Errorf("synonym: %v", got)
			}
			if got := set.Find("cafe au lait"); len(got) != 1 || got[0].Word != "Café" {
				t.Errorf("synonym with a space: %v", got)
			}
			if got := set.Find("missing"); len(got) != 0 {
				t.Errorf("missing: %v", got)
			}
		})
	}
}

// TestDictStarDictTypes: sametypesequence with sized fields, no sametypesequence, and markup in h fields.
func TestDictStarDictTypes(t *testing.T) {
	dir := t.TempDir()
	// "tm": phonetics, then meaning to the end. "hX" style: h is last and may hold markup.
	fixture{base: "tm", bits: 32, same: "tm", words: []word{{"dog", "/dɔɡ/\x00a loyal animal"}}}.write(t, dir)
	fixture{base: "html", bits: 32, same: "h", words: []word{{"cat", `<p>a <b>small</b> pet</p><script>alert(1)</script><p>purrs &amp; naps</p>`}}}.write(t, dir)
	free := "mfirst\x00xsecond <i>part</i>" // no sametypesequence: each field says its type
	fixture{base: "free", bits: 32, same: "", words: []word{{"owl", free}}}.write(t, dir)
	// A sized upper-case field is skipped; the field after it still reads.
	skip := append(append([]byte("P"), be32(3)...), "xyz"...)
	skip = append(skip, "mkept"...)
	fixture{base: "sized", bits: 32, same: "", words: []word{{"ant", string(skip)}}}.write(t, dir)
	set := Open(dir)
	if set.Count() != 4 {
		t.Fatalf("Count = %d, want 4", set.Count())
	}
	want := map[string]string{
		"dog": "Tm/dog//dɔɡ/\na loyal animal",
		"cat": "Html/cat/a small pet\npurrs & naps",
		"owl": "Free/owl/first\nsecond part",
		"ant": "Sized/ant/kept",
	}
	for w, exp := range want {
		if got := texts(set.Find(w)); !slices.Equal(got, []string{exp}) {
			t.Errorf("%s = %q, want %q", w, got, exp)
		}
	}
}

// TestDictDictzip: a dictzip body read across the edges of its chunks, and the same entries from a plain file.
func TestDictDictzip(t *testing.T) {
	words := []word{
		{"alpha", strings.Repeat("a", 90)}, {"beta", strings.Repeat("b", 200)}, {"gamma", "short"},
		{"delta", strings.Repeat("d", 31)}, {"epsilon", strings.Repeat("é", 50)},
	}
	plainDir, zipDir := t.TempDir(), t.TempDir()
	fixture{base: "z", bits: 32, same: "m", words: words}.write(t, plainDir)
	fixture{base: "z", bits: 32, same: "m", words: words, dictzip: 64}.write(t, zipDir)
	plain, packed := Open(plainDir), Open(zipDir)
	for _, w := range words {
		a, b := plain.Find(w.head), packed.Find(w.head)
		if len(a) != 1 || len(b) != 1 || a[0].Text != b[0].Text || a[0].Text != w.def {
			t.Errorf("%s: plain %q, packed %q", w.head, texts(a), texts(b))
		}
	}
}

// TestDictTSV: the tab format, its escapes, a byte order mark, folding, and lines that are skipped.
func TestDictTSV(t *testing.T) {
	dir := t.TempDir()
	data := "\xef\xbb\xbfZebra\ta striped animal\\nsecond line\nno tab here\n\tno word\nÉcole\ta school\nbad\t\xff\xfe\nzebra\talso\n"
	if err := os.WriteFile(filepath.Join(dir, "my words.tsv"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	set := Open(dir)
	got := texts(set.Find("zebra"))
	if !slices.Equal(got, []string{"my words/Zebra/a striped animal\nsecond line", "my words/zebra/also"}) {
		t.Errorf("zebra: %q", got)
	}
	if got := texts(set.Find("ecole")); len(got) != 1 {
		t.Errorf("ecole: %q", got)
	}
	if got := set.Find("bad"); len(got) != 0 {
		t.Errorf("a line that is not UTF-8 gave %q", got)
	}
}

// TestDictRefresh: a file added after the first use is picked up; a missing folder is an empty set.
func TestDictRefresh(t *testing.T) {
	if set := Open(filepath.Join(t.TempDir(), "nothing")); set.Count() != 0 || len(set.Find("word")) != 0 {
		t.Error("a missing folder is not an empty set")
	}
	dir := t.TempDir()
	set := Open(dir)
	if set.Count() != 0 {
		t.Fatal("not empty")
	}
	if err := os.WriteFile(filepath.Join(dir, "late.tsv"), []byte("hello\tgreeting\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if set.Count() != 1 || len(set.Find("hello")) != 1 {
		t.Error("a file added later was not read")
	}
}

// TestDictFold covers the folding and the English forms.
func TestDictFold(t *testing.T) {
	for in, want := range map[string]string{
		"Café": "cafe", "  ÉCOLE!": "ecole", "don’t": "don't", "well-known": "well-known", "'quoted'": "quoted",
		"ice   cream": "ice cream", "naïve,": "naive", "": "", "...": "", "straße": "strasse", "Œuvre": "oeuvre",
	} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string][]string{
		"dogs":    {"dog"},
		"boxes":   {"box", "boxe"},
		"babies":  {"baby", "babie"},
		"loved":   {"lov", "love"},
		"stopped": {"stopp", "stoppe", "stop"},
		"running": {"runn", "runne", "run"},
		"quickly": {"quick"},
		"glass":   nil,
		"is":      nil,
	} {
		got := Forms(in)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("Forms(%q) = %v, missing %q", in, got, w)
			}
		}
		if want == nil && len(got) != 0 {
			t.Errorf("Forms(%q) = %v, want none", in, got)
		}
	}
}

// TestDictHostile: damaged and malicious files are skipped, never fatal, and never reach outside the folder.
func TestDictHostile(t *testing.T) {
	good := fixture{base: "good", bits: 32, same: "m", words: []word{{"ok", "fine"}}}
	t.Run("truncated index", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "cut", bits: 32, same: "m", words: []word{{"abc", "x"}, {"def", "y"}}}.write(t, dir)
		idx, _ := os.ReadFile(filepath.Join(dir, "cut.idx"))
		os.WriteFile(filepath.Join(dir, "cut.idx"), idx[:len(idx)-3], 0o600)
		good.write(t, dir)
		set := Open(dir)
		if set.Count() != 1 || len(set.Find("ok")) != 1 {
			t.Errorf("Count = %d; the good dictionary must survive a damaged one", set.Count())
		}
	})
	t.Run("size in the ifo lies", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "lie", bits: 32, same: "m", words: []word{{"a", "b"}}, sizeLies: true}.write(t, dir)
		if Open(dir).Count() != 0 {
			t.Error("a dictionary whose .ifo disagrees with its .idx was read")
		}
	})
	t.Run("offset past the end and a huge size", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "far", bits: 32, same: "m", words: []word{{"far", "x"}, {"big", "y"}}}.write(t, dir)
		idx := slices.Concat([]byte("far\x00"), be32(1<<30), be32(5), []byte("big\x00"), be32(0), be32(0xffffffff))
		os.WriteFile(filepath.Join(dir, "far.idx"), idx, 0o600)
		ifo := fmt.Sprintf("StarDict's dict ifo file\nbookname=Far\nsametypesequence=m\nidxfilesize=%d\n", len(idx))
		os.WriteFile(filepath.Join(dir, "far.ifo"), []byte(ifo), 0o600)
		set := Open(dir)
		if got := set.Find("far"); len(got) != 0 {
			t.Errorf("an offset past the end of the file answered %v", got)
		}
		if got := set.Find("big"); len(got) != 0 { // 4 GiB asked, 1 byte file: refused, and never allocated
			t.Errorf("a huge size answered %v", got)
		}
	})
	t.Run("huge entry in a big file is cut", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "long", bits: 32, same: "m", words: []word{{"long", strings.Repeat("x", 300<<10)}}}.write(t, dir)
		got := Open(dir).Find("long")
		if len(got) != 1 || len([]rune(got[0].Text)) > MaxDefinition+1 {
			t.Errorf("a long definition was not cut: %d hits", len(got))
		}
	})
	t.Run("zip bomb index", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "bomb", bits: 32, same: "m", words: []word{{"a", "b"}}}.write(t, dir)
		os.Remove(filepath.Join(dir, "bomb.idx"))
		var z bytes.Buffer
		w, _ := gzip.NewWriterLevel(&z, gzip.BestCompression)
		defer func(old int64) { maxIndexBytes = old }(maxIndexBytes)
		maxIndexBytes = 1 << 20
		chunk := make([]byte, 1<<20)
		for i := 0; i < 8; i++ { // 8 MiB of zeros: far more than the 1 MiB the limit is set to here
			w.Write(chunk)
		}
		w.Close()
		os.WriteFile(filepath.Join(dir, "bomb.idx.gz"), z.Bytes(), 0o600)
		if Open(dir).Count() != 0 {
			t.Error("an index that unpacks past its limit was read")
		}
	})
	t.Run("dictzip header lies", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "dz", bits: 32, same: "m", words: []word{{"a", strings.Repeat("b", 100)}}, dictzip: 64}.write(t, dir)
		dz, _ := os.ReadFile(filepath.Join(dir, "dz.dict.dz"))
		os.WriteFile(filepath.Join(dir, "dz.dict.dz"), dz[:20], 0o600) // cut inside the header
		set := Open(dir)
		if set.Count() != 0 || len(set.Find("a")) != 0 {
			t.Error("a truncated dictzip answered")
		}
	})
	t.Run("symlinks are not followed", func(t *testing.T) {
		dir, outside := t.TempDir(), t.TempDir()
		os.WriteFile(filepath.Join(outside, "secret.tsv"), []byte("leak\tsecret\n"), 0o600)
		if err := os.Symlink(filepath.Join(outside, "secret.tsv"), filepath.Join(dir, "link.tsv")); err != nil {
			t.Skip("no symlinks here")
		}
		fixture{base: "real", bits: 32, same: "m", words: []word{{"real", "ok"}}}.write(t, outside)
		os.Symlink(filepath.Join(outside, "real.ifo"), filepath.Join(dir, "real.ifo"))
		os.Symlink(filepath.Join(outside, "real.idx"), filepath.Join(dir, "real.idx"))
		os.Symlink(filepath.Join(outside, "real.dict"), filepath.Join(dir, "real.dict"))
		set := Open(dir)
		if set.Count() != 0 || len(set.Find("leak")) != 0 || len(set.Find("real")) != 0 {
			t.Errorf("a symbolic link was followed (Count %d)", set.Count())
		}
	})
	t.Run("markup is text", func(t *testing.T) {
		dir := t.TempDir()
		fixture{base: "ml", bits: 32, same: "h", words: []word{{"x", `<img src=x onerror=alert(1)>hello<script>evil()</script> <a href="javascript:1">there</a>`}}}.write(t, dir)
		got := Open(dir).Find("x")
		if len(got) != 1 || got[0].Text != "hello there" {
			t.Errorf("markup came through as %q", texts(got))
		}
	})
	t.Run("only eight dictionaries", func(t *testing.T) {
		dir := t.TempDir()
		for i := 0; i < 12; i++ {
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("d%02d.tsv", i)), []byte("w\tx\n"), 0o600)
		}
		if n := Open(dir).Count(); n != MaxDictionaries {
			t.Errorf("Count = %d, want %d", n, MaxDictionaries)
		}
	})
	t.Run("a panic in a parser is recovered", func(t *testing.T) {
		set := Open(t.TempDir())
		d, err := set.guard("x", func() (*dictionary, error) { panic("boom") })
		if d != nil || err == nil {
			t.Error("panic not turned into an error")
		}
	})
}

// TestDictNoNetwork: the package may not import anything that opens a connection or writes a file.
func TestDictNoNetwork(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	banned := []string{`"net"`, `"net/http"`, `"os/exec"`, `"net/smtp"`, `"net/rpc"`}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(name)
		for _, b := range banned {
			if strings.Contains(string(src), "\t"+b+"\n") {
				t.Errorf("%s imports %s", name, b)
			}
		}
		for _, w := range []string{"os.Create(", "os.WriteFile(", "os.Remove(", "os.Rename(", "os.OpenFile(", "os.MkdirAll("} {
			if strings.Contains(string(src), w) {
				t.Errorf("%s calls %s: the package writes nothing", name, w)
			}
		}
	}
}
