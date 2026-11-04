package align

import (
	"sort"
	"strings"
)

// Pair says that heard word Heard is edition unit Unit. Exact is false for a near match (edit distance of at
// most one per four letters).
type Pair struct {
	Heard, Unit int
	Exact       bool
}

// Region is one stretch of audio mapped to one stretch of text (inclusive ends, first and last pair).
type Region struct{ HeardFrom, HeardTo, UnitFrom, UnitTo int }

// Mapping is the coarse map of the audio onto the edition. SpeechMapped is the share of heard words that lie
// inside a region. NearShare is the share of text between anchors that differs from what was said where
// something was said (substituted words, near matches), a sign of another edition.
type Mapping struct {
	Pairs        []Pair
	Regions      []Region
	SpeechMapped float64
	NearShare    float64
}

type hit struct{ h, u int }

// Map maps heard words onto the edition. The audio is cut into blocks of about BlockMs at the nearest pause.
// Each block looks up its 5-grams among the edition's 5-grams that occur once; the longest increasing chain of
// hits is one region, its hits are removed, and the search repeats while MinHits remain, up to MaxRegions. A
// block may therefore map to several places in the text (swapped chapters, text read out of order), and a
// block with too few hits is audio without text. Between the chain's anchors a word-level Needleman-Wunsch
// finds the rest of the pairs.
func Map(p Params, text Text, heard []Heard, pauses []Pause, totalFrames int64) Mapping {
	grams := editionGrams(text, 5)
	var (
		mp        Mapping
		spanUnits int
		covered   = make([]int, len(heard)+1)
		all       []Pair
		gaps      []gapRec
	)
	for _, blk := range cutBlocks(p, pauses, totalFrames) {
		a := sort.Search(len(heard), func(i int) bool { return heard[i].StartF >= blk[0] })
		b := sort.Search(len(heard), func(i int) bool { return heard[i].StartF >= blk[1] })
		var hits []hit
		for i := a; i+5 <= b; i++ {
			if u, ok := grams[heardKey(heard[i:i+5])]; ok && u >= 0 {
				hits = append(hits, hit{i, u})
			}
		}
		for r := 0; r < p.MaxRegions && len(hits) >= p.MinHits; r++ {
			chain := longestIncreasing(hits)
			if len(chain) < p.MinHits {
				break
			}
			used := make(map[hit]bool, len(chain))
			for _, h := range chain {
				used[h] = true
			}
			rest := hits[:0:0]
			for _, h := range hits {
				if !used[h] {
					rest = append(rest, h)
				}
			}
			hits = rest
			pairs := expandHits(chain, 5)
			full, g, s := fillChain(p, text, heard, pairs)
			gaps = append(gaps, g...)
			spanUnits += s
			all = append(all, full...)
			reg := Region{
				HeardFrom: full[0].Heard, HeardTo: full[len(full)-1].Heard,
				UnitFrom: full[0].Unit, UnitTo: full[len(full)-1].Unit,
			}
			mp.Regions = append(mp.Regions, reg)
			covered[reg.HeardFrom]++
			covered[reg.HeardTo+1]--
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Heard < all[j].Heard })
	for _, pr := range all {
		if n := len(mp.Pairs); n > 0 && mp.Pairs[n-1].Heard == pr.Heard {
			continue
		}
		mp.Pairs = append(mp.Pairs, pr)
	}
	// A gap counts as a difference only for the units whose audio was said and claimed by no region: a
	// word replaced by another. Text nobody read (abridged) and audio another region took (swapped chapters)
	// are not differences.
	paired := make([]bool, len(heard))
	for _, pr := range all {
		paired[pr.Heard] = pr.Exact || paired[pr.Heard] // a near match is still a difference
	}
	diff := 0
	for _, g := range gaps {
		free := 0
		for h := g.h0; h < g.h1; h++ {
			if !paired[h] {
				free++
			}
		}
		diff += min(g.differ, free)
	}
	in, run := 0, 0
	for i := range heard {
		run += covered[i]
		if run > 0 {
			in++
		}
	}
	if len(heard) > 0 {
		mp.SpeechMapped = float64(in) / float64(len(heard))
	}
	if spanUnits > 0 {
		mp.NearShare = float64(diff) / float64(spanUnits)
	}
	return mp
}

func heardKey(h []Heard) string {
	var sb strings.Builder
	for i, w := range h {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(w.Norm)
	}
	return sb.String()
}

