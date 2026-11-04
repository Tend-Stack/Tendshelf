package align

import (
	"errors"
	"math"
)

// ErrTooShort means the audio has fewer frames than the tokens need.
var ErrTooShort = errors.New("align: too few frames for the words")

// Span is where one unit sits in a matrix: frames [StartF, EndF) relative to the matrix, and the mean
// probability of its tokens' frames (0 for a wildcard, which has no measured score).
type Span struct {
	StartF, EndF int64
	Score        float64
}

const (
	negInf   = float32(-1e30)
	itemStar = -1
)

// ForceAlign finds, by CTC Viterbi over the 2L+1 blank-interleaved states, where each unit's tokens sit in m.
// A delimiter token separates neighbouring units. A wildcard unit is one star state that may take any frames
// (letters, blanks and delimiters alike, so a spoken year of several words is swallowed whole) and never
// carries a score. Every unit takes at least one frame.
func ForceAlign(m Matrix, units [][]int, wild []bool, v Vocab) ([]Span, error) {
	if len(units) == 0 {
		return nil, nil
	}
	var item, owner []int
	for u, toks := range units {
		if u > 0 {
			item = append(item, v.Delim)
			owner = append(owner, -1)
		}
		if wild[u] || len(toks) == 0 {
			item = append(item, itemStar)
			owner = append(owner, u)
			continue
		}
		for _, tk := range toks {
			item = append(item, tk)
			owner = append(owner, u)
		}
	}
	L := len(item)
	S := 2*L + 1
	need := L
	for j := 1; j < L; j++ {
		if item[j] == item[j-1] && item[j] != itemStar {
			need++
		}
	}
	if m.T < need {
		return nil, ErrTooShort
	}
	T := m.T
	canSkip := make([]bool, L)
	for j := 1; j < L; j++ {
		canSkip[j] = item[j] == itemStar || item[j-1] == itemStar || item[j] != item[j-1]
	}
	best := make([]float32, T) // best symbol log-probability of each frame, for star states
	for t := 0; t < T; t++ {
		row := m.Row(t)
		b := row[0]
		for _, x := range row[1:] {
			if x > b {
				b = x
			}
		}
		best[t] = b
	}
	emit := func(t, s int) float32 {
		if s%2 == 0 {
			return m.LogP[t*m.V+v.Blank]
		}
		it := item[s/2]
		if it == itemStar {
			return best[t]
		}
		return m.LogP[t*m.V+it]
	}
	back := make([]uint8, T*S)
	prev := make([]float32, S)
	cur := make([]float32, S)
	for s := range prev {
		prev[s] = negInf
	}
	prev[0] = emit(0, 0)
	if S > 1 {
		prev[1] = emit(0, 1)
	}
	for t := 1; t < T; t++ {
		lo := S - 2 - 2*(T-1-t)
		if lo < 0 {
			lo = 0
		}
		hi := 2*t + 1
		if hi > S-1 {
			hi = S - 1
		}
		for s := range cur {
			cur[s] = negInf
		}
		for s := lo; s <= hi; s++ {
			bestv, from := prev[s], uint8(0)
			if s >= 1 && prev[s-1] > bestv {
				bestv, from = prev[s-1], 1
			}
			if s >= 2 && s%2 == 1 && canSkip[s/2] && prev[s-2] > bestv {
				bestv, from = prev[s-2], 2
			}
			if bestv <= negInf {
				continue
			}
			cur[s] = bestv + emit(t, s)
			back[t*S+s] = from
		}
		prev, cur = cur, prev
	}
	s := S - 1
	if S >= 2 && prev[S-2] > prev[S-1] {
		s = S - 2
	}
	if prev[s] <= negInf {
		return nil, ErrTooShort
	}
	path := make([]int, T)
	for t := T - 1; t >= 0; t-- {
		path[t] = s
		s -= int(back[t*S+s])
	}
	spans := make([]Span, len(units))
	sums := make([]float64, len(units))
	counts := make([]int, len(units))
	seen := make([]bool, len(units))
	for t, st := range path {
		if st%2 == 0 {
			continue
		}
		u := owner[st/2]
		if u < 0 {
			continue
		}
		if !seen[u] {
			seen[u] = true
			spans[u].StartF = int64(t)
		}
		spans[u].EndF = int64(t) + 1
		if item[st/2] != itemStar {
			sums[u] += math.Exp(float64(m.LogP[t*m.V+item[st/2]]))
			counts[u]++
		}
	}
	for u := range spans {
		if counts[u] > 0 {
			spans[u].Score = sums[u] / float64(counts[u])
		}
	}
	return spans, nil
}
