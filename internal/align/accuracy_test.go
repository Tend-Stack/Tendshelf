package align_test

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/align/aligntest"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// Accuracy harness: synthetic narration with known word times (helpers syn*). A sentence is correct when
// its start is within 250 ms of the truth.

const synTolMs = 250

const synNoise = 0.8

// synBase is 6 chapters of about 400 words.
func synBase() [][]string { return aligntest.Chapters(1, 6, 400) }

type synRun struct {
	res   align.Result
	truth aligntest.Truth
	share float64 // correct narrated sentences / narrated sentences
}

// synExecute emits the script, aligns it against doc and checks the invariants every case shares.
func synExecute(t *testing.T, doc *ebook.Doc, pieces []aligntest.Piece, blockMs int64) synRun {
	t.Helper()
	v := aligntest.Vocab()
	script := aligntest.NewScript(pieces, 5)
	m, truth := aligntest.Gen(script, v, synNoise, 9)
	p := align.DefaultParams()
	if blockMs > 0 {
		p.BlockMs = blockMs
	}
	res, err := align.Align(align.Input{
		Text: align.BuildText(doc, "en", v), Vocab: v, Src: align.MemSource{M: m},
		Track: script.Track("hash-a"), ModelSHA: "model-a", Params: p,
	})
	if err != nil {
		t.Fatalf("Align: %v", err)
	}
	return synRun{res: res, truth: truth, share: synCheck(t, res, truth)}
}

// synCheck asserts zero invented times and zero out-of-order starts, and returns the accuracy.
func synCheck(t *testing.T, res align.Result, truth aligntest.Truth) float64 {
	t.Helper()
	correct := 0
	for _, ch := range res.Chapters {
		if ch.Timing == nil {
			continue
		}
		last := int64(-1)
		for i, sid := range ch.Timing.SID {
			ss := ch.Timing.SS[i]
			if ss < 0 {
				if ch.Timing.Gap[i] == nil {
					t.Errorf("%s: no time and no reason", sid)
				}
				continue
			}
			if ss < last {
				t.Errorf("%s: start %d runs back from %d", sid, ss, last)
			}
			last = ss
			want, ok := truth[sid]
			if !ok {
				t.Errorf("%s: timed but never narrated (invented)", sid)
				continue
			}
			if math.Abs(float64(ss-want[0])) <= synTolMs {
				correct++
			}
		}
	}
	if len(truth) == 0 {
		return 0
	}
	return float64(correct) / float64(len(truth))
}

func synNeed(t *testing.T, name string, r synRun, min float64) {
	t.Helper()
	t.Logf("%-14s accuracy %.2f%% state=%s coverage=%.3f labels=%v viterbi=%d speech=%.2f differ=%.3f",
		name, 100*r.share, r.res.State, r.res.Coverage, r.res.Labels, r.res.ViterbiRuns, r.res.SpeechMapped, r.res.NearShare)
	if r.share < min {
		t.Errorf("%s: accuracy %.2f%%, want at least %.0f%%", name, 100*r.share, 100*min)
	}
}

func synHas(labels []string, l string) bool {
	for _, x := range labels {
		if x == l {
			return true
		}
	}
	return false
}

func TestAlignAccuracyClean(t *testing.T) {
	doc := aligntest.Doc(synBase())
	r := synExecute(t, doc, aligntest.Pieces(doc), 0)
	synNeed(t, "clean", r, 0.99)
	if r.res.State != align.StateDone {
		t.Errorf("state %s, want done", r.res.State)
	}
}

func TestAlignAccuracyIntro(t *testing.T) {
	doc := aligntest.Doc(synBase())
	pieces := append([]aligntest.Piece{{Para: -1, Words: aligntest.Words(77, 40)}}, aligntest.Pieces(doc)...)
	synNeed(t, "intro", synExecute(t, doc, pieces, 0), 0.95)
}

func TestAlignAccuracyFrontMatter(t *testing.T) {
	chapters := append([][]string{{aligntest.Paragraph(501, 3), aligntest.Paragraph(502, 3)}}, synBase()...)
	doc := aligntest.Doc(chapters)
	doc.Chapters[0].Front = true
	var pieces []aligntest.Piece
	for _, p := range aligntest.Pieces(doc) {
		if !strings.HasPrefix(p.SID, "c1.") { // the title and copyright pages are not read aloud
			pieces = append(pieces, p)
		}
	}
	r := synExecute(t, doc, pieces, 0)
	synNeed(t, "front-matter", r, 0.95)
	if r.res.Chapters[0].State != align.ChapterUnmatched {
		t.Errorf("front chapter state %s, want unmatched", r.res.Chapters[0].State)
	}
	if r.res.State != align.StateDone {
		t.Errorf("state %s: front matter must not count against coverage", r.res.State)
	}
}