// editionGrams maps every n-gram of consecutive non-wildcard units inside one chapter to the index of its
// first unit, or to -1 when it occurs more than once.
func editionGrams(text Text, n int) map[string]int {
	out := map[string]int{}
	for i := 0; i+n <= len(text.Units); i++ {
		ok := true
		var sb strings.Builder
		for k := 0; k < n; k++ {
			u := text.Units[i+k]
			if u.Wildcard || u.Chapter != text.Units[i].Chapter {
				ok = false
				break
			}
			if k > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(u.Norm)
		}
		if !ok {
			continue
		}
		key := sb.String()
		if _, dup := out[key]; dup {
			out[key] = -1
		} else {
			out[key] = i
		}
	}
	return out
}

// cutBlocks splits [0,total) into blocks of about BlockMs, each cut at the pause middle nearest the target.
func cutBlocks(p Params, pauses []Pause, total int64) [][2]int64 {
	size := p.BlockMs / FrameMs
	if size < 1 {
		size = 1
	}
	var out [][2]int64
	start := int64(0)
	for start < total {
		target := start + size
		if target >= total {
			out = append(out, [2]int64{start, total})
			break
		}
		cut, bestD := target, size/4+1
		for _, ps := range pauses {
			mid := (ps.StartF + ps.EndF) / 2
			if mid <= start {
				continue
			}
			d := mid - target
			if d < 0 {
				d = -d
			}
			if d < bestD {
				cut, bestD = mid, d
			}
			if mid > target+size/4 {
				break
			}
		}
		out = append(out, [2]int64{start, cut})
		start = cut
	}
	return out
}

// longestIncreasing returns the longest chain of hits whose units strictly increase (hits come in heard order).
func longestIncreasing(hits []hit) []hit {
	if len(hits) == 0 {
		return nil
	}
	tails := []int{} // index of the hit ending the best chain of each length
	parent := make([]int, len(hits))
	for i, h := range hits {
		k := sort.Search(len(tails), func(j int) bool { return hits[tails[j]].u >= h.u })
		if k > 0 {
			parent[i] = tails[k-1]
		} else {
			parent[i] = -1
		}
		if k == len(tails) {
			tails = append(tails, i)
		} else {
			tails[k] = i
		}
	}
	chain := make([]hit, len(tails))
	for i, k := len(tails)-1, tails[len(tails)-1]; i >= 0; i, k = i-1, parent[k] {
		chain[i] = hits[k]
	}
	return chain
}

// expandHits turns each n-gram hit into n exact pairs, keeping the sequence strictly increasing.
func expandHits(chain []hit, n int) []Pair {
	var out []Pair
	lastH, lastU := -1, -1
	for _, c := range chain {
		for k := 0; k < n; k++ {
			h, u := c.h+k, c.u+k
			if h > lastH && u > lastU {
				out = append(out, Pair{Heard: h, Unit: u, Exact: true})
				lastH, lastU = h, u
			}
		}
	}
	return out
}

// gapRec is a stretch between two anchors with text and audio on both sides: heard[h0:h1] was said where the
// text has differ units that no pair explains.
type gapRec struct{ h0, h1, differ int }

// fillChain adds the pairs found between consecutive anchors. It also returns the gaps that may be
// differences and how many units the chain spans.
func fillChain(p Params, text Text, heard []Heard, anchors []Pair) (out []Pair, gaps []gapRec, span int) {
	for i, a := range anchors {
		out = append(out, a)
		if i+1 == len(anchors) {
			break
		}
		b := anchors[i+1]
		span += b.Unit - a.Unit
		gapU, gapH := b.Unit-a.Unit-1, b.Heard-a.Heard-1
		if gapU <= 0 || gapH <= 0 {
			continue
		}
		inner := fillGap(p, text, heard, a.Heard+1, b.Heard, a.Unit+1, b.Unit, 0)
		exact := 0
		for _, q := range inner {
			if q.Exact {
				exact++
			}
		}
		wild := 0
		for u := a.Unit + 1; u < b.Unit; u++ {
			if text.Units[u].Wildcard {
				wild++
			}
		}
		if d := gapU - exact - wild; d > 0 {
			gaps = append(gaps, gapRec{h0: a.Heard + 1, h1: b.Heard, differ: d})
		}
		out = append(out, inner...)
	}
	return out, gaps, span
}

