package aligntest

import (
	"math"
	"math/rand/v2"

	"github.com/Tend-Stack/tendshelf/internal/align"
)

// Vocab is the 32-symbol upper-case table of wav2vec2-base-960h: pad (the CTC blank), s, /s, unk, the word
// delimiter and the letters.
func Vocab() align.Vocab {
	syms := []string{"<pad>", "<s>", "</s>", "<unk>", "|", "E", "T", "A", "O", "N", "I", "H", "S", "R", "D", "L",
		"U", "M", "W", "C", "F", "G", "Y", "P", "B", "V", "K", "'", "X", "J", "Q", "Z"}
	v, err := align.NewVocab(syms, 0, "|")
	if err != nil {
		panic(err)
	}
	return v
}

// Gen makes the emissions of a script: 20 ms frames of log-probabilities. In a spoken word the letters'
// peaks (probability 0.85) are spread evenly over its frames and the delimiter peaks in its last frame; every
// other frame is blank. Music frames carry random low peaks. Gaussian noise of the given spread is added to
// the logits before the log-softmax. The same seed gives the same matrix.
func Gen(s Script, v align.Vocab, noise float64, seed int64) (align.Matrix, Truth) {
	rng := rand.New(rand.NewPCG(uint64(seed), 23))
	frames := int(s.TotalMS / frameMs)
	V := v.Size()
	peak := make([]int, frames) // symbol with the high probability in each frame
	mass := make([]float64, frames)
	for t := range peak {
		peak[t], mass[t] = v.Blank, 0.85
	}
	letters := []int{}
	for id := 0; id < V; id++ {
		if v.IsLetter(id) {
			letters = append(letters, id)
		}
	}
	for _, it := range s.Items {
		if it.MusicMS > 0 {
			for t := int(it.StartMS / frameMs); t < int(it.EndMS/frameMs) && t < frames; t++ {
				if rng.Float64() < 0.3 {
					peak[t], mass[t] = letters[rng.IntN(len(letters))], 0.35
				} else {
					mass[t] = 0.4
				}
			}
			continue
		}
		for _, w := range it.Spoken {
			toks, _, ok := v.Encode(w.Text)
			if !ok {
				continue
			}
			s0, n := int(w.StartMS/frameMs), int((w.EndMS-w.StartMS)/frameMs)
			for i, tk := range toks {
				t := s0 + i*(n-1)/len(toks)
				if t < frames {
					peak[t] = tk
				}
			}
			if s0+n-1 < frames {
				peak[s0+n-1] = v.Delim
			}
		}
	}
	m := align.Matrix{T: frames, V: V, LogP: make([]float32, frames*V)}
	logits := make([]float64, V)
	for t := 0; t < frames; t++ {
		rest := math.Log((1 - mass[t]) / float64(V-1))
		for k := range logits {
			logits[k] = rest
		}
		logits[peak[t]] = math.Log(mass[t])
		if peak[t] != v.Blank && mass[t] < 0.5 {
			logits[v.Blank] = math.Log(0.3)
		}
		mx := math.Inf(-1)
		for k := range logits {
			logits[k] += noise * rng.NormFloat64()
			mx = math.Max(mx, logits[k])
		}
		var sum float64
		for _, x := range logits {
			sum += math.Exp(x - mx)
		}
		lse := mx + math.Log(sum)
		row := m.Row(t)
		for k, x := range logits {
			row[k] = float32(x - lse)
		}
	}
	return m, s.Truth()
}

// Track is one file as long as the script.
func (s Script) Track(quickHash string) align.Track {
	return align.Track{Files: []align.TrackFile{{QuickHash: quickHash, DurationMs: s.TotalMS}}}
}
