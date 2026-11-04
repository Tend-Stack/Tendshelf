package align_test

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/align/aligntest"
)

// coreAlign runs the whole core over a small synthetic book and returns the pieces used.
func coreAlign(t *testing.T, reuse func(int, string) (*align.Timing, bool)) (align.Result, align.Input) {
	t.Helper()
	doc := aligntest.Doc(aligntest.Chapters(3, 3, 150))
	v := aligntest.Vocab()
	script := aligntest.NewScript(aligntest.Pieces(doc), 4)
	m, _ := aligntest.Gen(script, v, 0.5, 6)
	in := align.Input{
		Text: align.BuildText(doc, "en", v), Vocab: v, Src: align.MemSource{M: m},
		Track: script.Track("hash-core"), ModelSHA: "model-core", Params: align.DefaultParams(), Reuse: reuse,
	}
	res, err := align.Align(in)
	if err != nil {
		t.Fatal(err)
	}
	return res, in
}

func TestAlignCoreVocab(t *testing.T) {
	if _, err := align.NewVocab([]string{"_", "a"}, 0, "|"); err == nil {
		t.Error("a vocabulary without the delimiter was accepted")
	}
	if _, err := align.NewVocab([]string{"_", "|"}, 0, "|"); err == nil {
		t.Error("a vocabulary without letters was accepted")
	}
	if _, err := align.NewVocab([]string{"_", "|", "a"}, 5, "|"); err == nil {
		t.Error("a blank outside the table was accepted")
	}
	up := aligntest.Vocab()
	if !up.Upper {
		t.Error("the wav2vec2 table should be upper-case mode")
	}
	toks, spelt, ok := up.Encode("café")
	if !ok || spelt != "cafe" || len(toks) != 4 {
		t.Errorf("accent fallback: %v %q %v", toks, spelt, ok)
	}
	if _, _, ok := up.Encode("日本"); ok {
		t.Error("a letter outside the table was encoded")
	}
	lo, err := align.NewVocab([]string{"<pad>", "|", "a", "ñ", "o", "c"}, 0, "|")
	if err != nil || lo.Upper {
		t.Fatalf("lower-case table: %v upper=%v", err, lo.Upper)
	}
	if _, spelt, ok := lo.Encode("Año"); !ok || spelt != "año" {
		t.Errorf("Spanish table: %q %v", spelt, ok)
	}

	doc := aligntest.Doc([][]string{{"The cat sat. In 1999 it ran!", "* * *"}})
	text := align.BuildText(doc, "en", up)
	if len(text.Chapters) != 1 || len(text.Sentences) != 3 {
		t.Fatalf("chapters %d sentences %d", len(text.Chapters), len(text.Sentences))
	}
	if text.Sentences[0].SID != "c1.p1.s0" || text.Sentences[2].First != text.Sentences[2].End {
		t.Errorf("sentence table %+v", text.Sentences)
	}
	wild := 0
	for _, u := range text.Units {
		if u.Wildcard {
			wild++
			if u.Norm != "" || u.Tokens != nil {
				t.Errorf("wildcard carries a reading: %+v", u)
			}
		}
	}
	if wild != 1 {
		t.Errorf("%d wildcards, want the year only", wild)
	}
	if u := text.Units[0]; u.Norm != "the" || len(u.Tokens) != 3 || u.Off0 != 0 || u.Off1 != 3 {
		t.Errorf("first unit %+v", u)
	}
}

