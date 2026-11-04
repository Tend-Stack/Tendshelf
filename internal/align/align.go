package align

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"sort"
	"strings"
)

// Input is everything Align needs. Reuse is asked, once per chapter whose audio was mapped, for a finished
// timing under the chapter key; a hit skips that chapter's Viterbi runs.
type Input struct {
	Text     Text
	Vocab    Vocab
	Src      Source
	Track    Track
	ModelSHA string
	Params   Params
	Reuse    func(chapter int, key string) (*Timing, bool)
	// Stop, if set, is asked before every Viterbi segment; an error ends Align with it.
	Stop func() error
	// OnChapter, if set, gets each chapter as soon as its last segment is done, in document order, so a job
	// can save it before the next one is computed. An error ends Align with it.
	OnChapter func(ChapterResult) error
}

// ChapterResult is one chapter. Idx is the document's chapter number. State is ChapterAligned when at least
// one sentence has a time, else ChapterUnmatched (Timing is nil when no audio was mapped to it, and Key is
// then empty). StartMS and EndMS are -1 without times.
type ChapterResult struct {
	Idx            int
	Front          bool
	Key, State     string
	Timing         *Timing
	Total, Timed   int
	StartMS, EndMS int64
	MeanConfidence float64
	Reused         bool
}

// Result is the outcome of Align. Total and Timed count body (non-front) sentences. State is StateDone,
// StatePartial or StateFailed (with Code CodeNoMatch).
type Result struct {
	Chapters     []ChapterResult
	Total, Timed int
	Coverage     float64
	Labels       []string
	State, Code  string
	ViterbiRuns  int
	SpeechMapped float64
	NearShare    float64
}

// chunk is one Viterbi job: units [u0,u1) over global frames [f0,f1).
type chunk struct {
	u0, u1 int
	f0, f1 int64
}

type unitRes struct {
	chunk     int // -1 when no chunk holds the unit
	attempted bool
	ok        bool
	s, e      int64
	score     float64
}