func TestAlignAccuracySwapped(t *testing.T) {
	doc := aligntest.Doc(synBase())
	byChapter := map[string][]aligntest.Piece{}
	for _, p := range aligntest.Pieces(doc) {
		c := p.SID[:strings.Index(p.SID, ".")]
		byChapter[c] = append(byChapter[c], p)
	}
	var pieces []aligntest.Piece
	for _, c := range []string{"c1", "c5", "c3", "c4", "c2", "c6"} { // chapters 2 and 5 are read out of order
		pieces = append(pieces, byChapter[c]...)
	}
	for _, block := range []int64{0, 120000} {
		r := synExecute(t, doc, pieces, block)
		synNeed(t, "swapped", r, 0.95)
		if r.res.State != align.StateDone {
			t.Errorf("block %d: state %s, want done", block, r.res.State)
		}
	}
}

func TestAlignAccuracyAbridged(t *testing.T) {
	doc := aligntest.Doc(synBase())
	for _, tc := range []struct {
		name  string
		drop  func(n int) bool
		label bool
	}{
		{"abridged-20", func(n int) bool { return n%5 == 0 }, false},
		{"abridged-40", func(n int) bool { return n%5 == 0 || n%5 == 2 }, true},
	} {
		var pieces []aligntest.Piece
		for _, p := range aligntest.Pieces(doc) {
			if !tc.drop(p.Para) {
				pieces = append(pieces, p)
			}
		}
		r := synExecute(t, doc, pieces, 0)
		synNeed(t, tc.name, r, 0.95)
		if r.res.State != align.StatePartial {
			t.Errorf("%s: state %s, want partial", tc.name, r.res.State)
		}
		if got := synHas(r.res.Labels, align.LabelAbridged); got != tc.label {
			t.Errorf("%s: abridged label %v, want %v (coverage %.2f)", tc.name, got, tc.label, r.res.Coverage)
		}
	}
}

// synEdit returns chapters with a share of words replaced: half by a near variant (one letter changed), half
// by another word.
func synEdit(chapters [][]string, share float64, seed uint64) [][]string {
	rng := rand.New(rand.NewPCG(seed, 3))
	other := aligntest.Words(int64(seed), 400)
	out := make([][]string, len(chapters))
	for c, paras := range chapters {
		for _, p := range paras {
			words := strings.Fields(p)
			for i, w := range words {
				core := strings.TrimRight(w, ".,;:!?")
				tail := w[len(core):]
				if rng.Float64() >= share || len(core) < 4 {
					continue
				}
				if rng.IntN(2) == 0 {
					b := []byte(core)
					b[1+rng.IntN(len(b)-2)] = "ptkzmn"[rng.IntN(6)]
					words[i] = string(b) + tail
				} else {
					words[i] = other[rng.IntN(len(other))] + tail
				}
			}
			out[c] = append(out[c], strings.Join(words, " "))
		}
	}
	return out
}

func TestAlignAccuracyOtherEdition(t *testing.T) {
	base := synBase()
	audioDoc := aligntest.Doc(base)
	textDoc := aligntest.Doc(synEdit(base, 0.14, 31))
	for ci := range textDoc.Chapters { // a new paragraph the recording does not have, in each chapter
		ch := &textDoc.Chapters[ci]
		extra := aligntest.NewBlock(ch.Blocks[2].ID+"x", aligntest.Paragraph(int64(900+ci), 3))
		ch.Blocks = append(ch.Blocks[:3], append([]ebook.Block{extra}, ch.Blocks[3:]...)...)
	}
	r := synExecute(t, textDoc, aligntest.Pieces(audioDoc), 0)
	synNeed(t, "other-edition", r, 0.95)
	if !synHas(r.res.Labels, align.LabelDifferentEdition) {
		t.Errorf("labels %v, want different_edition (differing share %.3f)", r.res.Labels, r.res.NearShare)
	}
	for ci, ch := range textDoc.Chapters {
		for _, b := range ch.Blocks {
			if !strings.HasSuffix(b.ID, "x") {
				continue
			}
			for i, sid := range r.res.Chapters[ci].Timing.SID {
				if strings.HasPrefix(sid, b.ID+".") && r.res.Chapters[ci].Timing.SS[i] >= 0 {
					t.Errorf("inserted sentence %s was timed", sid)
				}
			}
		}
	}
}

