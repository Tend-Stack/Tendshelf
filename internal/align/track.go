package align

// TrackFile is one audio file on the timeline. OffsetMs is where it starts in the book's timeline.
type TrackFile struct {
	QuickHash            string
	OffsetMs, DurationMs int64
}

// Track is the book's audio files in play order. Global frame indexes count 20 ms frames over the
// concatenation, each file contributing ceil(DurationMs/20) frames.
type Track struct{ Files []TrackFile }

// Matrix is T frames of V log-probabilities, row-major.
type Matrix struct {
	T, V int
	LogP []float32
}

// Row is frame t.
func (m Matrix) Row(t int) []float32 { return m.LogP[t*m.V : (t+1)*m.V] }

// Source gives emissions for global frames [from, to). P3b implements it over the cache; tests use memory.
type Source interface {
	Frames(from, to int64) (Matrix, error)
}

// MemSource is a Source over one matrix that starts at global frame 0.
type MemSource struct{ M Matrix }

// Frames returns the rows from..to, clipped to the matrix.
func (s MemSource) Frames(from, to int64) (Matrix, error) {
	if from < 0 {
		from = 0
	}
	if to > int64(s.M.T) {
		to = int64(s.M.T)
	}
	if to < from {
		to = from
	}
	return Matrix{T: int(to - from), V: s.M.V, LogP: s.M.LogP[int(from)*s.M.V : int(to)*s.M.V]}, nil
}

func fileFrames(f TrackFile) int64 { return (f.DurationMs + FrameMs - 1) / FrameMs }

// Frames is the length of the whole track in frames.
func (t Track) Frames() int64 {
	var n int64
	for _, f := range t.Files {
		n += fileFrames(f)
	}
	return n
}

// FileAt returns the index of the file holding global frame g and that file's first frame. A frame past the
// end belongs to the last file.
func (t Track) FileAt(g int64) (int, int64) {
	var start int64
	for i, f := range t.Files {
		n := fileFrames(f)
		if g < start+n || i == len(t.Files)-1 {
			return i, start
		}
		start += n
	}
	return -1, 0
}

// TimeMs is the position of global frame g on the book's timeline.
func (t Track) TimeMs(g int64) int64 {
	i, start := t.FileAt(g)
	if i < 0 {
		return g * FrameMs
	}
	local := g - start
	f := t.Files[i]
	if local > fileFrames(f) {
		local = fileFrames(f)
	}
	return f.OffsetMs + local*FrameMs
}