func TestAlignCoreEmRoundTrip(t *testing.T) {
	for _, c := range []struct {
		in   float32
		want uint8
	}{{0, 0}, {-0.04, 0}, {-0.06, 1}, {-1, 10}, {-25.5, 255}, {-40, 255}, {0.5, 0}} {
		if got := align.Quantise(c.in); got != c.want {
			t.Errorf("Quantise(%v) = %d, want %d", c.in, got, c.want)
		}
	}
	if d := align.Dequantise(10); math.Abs(float64(d)+1) > 1e-6 {
		t.Errorf("Dequantise(10) = %v", d)
	}
	cache := align.Cache{Root: t.TempDir()}
	em := align.Em{Frames: 3, Vocab: 4, FrameMs: 20, Data: []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 255}}
	if cache.Has("f1", "m1", 7) {
		t.Fatal("empty cache has a window")
	}
	if err := cache.Write("f1", "m1", 7, em); err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cache.Root, "f1", "m1", "00007.em"); cache.Path("f1", "m1", 7) != want || !cache.Has("f1", "m1", 7) {
		t.Errorf("path %q", cache.Path("f1", "m1", 7))
	}
	got, ok, err := cache.Read("f1", "m1", 7)
	if err != nil || !ok || !reflect.DeepEqual(got, em) {
		t.Fatalf("read back: %+v %v %v", got, ok, err)
	}
	if left, _ := filepath.Glob(filepath.Join(cache.Root, "f1", "m1", ".em-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if _, ok, err := cache.Read("f1", "m1", 8); ok || err != nil {
		t.Errorf("missing window: ok=%v err=%v", ok, err)
	}
}

