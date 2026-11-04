package align

import "strings"

// Heard is one word of the coarse transcript: the greedy CTC reading of the audio. Frames are global.
type Heard struct {
	Norm         string
	StartF, EndF int64
}

// Pause is a run of at least PauseFrames frames where the model says blank or word delimiter.
type Pause struct{ StartF, EndF int64 }

const greedyChunk = 4096

// Greedy reads the whole track with the recipe pause length.
func Greedy(src Source, track Track, v Vocab) ([]Heard, []Pause, error) {
	return greedy(src, track, v, PauseFrames)
}

// greedy takes the best symbol of every frame, merges repeats, and cuts words at the delimiter or at a pause.
// It returns an error only when the source fails.
func greedy(src Source, track Track, v Vocab, pauseFrames int) ([]Heard, []Pause, error) {
	total := track.Frames()
	var (
		heard   []Heard
		pauses  []Pause
		word    strings.Builder
		wStart  int64
		wEnd    int64
		prev          = -1 // best symbol of the previous frame
		sepFrom int64 = -1
	)
	closeWord := func() {
		if word.Len() > 0 {
			heard = append(heard, Heard{Norm: word.String(), StartF: wStart, EndF: wEnd})
			word.Reset()
		}
	}
	closeSep := func(g int64) {
		if sepFrom >= 0 && g-sepFrom >= int64(pauseFrames) {
			pauses = append(pauses, Pause{StartF: sepFrom, EndF: g})
		}
		sepFrom = -1
	}
	for from := int64(0); from < total; from += greedyChunk {
		to := from + greedyChunk
		if to > total {
			to = total
		}
		m, err := src.Frames(from, to)
		if err != nil {
			return nil, nil, err
		}
		for t := 0; t < m.T; t++ {
			g := from + int64(t)
			row := m.Row(t)
			best := 0
			for k := 1; k < len(row); k++ {
				if row[k] > row[best] {
					best = k
				}
			}
			switch {
			case best == v.Blank || best == v.Delim:
				if sepFrom < 0 {
					sepFrom = g
				}
				if best == v.Delim || g+1-sepFrom >= int64(pauseFrames) {
					closeWord()
				}
			default:
				closeSep(g)
				if best != prev || word.Len() == 0 {
					if word.Len() == 0 {
						wStart = g
					}
					word.WriteString(v.Letter(best))
				}
				wEnd = g + 1
			}
			prev = best
		}
	}
	closeWord()
	closeSep(total)
	return heard, pauses, nil
}
