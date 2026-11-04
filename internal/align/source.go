package align

import (
	"errors"
	"fmt"
)

// WindowFrames is the number of kept frames in one cached window.
const WindowFrames = WindowMs / FrameMs

// ErrWindowMissing is returned by a cache Source when a window it needs is not (or no longer) cached.
var ErrWindowMissing = errors.New("align: an emissions window is missing from the cache")

// cacheSource is a Source over the emissions cache for one track and model. Frames past the end of what
// the decoder produced read as blank.
type cacheSource struct {
	c        Cache
	track    Track
	modelSHA string
	vocab    int
	blank    int

	lastKey string
	last    Matrix
}

func newCacheSource(c Cache, track Track, modelSHA string, v Vocab) *cacheSource {
	return &cacheSource{c: c, track: track, modelSHA: modelSHA, vocab: v.Size(), blank: v.Blank}
}

// window reads (and remembers) one cached window as a matrix.
func (s *cacheSource) window(f TrackFile, k int) (Matrix, error) {
	key := fmt.Sprintf("%s/%d", f.QuickHash, k)
	if key == s.lastKey {
		return s.last, nil
	}
	em, ok, err := s.c.Read(f.QuickHash, s.modelSHA, k)
	if err != nil {
		return Matrix{}, err
	}
	if !ok {
		return Matrix{}, fmt.Errorf("%w: %s window %d", ErrWindowMissing, f.QuickHash[:8], k)
	}
	if em.Vocab != s.vocab {
		return Matrix{}, fmt.Errorf("align: cached window has %d symbols, the model has %d", em.Vocab, s.vocab)
	}
	s.lastKey, s.last = key, em.Matrix()
	return s.last, nil
}

// Frames returns global frames [from, to).
func (s *cacheSource) Frames(from, to int64) (Matrix, error) {
	total := s.track.Frames()
	from = max(from, 0)
	to = min(to, total)
	if to < from {
		to = from
	}
	n := int(to - from)
	m := Matrix{T: n, V: s.vocab, LogP: make([]float32, n*s.vocab)}
	pad := Dequantise(255)
	for t := 0; t < n; t++ {
		row := m.Row(t)
		for i := range row {
			row[i] = pad
		}
		row[s.blank] = 0
	}
	var start int64 // global frame of the file's first frame
	for _, f := range s.track.Files {
		nf := fileFrames(f)
		lo, hi := max(from, start), min(to, start+nf)
		for g := lo; g < hi; {
			local := g - start
			k := int(local / WindowFrames)
			w, err := s.window(f, k)
			if err != nil {
				return Matrix{}, err
			}
			inWin := int(local % WindowFrames)
			end := min(hi, start+int64(k+1)*WindowFrames)
			for ; g < end; g++ {
				if inWin < w.T {
					copy(m.Row(int(g-from)), w.Row(inWin))
				}
				inWin++
			}
		}
		start += nf
	}
	return m, nil
}