// fillGap pairs heard[h0:h1] with units[u0:u1]. A gap within the cell bound goes to Needleman-Wunsch; a larger
// one first re-anchors on 3-grams that are unique in the gap on both sides; what is still too large stays
// unpaired.
func fillGap(p Params, text Text, heard []Heard, h0, h1, u0, u1, depth int) []Pair {
	n, m := h1-h0, u1-u0
	if n <= 0 || m <= 0 {
		return nil
	}
	if (n+1)*(m+1) <= p.NWMaxCells {
		return needlemanWunsch(text, heard, h0, h1, u0, u1)
	}
	if depth > 0 {
		return nil
	}
	hg := map[string]int{}
	for i := h0; i+3 <= h1; i++ {
		k := heardKey(heard[i : i+3])
		if _, dup := hg[k]; dup {
			hg[k] = -1
		} else {
			hg[k] = i
		}
	}
	ug := map[string]int{}
	for i := u0; i+3 <= u1; i++ {
		var sb strings.Builder
		ok := true
		for k := 0; k < 3; k++ {
			if text.Units[i+k].Wildcard {
				ok = false
				break
			}
			if k > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(text.Units[i+k].Norm)
		}
		if !ok {
			continue
		}
		key := sb.String()
		if _, dup := ug[key]; dup {
			ug[key] = -1
		} else {
			ug[key] = i
		}
	}
	var hits []hit
	for i := h0; i+3 <= h1; i++ {
		k := heardKey(heard[i : i+3])
		if hi, ok := hg[k]; ok && hi >= 0 {
			if u, ok := ug[k]; ok && u >= 0 {
				hits = append(hits, hit{i, u})
			}
		}
	}
	chain := longestIncreasing(hits)
	if len(chain) == 0 {
		return nil
	}
	anchors := expandHits(chain, 3)
	var out []Pair
	ph, pu := h0, u0
	for _, a := range anchors {
		out = append(out, fillGap(p, text, heard, ph, a.Heard, pu, a.Unit, 1)...)
		out = append(out, a)
		ph, pu = a.Heard+1, a.Unit+1
	}
	out = append(out, fillGap(p, text, heard, ph, h1, pu, u1, 1)...)
	return out
}

// near reports whether two words differ by at most one edit per four letters of the longer.
func near(a, b string) bool {
	limit := len([]rune(a))
	if l := len([]rune(b)); l > limit {
		limit = l
	}
	limit /= 4
	if limit == 0 {
		return false
	}
	return levenshtein([]rune(a), []rune(b), limit) <= limit
}

// levenshtein is the edit distance, or limit+1 when it exceeds limit.
func levenshtein(a, b []rune, limit int) int {
	if d := len(a) - len(b); d > limit || -d > limit {
		return limit + 1
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			c := 1
			if a[i-1] == b[j-1] {
				c = 0
			}
			cur[j] = min(prev[j-1]+c, prev[j]+1, cur[j-1]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// needlemanWunsch aligns heard[h0:h1] with units[u0:u1] word by word: exact +2, near +1, wildcard 0, other
// mismatch -1, gap -1. It returns the exact and near pairs.
func needlemanWunsch(text Text, heard []Heard, h0, h1, u0, u1 int) []Pair {
	n, m := h1-h0, u1-u0
	ptr := make([]uint8, (n+1)*(m+1)) // 1 diagonal, 2 up (heard skipped), 3 left (unit skipped)
	prev := make([]int32, m+1)
	cur := make([]int32, m+1)
	for j := 1; j <= m; j++ {
		prev[j] = -int32(j)
		ptr[j] = 3
	}
	for i := 1; i <= n; i++ {
		cur[0] = -int32(i)
		ptr[i*(m+1)] = 2
		hw := heard[h0+i-1].Norm
		for j := 1; j <= m; j++ {
			u := text.Units[u0+j-1]
			var s int32 = -1
			switch {
			case u.Wildcard:
				s = 0
			case u.Norm == hw:
				s = 2
			case near(u.Norm, hw):
				s = 1
			}
			best, dir := prev[j-1]+s, uint8(1)
			if v := prev[j] - 1; v > best {
				best, dir = v, 2
			}
			if v := cur[j-1] - 1; v > best {
				best, dir = v, 3
			}
			cur[j] = best
			ptr[i*(m+1)+j] = dir
		}
		prev, cur = cur, prev
	}
	var out []Pair
	i, j := n, m
	for i > 0 || j > 0 {
		switch ptr[i*(m+1)+j] {
		case 1:
			u := text.Units[u0+j-1]
			hw := heard[h0+i-1].Norm
			if !u.Wildcard {
				if u.Norm == hw {
					out = append(out, Pair{Heard: h0 + i - 1, Unit: u0 + j - 1, Exact: true})
				} else if near(u.Norm, hw) {
					out = append(out, Pair{Heard: h0 + i - 1, Unit: u0 + j - 1})
				}
			}
			i, j = i-1, j-1
		case 2:
			i--
		default:
			j--
		}
	}
	for l, r := 0, len(out)-1; l < r; l, r = l+1, r-1 {
		out[l], out[r] = out[r], out[l]
	}
	return out
}