// Align maps the narration onto the edition and times every word it can measure. It never interpolates: a
// word has a time only when forced alignment scored it at TimedMin or more, and every sentence without one
// carries the reason.
func Align(in Input) (Result, error) {
	p := in.Params
	if p.BlockMs == 0 {
		p = DefaultParams()
	}
	total := in.Track.Frames()
	heard, pauses, err := greedy(in.Src, in.Track, in.Vocab, p.PauseFrames)
	if err != nil {
		return Result{}, err
	}
	mp := Map(p, in.Text, heard, pauses, total)
	exact := make([]bool, len(in.Text.Units))
	var anchors []Pair
	for _, pr := range mp.Pairs {
		if pr.Exact {
			exact[pr.Unit] = true
			anchors = append(anchors, pr)
		}
	}
	chunks := buildChunks(p, in.Text, heard, anchors, total)
	keys := chapterKeys(in, heard, anchors)

	res := Result{SpeechMapped: mp.SpeechMapped, NearShare: mp.NearShare}
	reused := make([]*Timing, len(in.Text.Chapters))
	for ci, key := range keys {
		if key == "" || in.Reuse == nil {
			continue
		}
		if t, ok := in.Reuse(in.Text.Chapters[ci].Idx, key); ok && t != nil {
			reused[ci] = t
		}
	}

	units := make([]unitRes, len(in.Text.Units))
	for i := range units {
		units[i].chunk = -1
	}
	// minU0[i] is the smallest first unit among chunks i.., so a chapter is finished once every remaining
	// chunk starts after it.
	minU0 := make([]int, len(chunks)+1)
	minU0[len(chunks)] = len(in.Text.Units)
	for i := len(chunks) - 1; i >= 0; i-- {
		minU0[i] = min(chunks[i].u0, minU0[i+1])
	}
	built := make([]ChapterResult, len(in.Text.Chapters))
	next := 0
	flush := func(limit int) error {
		for next < len(in.Text.Chapters) && in.Text.Chapters[next].EndUnit <= limit {
			cr := buildChapter(in, p, next, keys, reused, units, exact)
			built[next] = cr
			next++
			if in.OnChapter != nil {
				if err := in.OnChapter(cr); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for id, c := range chunks {
		if err := flush(minU0[id]); err != nil {
			return Result{}, err
		}
		if in.Stop != nil {
			if err := in.Stop(); err != nil {
				return Result{}, err
			}
		}
		need := false
		for u := c.u0; u < c.u1; u++ {
			units[u].chunk = id
			if reused[in.Text.Units[u].Chapter] == nil && keys[in.Text.Units[u].Chapter] != "" {
				need = true
			}
		}
		if !need {
			continue
		}
		n := c.u1 - c.u0
		if n >= 8 && (float64(n)/(float64(c.f1-c.f0)*FrameMs/1000) < p.RateMin ||
			float64(n)/(float64(c.f1-c.f0)*FrameMs/1000) > p.RateMax) {
			continue
		}
		m, err := in.Src.Frames(c.f0, c.f1)
		if err != nil {
			return Result{}, err
		}
		toks := make([][]int, n)
		wild := make([]bool, n)
		for k := 0; k < n; k++ {
			toks[k] = in.Text.Units[c.u0+k].Tokens
			wild[k] = in.Text.Units[c.u0+k].Wildcard
		}
		res.ViterbiRuns++
		spans, err := ForceAlign(m, toks, wild, in.Vocab)
		for k := 0; k < n; k++ {
			units[c.u0+k].attempted = true
		}
		if errors.Is(err, ErrTooShort) {
			continue
		}
		if err != nil {
			return Result{}, err
		}
		for k, sp := range spans {
			r := &units[c.u0+k]
			r.ok = true
			r.s, r.e, r.score = c.f0+sp.StartF, c.f0+sp.EndF, sp.Score
		}
	}

	if err := flush(len(in.Text.Units) + 1); err != nil {
		return Result{}, err
	}
	for ci, tc := range in.Text.Chapters {
		cr := built[ci]
		if !tc.Front {
			res.Total += cr.Total
			res.Timed += cr.Timed
		}
		res.Chapters = append(res.Chapters, cr)
	}
	for ci, tc := range in.Text.Chapters {
		if res.Chapters[ci].Timing == nil && !tc.Front {
			res.Total += tc.EndSent - tc.FirstSent
		}
	}
	finishResult(&res, p)
	return res, nil
}

// buildChapter assembles chapter ci from the finished units.
func buildChapter(in Input, p Params, ci int, keys []string, reused []*Timing, units []unitRes, exact []bool) ChapterResult {
	tc := in.Text.Chapters[ci]
	cr := ChapterResult{Idx: tc.Idx, Front: tc.Front, Key: keys[ci], StartMS: -1, EndMS: -1, State: ChapterUnmatched}
	switch {
	case reused[ci] != nil:
		cr.Timing, cr.Reused = reused[ci], true
	case keys[ci] != "":
		cr.Timing = buildTiming(in, p, ci, units, exact)
	}
	if cr.Timing != nil {
		chapterStats(&cr)
		if cr.Timed > 0 {
			cr.State = ChapterAligned
		}
	}
	return cr
}

func finishResult(res *Result, p Params) {
	if res.Total > 0 {
		res.Coverage = float64(res.Timed) / float64(res.Total)
	}
	switch {
	case res.Total == 0 || res.Coverage < p.PartialMin:
		res.State, res.Code = StateFailed, CodeNoMatch
	case res.Coverage < p.DoneMin:
		res.State = StatePartial
	default:
		res.State = StateDone
	}
	if res.State == StateFailed {
		return
	}
	if res.SpeechMapped >= p.AbridgedSpeech && res.Coverage < p.AbridgedText {
		res.Labels = append(res.Labels, LabelAbridged)
	}
	if res.NearShare > p.EditionNear {
		res.Labels = append(res.Labels, LabelDifferentEdition)
	}
	sort.Strings(res.Labels)
}

// chapterStats fills the counts of a chapter from its timing.
func chapterStats(cr *ChapterResult) {
	t := cr.Timing
	cr.Total = len(t.SID)
	var sum float64
	for i := range t.SID {
		if t.SS[i] < 0 {
			continue
		}
		cr.Timed++
		sum += t.SC[i]
		if cr.StartMS < 0 || t.SS[i] < cr.StartMS {
			cr.StartMS = t.SS[i]
		}
		if t.SE[i] > cr.EndMS {
			cr.EndMS = t.SE[i]
		}
	}
	if cr.Timed > 0 {
		cr.MeanConfidence = sum / float64(cr.Timed)
	}
}

// linked reports whether the audio and text between two anchors can be forced together: the text runs on, the
// gap is short enough for one segment and its words fit the audio at a speakable rate.
func linked(p Params, heard []Heard, a, b Pair) bool {
	if b.Unit <= a.Unit {
		return false
	}
	gapF := heard[b.Heard].StartF - heard[a.Heard].EndF
	if gapF < 0 {
		gapF = 0
	}
	durMs := (gapF + 2*int64(p.PadFrames)) * FrameMs
	if durMs > p.SegMaxMs {
		return false
	}
	gapU := b.Unit - a.Unit - 1
	if gapU == 0 {
		return true
	}
	rate := float64(gapU) / (float64(durMs) / 1000)
	return rate >= p.RateMin && rate <= p.RateMax
}

// buildChunks cuts the chain of anchors into Viterbi segments: neighbours that are linked share a segment
// while it stays within SegMaxMs; an unlinked gap ends the segment at its last anchor's word.
func buildChunks(p Params, text Text, heard []Heard, ex []Pair, total int64) []chunk {
	var out []chunk
	segMax := p.SegMaxMs / FrameMs
	pad := int64(p.PadFrames)
	cur, adjacent := -1, false
	for k := range ex {
		if cur < 0 {
			cur, adjacent = k, false
		}
		start := func() int64 {
			f0 := heard[ex[cur].Heard].StartF
			if !adjacent {
				f0 -= pad
			}
			if f0 < 0 {
				f0 = 0
			}
			if n := len(out); n > 0 && out[n-1].f1 > f0 {
				f0 = out[n-1].f1
			}
			return f0
		}
		if k+1 >= len(ex) || !linked(p, heard, ex[k], ex[k+1]) {
			f1 := heard[ex[k].Heard].EndF + pad
			if k+1 < len(ex) && f1 > heard[ex[k+1].Heard].StartF {
				f1 = heard[ex[k+1].Heard].StartF
			}
			if f1 > total {
				f1 = total
			}
			out = append(out, chunk{u0: ex[cur].Unit, u1: ex[k].Unit + 1, f0: start(), f1: f1})
			cur, adjacent = -1, false
			continue
		}
		if heard[ex[k+1].Heard].EndF-heard[ex[cur].Heard].StartF > segMax {
			out = append(out, chunk{u0: ex[cur].Unit, u1: ex[k+1].Unit, f0: start(), f1: heard[ex[k+1].Heard].StartF})
			cur, adjacent = k+1, true
		}
	}
	// Drop segments the clipping left empty.
	kept := out[:0]
	for _, c := range out {
		if c.f1 > c.f0 && c.u1 > c.u0 {
			kept = append(kept, c)
		}
	}
	return kept
}

// chapterKeys gives each chapter its content address, or "" when no audio was mapped to it.
func chapterKeys(in Input, heard []Heard, anchors []Pair) []string {
	files := make([]map[string]bool, len(in.Text.Chapters))
	for _, a := range anchors {
		ci := in.Text.Units[a.Unit].Chapter
		fi, _ := in.Track.FileAt(heard[a.Heard].StartF)
		if fi < 0 {
			continue
		}
		if files[ci] == nil {
			files[ci] = map[string]bool{}
		}
		files[ci][in.Track.Files[fi].QuickHash] = true
	}
	keys := make([]string, len(in.Text.Chapters))
	for ci, tc := range in.Text.Chapters {
		if len(files[ci]) == 0 {
			continue
		}
		norms := make([]string, 0, tc.EndUnit-tc.FirstUnit)
		for _, u := range in.Text.Units[tc.FirstUnit:tc.EndUnit] {
			norms = append(norms, u.Norm)
		}
		textSum := sha256.Sum256([]byte(strings.Join(norms, " ")))
		hashes := make([]string, 0, len(files[ci]))
		for h := range files[ci] {
			hashes = append(hashes, h)
		}
		sort.Strings(hashes)
		sum := sha256.Sum256([]byte(RecipeAlign + "\n" + in.ModelSHA + "\n" + hex.EncodeToString(textSum[:]) + "\n" + strings.Join(hashes, ",")))
		keys[ci] = hex.EncodeToString(sum[:])
	}
	return keys
}

// buildTiming turns unit results into one chapter's Timing. A unit is timed when its score reaches TimedMin;
// a later unit that starts before the previous timed one ended is dropped, so times never run backwards.
func buildTiming(in Input, p Params, ci int, units []unitRes, exact []bool) *Timing {
	tc := in.Text.Chapters[ci]
	kept := make([]bool, len(units))
	var lastEnd int64 = -1
	for u := tc.FirstUnit; u < tc.EndUnit; u++ {
		r := units[u]
		if in.Text.Units[u].Wildcard || !r.ok || r.score < p.TimedMin || r.e <= r.s {
			continue
		}
		if r.s < lastEnd {
			continue
		}
		kept[u], lastEnd = true, r.e
	}
	// A wildcard between two kept neighbours of one segment borrows their measured edges.
	between := make([][2]int64, len(units))
	isBetween := make([]bool, len(units))
	for u := tc.FirstUnit; u < tc.EndUnit; u++ {
		if !in.Text.Units[u].Wildcard {
			continue
		}
		lo := u
		for lo >= tc.FirstUnit && in.Text.Units[lo].Wildcard {
			lo--
		}
		hi := u
		for hi < tc.EndUnit && in.Text.Units[hi].Wildcard {
			hi++
		}
		if lo < tc.FirstUnit || hi >= tc.EndUnit || !kept[lo] || !kept[hi] ||
			units[lo].chunk < 0 || units[lo].chunk != units[hi].chunk || units[hi].s < units[lo].e {
			continue
		}
		isBetween[u] = true
		between[u] = [2]int64{in.Track.TimeMs(units[lo].e), in.Track.TimeMs(units[hi].s)}
	}

	t := &Timing{V: TimingV, Chapter: tc.Idx}
	sentIdx := map[[2]int]int{}
	for si := tc.FirstSent; si < tc.EndSent; si++ {
		s := in.Text.Sentences[si]
		sentIdx[[2]int{s.Block, s.Sent}] = si - tc.FirstSent
		t.SID = append(t.SID, s.SID)
		var nKept, nWild, nExact int
		var sum float64
		first, last := -1, -1
		attempted := false
		for u := s.First; u < s.End; u++ {
			if units[u].attempted {
				attempted = true
			}
			if in.Text.Units[u].Wildcard {
				nWild++
				continue
			}
			if exact[u] {
				nExact++
			}
			if kept[u] {
				nKept++
				sum += units[u].score
				if first < 0 {
					first = u
				}
				last = u
			}
		}
		nWords := s.End - s.First - nWild
		switch {
		case nKept > 0:
			conf := 0.5*sum/float64(nKept) + 0.3*float64(nKept)/float64(nWords) + 0.2*float64(nExact)/float64(nWords)
			t.SS = append(t.SS, in.Track.TimeMs(units[first].s))
			t.SE = append(t.SE, in.Track.TimeMs(units[last].e))
			t.SC = append(t.SC, math.Round(math.Min(conf, 1)*1000)/1000)
			t.Gap = append(t.Gap, nil)
		default:
			reason := GapNotInRecording
			if nWords == 0 {
				reason = GapUnsupported
			} else if attempted {
				reason = GapNotFound
			}
			t.SS, t.SE, t.SC = append(t.SS, -1), append(t.SE, -1), append(t.SC, -1)
			t.Gap = append(t.Gap, &reason)
		}
	}
	// Words: one entry per source word; its units share a block and a word index.
	for u := tc.FirstUnit; u < tc.EndUnit; {
		tu := in.Text.Units[u]
		end := u + 1
		for end < tc.EndUnit && in.Text.Units[end].Block == tu.Block && in.Text.Units[end].Word == tu.Word {
			end++
		}
		w0, w1, mark := int64(-1), int64(-1), WordNotTimed
		for k := u; k < end; k++ {
			switch {
			case kept[k]:
				if mark != WordTimed {
					w0 = in.Track.TimeMs(units[k].s)
				}
				w1, mark = in.Track.TimeMs(units[k].e), WordTimed
			case isBetween[k] && mark == WordNotTimed:
				w0, w1, mark = between[k][0], between[k][1], WordBetween
			}
		}
		t.WS = append(t.WS, sentIdx[[2]int{tu.Block, tu.Sentence}])
		t.WO0, t.WO1 = append(t.WO0, tu.Off0), append(t.WO1, tu.Off1)
		t.W0, t.W1, t.WM = append(t.W0, w0), append(t.W1, w1), append(t.WM, mark)
		u = end
	}
	return t
}