func TestAlignAccuracyUnsupported(t *testing.T) {
	base := synBase()
	var year []string // sentence ids whose third word is a year in the text and three words in the audio
	for ci, paras := range base {
		for pi, p := range paras {
			if pi%4 != 1 {
				continue
			}
			words := strings.Fields(p)
			words[2] = "1999"
			paras[pi] = strings.Join(words, " ")
			year = append(year, aligntest.Doc([][]string{paras}).Chapters[0].Blocks[pi].ID)
			_ = ci
		}
	}
	doc := aligntest.Doc(base)
	for ci := range doc.Chapters { // a block of symbols nobody can read aloud
		ch := &doc.Chapters[ci]
		ch.Blocks = append(ch.Blocks[:2], append([]ebook.Block{aligntest.NewBlock(ch.Blocks[1].ID+"x", "* * *")}, ch.Blocks[2:]...)...)
	}
	// Every block id is "c1.pN" because the loop above built each chapter alone; read the real ids.
	isYear := map[string]bool{}
	for ci, ch := range doc.Chapters {
		for bi, b := range ch.Blocks {
			if strings.HasSuffix(b.ID, "x") {
				continue
			}
			if strings.Contains(b.Text, "1999") {
				isYear[ebook.SentenceID(b.ID, 0)] = true
			}
			_, _ = ci, bi
		}
	}
	var pieces []aligntest.Piece
	for _, p := range aligntest.Pieces(doc) {
		if isYear[p.SID] {
			w := append([]string{}, p.Words[:2]...)
			w = append(w, "nineteen", "ninety", "nine")
			p.Words = append(w, p.Words[3:]...)
		}
		pieces = append(pieces, p)
	}
	var symbolSIDs []string
	filtered := pieces[:0:0]
	for _, p := range pieces {
		if len(p.Words) == 0 {
			symbolSIDs = append(symbolSIDs, p.SID)
			continue // nothing to read: the narrator skips it
		}
		filtered = append(filtered, p)
	}
	if len(isYear) == 0 || len(symbolSIDs) == 0 {
		t.Fatalf("setup: %d year sentences, %d symbol sentences", len(isYear), len(symbolSIDs))
	}
	r := synExecute(t, doc, filtered, 0)
	synNeed(t, "unsupported", r, 0.95)
	_ = year
	for _, ch := range r.res.Chapters {
		for i, sid := range ch.Timing.SID {
			for _, s := range symbolSIDs {
				if sid == s && (ch.Timing.SS[i] >= 0 || ch.Timing.Gap[i] == nil || *ch.Timing.Gap[i] != align.GapUnsupported) {
					t.Errorf("%s: want gap unsupported, got start %d gap %v", sid, ch.Timing.SS[i], ch.Timing.Gap[i])
				}
			}
		}
	}
	between := 0
	for _, ch := range r.res.Chapters {
		for _, m := range ch.Timing.WM {
			if m == align.WordBetween {
				between++
			}
		}
	}
	if between == 0 {
		t.Error("no wildcard word got the between mark")
	}
}

func TestAlignAccuracyMusic(t *testing.T) {
	doc := aligntest.Doc(synBase())
	var pieces []aligntest.Piece
	for _, p := range aligntest.Pieces(doc) {
		if strings.HasPrefix(p.SID, "c4.p1.") {
			pieces = append(pieces, aligntest.Piece{Para: -2, MusicMS: 180000})
		}
		pieces = append(pieces, p)
	}
	synNeed(t, "music", synExecute(t, doc, pieces, 0), 0.95)
}

// TestAlignAccuracySeeds repeats the clean case with other books, scripts and heavier noise.
func TestAlignAccuracySeeds(t *testing.T) {
	v := aligntest.Vocab()
	for _, tc := range []struct {
		seed  int64
		noise float64
	}{{11, 0.8}, {12, 0.8}, {13, 1.0}} {
		doc := aligntest.Doc(aligntest.Chapters(tc.seed, 6, 400))
		script := aligntest.NewScript(aligntest.Pieces(doc), tc.seed)
		m, truth := aligntest.Gen(script, v, tc.noise, tc.seed)
		res, err := align.Align(align.Input{
			Text: align.BuildText(doc, "en", v), Vocab: v, Src: align.MemSource{M: m},
			Track: script.Track("h"), ModelSHA: "m", Params: align.DefaultParams(),
		})
		if err != nil {
			t.Fatal(err)
		}
		share := synCheck(t, res, truth)
		t.Logf("seed %d noise %.1f accuracy %.2f%% state=%s", tc.seed, tc.noise, 100*share, res.State)
		if share < 0.99 {
			t.Errorf("seed %d noise %.1f: accuracy %.2f%%", tc.seed, tc.noise, 100*share)
		}
	}
}