func TestAlignCoreEmCorrupt(t *testing.T) {
	cache := align.Cache{Root: t.TempDir()}
	em := align.Em{Frames: 2, Vocab: 3, FrameMs: 20, Data: []uint8{1, 2, 3, 4, 5, 6}}
	for name, damage := range map[string]func([]byte) []byte{
		"payload": func(b []byte) []byte { b[len(b)-1] ^= 0xFF; return b },
		"magic":   func(b []byte) []byte { b[0] = 'X'; return b },
		"short":   func(b []byte) []byte { return b[:len(b)-2] },
		"empty":   func([]byte) []byte { return nil },
	} {
		if err := cache.Write("f", "m", 1, em); err != nil {
			t.Fatal(err)
		}
		p := cache.Path("f", "m", 1)
		raw, _ := os.ReadFile(p)
		if err := os.WriteFile(p, damage(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok, err := cache.Read("f", "m", 1); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v, want missing", name, ok, err)
		}
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s: damaged file was not deleted", name)
		}
	}
	for _, bad := range []string{"", "..", "a/b", `a\b`} {
		if err := cache.Write(bad, "m", 1, em); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

func TestAlignCorePrune(t *testing.T) {
	cache := align.Cache{Root: t.TempDir()}
	em := align.Em{Frames: 10, Vocab: 10, FrameMs: 20, Data: make([]uint8, 100)}
	now := time.Now()
	for i, name := range []string{"old", "mid", "new"} {
		if err := cache.Write(name, "m", 0, em); err != nil {
			t.Fatal(err)
		}
		age := now.Add(time.Duration(i-3) * time.Hour)
		if err := os.Chtimes(filepath.Join(cache.Root, name, "m"), age, age); err != nil {
			t.Fatal(err)
		}
	}
	size := int64(len(em.Data) + 18)
	cache.Touch("old", "m") // used just now: it is the newest
	freed, err := cache.Prune(2*size, nil)
	if err != nil || freed != size {
		t.Fatalf("freed %d (%v), want %d", freed, err, size)
	}
	if cache.Has("mid", "m", 0) || !cache.Has("old", "m", 0) || !cache.Has("new", "m", 0) {
		t.Error("the least recently used folder should go")
	}
	freed, _ = cache.Prune(0, map[string]bool{"old/m": true})
	if freed != size || !cache.Has("old", "m", 0) || cache.Has("new", "m", 0) {
		t.Errorf("keep list ignored: freed %d", freed)
	}
	if _, err := os.Stat(filepath.Join(cache.Root, "new")); !os.IsNotExist(err) {
		t.Error("the emptied file folder stayed")
	}
}

func TestAlignCoreGreedy(t *testing.T) {
	v := aligntest.Vocab()
	doc := aligntest.Doc([][]string{{"Aa kolo bello. Dime kola."}})
	script := aligntest.NewScript(aligntest.Pieces(doc), 2)
	m, truth := aligntest.Gen(script, v, 0, 1)
	heard, pauses, err := align.Greedy(align.MemSource{M: m}, script.Track("h"), v)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range heard {
		got = append(got, h.Norm)
	}
	if want := "aa kolo bello dime kola"; strings.Join(got, " ") != want {
		t.Fatalf("heard %q, want %q", strings.Join(got, " "), want)
	}
	if first := truth["c1.p1.s0"][0]; heard[0].StartF*20 != first {
		t.Errorf("first word starts at %d ms, truth %d", heard[0].StartF*20, first)
	}
	if len(pauses) < 2 {
		t.Errorf("%d pauses, want the lead and tail silence", len(pauses))
	}
	for _, p := range pauses {
		if p.EndF-p.StartF < align.PauseFrames {
			t.Errorf("short pause %+v", p)
		}
	}
}

// coreHand builds a matrix from one best symbol per frame (probability 0.9).
func coreHand(v align.Vocab, peaks []int) align.Matrix {
	m := align.Matrix{T: len(peaks), V: v.Size(), LogP: make([]float32, len(peaks)*v.Size())}
	rest := float32(math.Log(0.1 / float64(v.Size()-1)))
	for t, p := range peaks {
		row := m.Row(t)
		for k := range row {
			row[k] = rest
		}
		row[p] = float32(math.Log(0.9))
	}
	return m
}

func TestAlignCoreViterbi(t *testing.T) {
	v, err := align.NewVocab([]string{"_", "|", "a", "b", "x", "y"}, 0, "|")
	if err != nil {
		t.Fatal(err)
	}
	const blank, delim, a, b = 0, 1, 2, 3
	m := coreHand(v, []int{a, blank, delim, b, blank, a, blank})
	spans, err := align.ForceAlign(m, [][]int{{a}, {b, a}}, []bool{false, false}, v)
	if err != nil {
		t.Fatal(err)
	}
	if spans[0].StartF != 0 || spans[0].EndF != 1 || spans[1].StartF != 3 || spans[1].EndF != 6 {
		t.Errorf("spans %+v", spans)
	}
	for _, s := range spans {
		if math.Abs(s.Score-0.9) > 1e-3 {
			t.Errorf("score %v, want 0.9", s.Score)
		}
	}
	// A unit that was not said scores low.
	spans, err = align.ForceAlign(coreHand(v, []int{a, blank, delim, a, blank, blank}), [][]int{{a}, {b}}, []bool{false, false}, v)
	if err != nil || spans[1].Score > 0.3 {
		t.Errorf("unsaid unit: %+v %v", spans, err)
	}
	if _, err := align.ForceAlign(coreHand(v, []int{a, blank}), [][]int{{a, a}}, []bool{false}, v); err != align.ErrTooShort {
		t.Errorf("double letter in two frames: %v, want ErrTooShort", err)
	}
	if _, err := align.ForceAlign(coreHand(v, []int{a}), [][]int{{a}, {b}}, []bool{false, false}, v); err != align.ErrTooShort {
		t.Errorf("too few frames: %v", err)
	}
	if got, err := align.ForceAlign(coreHand(v, []int{a, blank, a}), [][]int{{a, a}}, []bool{false}, v); err != nil || got[0].StartF != 0 || got[0].EndF != 3 {
		t.Errorf("repeat needs the blank between: %+v %v", got, err)
	}
}

func TestAlignCoreWildcard(t *testing.T) {
	v, _ := align.NewVocab([]string{"_", "|", "a", "b", "x", "y", "z"}, 0, "|")
	const blank, delim, a, b, x, y, z = 0, 1, 2, 3, 4, 5, 6
	// a | x y z (a year said as three words, with their delimiters) | b
	m := coreHand(v, []int{a, blank, delim, x, blank, y, delim, z, blank, delim, b, blank})
	spans, err := align.ForceAlign(m, [][]int{{a}, nil, {b}}, []bool{false, true, false}, v)
	if err != nil {
		t.Fatal(err)
	}
	if spans[0].StartF != 0 || spans[0].EndF != 1 {
		t.Errorf("left neighbour %+v", spans[0])
	}
	if spans[2].StartF != 10 || spans[2].EndF != 11 {
		t.Errorf("right neighbour %+v, the wildcard must not steal its frame", spans[2])
	}
	if spans[1].Score != 0 || spans[1].StartF < 2 || spans[1].EndF > 10 {
		t.Errorf("wildcard %+v", spans[1])
	}
}

func TestAlignCoreMapSwapped(t *testing.T) {
	doc := aligntest.Doc(aligntest.Chapters(2, 4, 120))
	v := aligntest.Vocab()
	by := map[string][]aligntest.Piece{}
	for _, p := range aligntest.Pieces(doc) {
		c := p.SID[:strings.Index(p.SID, ".")]
		by[c] = append(by[c], p)
	}
	var pieces []aligntest.Piece
	for _, c := range []string{"c1", "c3", "c2", "c4"} {
		pieces = append(pieces, by[c]...)
	}
	script := aligntest.NewScript(pieces, 1)
	m, _ := aligntest.Gen(script, v, 0.5, 2)
	track := script.Track("h")
	heard, pauses, err := align.Greedy(align.MemSource{M: m}, track, v)
	if err != nil {
		t.Fatal(err)
	}
	p := align.DefaultParams()
	p.BlockMs = 3600000 // one block holds all four chapters
	mp := align.Map(p, align.BuildText(doc, "en", v), heard, pauses, track.Frames())
	if len(mp.Regions) < 2 {
		t.Fatalf("%d regions, want the two orders of one block", len(mp.Regions))
	}
	if len(mp.Regions) > align.MaxRegions {
		t.Errorf("%d regions, at most %d", len(mp.Regions), align.MaxRegions)
	}
	if mp.SpeechMapped < 0.99 {
		t.Errorf("speech mapped %.2f", mp.SpeechMapped)
	}
	// Every heard word is paired once, and each pair is the same word.
	text := align.BuildText(doc, "en", v)
	if float64(len(mp.Pairs)) < 0.99*float64(len(heard)) {
		t.Errorf("%d pairs for %d heard words", len(mp.Pairs), len(heard))
	}
	for _, pr := range mp.Pairs {
		if pr.Exact && heard[pr.Heard].Norm != text.Units[pr.Unit].Norm {
			t.Fatalf("pair %+v joins %q and %q", pr, heard[pr.Heard].Norm, text.Units[pr.Unit].Norm)
		}
	}
}

func TestAlignCoreNW(t *testing.T) {
	// 40 words said as written, then 30 words where every third one is another word, then 40 as written.
	words := aligntest.Words(41, 110)
	other := aligntest.Words(42, 10)
	text := strings.Join(words, " ") + "."
	said := append([]string{}, words...)
	for i := 40; i < 70; i += 3 {
		said[i] = other[(i-40)/3]
	}
	doc := aligntest.Doc([][]string{{text}})
	pieces := aligntest.Pieces(doc)
	pieces[0].Words = said
	v := aligntest.Vocab()
	script := aligntest.NewScript(pieces, 1)
	m, _ := aligntest.Gen(script, v, 0.3, 2)
	track := script.Track("h")
	heard, pauses, _ := align.Greedy(align.MemSource{M: m}, track, v)
	tx := align.BuildText(doc, "en", v)
	inGap := func(mp align.Mapping) int {
		n := 0
		for _, pr := range mp.Pairs {
			if pr.Unit >= 45 && pr.Unit < 65 {
				n++
			}
		}
		return n
	}
	p := align.DefaultParams()
	if n := inGap(align.Map(p, tx, heard, pauses, track.Frames())); n < 12 {
		t.Errorf("with room for Needleman-Wunsch only %d pairs in the gap", n)
	}
	p.NWMaxCells = 50 // the gap (about 31 x 31 cells) is over the bound, and no 3-gram survives in it
	if n := inGap(align.Map(p, tx, heard, pauses, track.Frames())); n != 0 {
		t.Errorf("over the cell bound %d pairs were still found in the gap", n)
	}
}

func TestAlignCoreQuantised(t *testing.T) {
	res, in := coreAlign(t, nil)
	m := in.Src.(align.MemSource).M
	in.Src = align.MemSource{M: align.QuantiseMatrix(m).Matrix()}
	q, err := align.Align(in)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for ci := range res.Chapters {
		a, b := res.Chapters[ci].Timing, q.Chapters[ci].Timing
		for i := range a.SID {
			if (a.SS[i] < 0) != (b.SS[i] < 0) {
				t.Fatalf("%s timed in one run only", a.SID[i])
			}
			if a.SS[i] < 0 {
				continue
			}
			if d := a.SS[i] - b.SS[i]; d > 20 || d < -20 {
				t.Fatalf("%s starts %d ms apart after quantising", a.SID[i], d)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("nothing compared")
	}
}

func TestAlignCoreDeterministic(t *testing.T) {
	a, _ := coreAlign(t, nil)
	b, _ := coreAlign(t, nil)
	dirA, dirB := t.TempDir(), t.TempDir()
	for ci := range a.Chapters {
		ka, kb := a.Chapters[ci].Key, b.Chapters[ci].Key
		if ka == "" || ka != kb {
			t.Fatalf("chapter %d keys %q %q", ci, ka, kb)
		}
		if err := align.WriteTiming(dirA, ka, a.Chapters[ci].Timing); err != nil {
			t.Fatal(err)
		}
		if err := align.WriteTiming(dirB, kb, b.Chapters[ci].Timing); err != nil {
			t.Fatal(err)
		}
		x, _ := os.ReadFile(align.TimingPath(dirA, ka))
		y, _ := os.ReadFile(align.TimingPath(dirB, kb))
		if !bytes.Equal(x, y) {
			t.Errorf("chapter %d timing bytes differ between runs", ci)
		}
		back, err := align.ReadTiming(dirA, ka)
		if err != nil || !reflect.DeepEqual(back, a.Chapters[ci].Timing) {
			t.Errorf("chapter %d read back: %v", ci, err)
		}
	}
	if _, err := align.ReadTiming(dirA, "missing"); !os.IsNotExist(err) {
		t.Errorf("missing timing: %v", err)
	}
	if err := align.WriteTiming(dirA, "../x", a.Chapters[0].Timing); err == nil {
		t.Error("a key with a path was accepted")
	}
}

func TestAlignCoreReuse(t *testing.T) {
	full, _ := coreAlign(t, nil)
	if full.ViterbiRuns == 0 {
		t.Fatal("no Viterbi runs")
	}
	keep := map[string]*align.Timing{}
	for _, c := range full.Chapters {
		keep[c.Key] = c.Timing
	}
	asked := 0
	all, _ := coreAlign(t, func(ch int, key string) (*align.Timing, bool) {
		asked++
		tm, ok := keep[key]
		return tm, ok
	})
	if all.ViterbiRuns != 0 || asked != len(full.Chapters) {
		t.Errorf("full reuse: %d runs, %d asked", all.ViterbiRuns, asked)
	}
	if all.Timed != full.Timed || all.State != full.State {
		t.Errorf("reused result differs: %d/%s vs %d/%s", all.Timed, all.State, full.Timed, full.State)
	}
	second := full.Chapters[1].Key
	part, _ := coreAlign(t, func(ch int, key string) (*align.Timing, bool) {
		tm, ok := keep[key]
		return tm, ok && key != second
	})
	if part.ViterbiRuns == 0 || part.ViterbiRuns >= full.ViterbiRuns {
		t.Errorf("partial reuse: %d runs of %d", part.ViterbiRuns, full.ViterbiRuns)
	}
	if !reflect.DeepEqual(part.Chapters[1].Timing, full.Chapters[1].Timing) {
		t.Error("the recomputed chapter differs from the first run")
	}
}
