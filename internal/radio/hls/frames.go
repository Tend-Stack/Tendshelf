package hls

// maxResync is how far into the data the first frame may start (junk before it is skipped once).
const maxResync = 4 << 10

// headerFunc reads a frame header at the start of b. It reports the whole frame length (header
// included), the format, and whether b really starts with a valid header. It must not read past b.
type headerFunc func(b []byte) (length int, f Format, ok bool)

// scanFrames returns the run of consecutive valid frames in b as one slice of b. A trailing partial
// frame is dropped. The first frame is searched for within maxResync bytes and must be followed by
// another valid header (or the end of the data), so a stray sync pattern in junk is not taken. The run
// stops at the first byte that is not a frame; a different format inside the run is ErrChanged.
func scanFrames(b []byte, head headerFunc) ([]byte, Format, error) {
	limit := min(len(b), maxResync+1)
	for start := 0; start < limit; start++ {
		n, f, ok := head(b[start:])
		if !ok {
			continue
		}
		if next := start + n; next < len(b) {
			// The next header must be valid and the same format. Too few bytes left to hold a
			// header cannot be judged, so that case is accepted.
			if _, g, ok := head(b[next:]); (ok && g != f) || (!ok && len(b)-next >= 8) {
				continue
			}
		}
		end := start
		for end < len(b) {
			n, g, ok := head(b[end:])
			if !ok || end+n > len(b) {
				break
			}
			if g != f {
				return nil, Format{}, ErrChanged
			}
			end += n
		}
		if end == start {
			continue // only a partial frame: nothing playable here
		}
		return b[start:end], f, nil
	}
	return nil, Format{}, ErrCodec
}
