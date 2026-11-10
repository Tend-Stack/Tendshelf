package hls

// Demux turns one media segment into audio frames: a plain run of ADTS (AAC) or MPEG Layer III
// frames, their format, and the titles found in timed metadata. init is the segment's EXT-X-MAP
// section, only needed for fragmented MP4. The three container kinds are told apart by content, never
// by file name: MPEG-TS (0x47 every 188 bytes), packed audio (an ID3 tag or a frame sync first), and
// otherwise fragmented MP4.
func Demux(seg, init []byte) (frames []byte, f Format, titles []string, err error) {
	if len(seg) > MaxSegment || len(init) > MaxInit {
		return nil, Format{}, nil, ErrTooLarge
	}
	switch {
	case len(seg) > tsPacket && seg[0] == tsSync && seg[tsPacket] == tsSync:
		return demuxTS(seg)
	case id3Len(seg) > 0 || isFrameSync(seg):
		return demuxPacked(seg)
	case len(init) > 0:
		cfg, err := parseInit(init)
		if err != nil {
			return nil, Format{}, nil, err
		}
		frames, f, err = demuxFMP4(seg, cfg)
		return frames, f, nil, err
	default:
		return nil, Format{}, nil, ErrUnsupported
	}
}

func isFrameSync(b []byte) bool { return len(b) >= 2 && b[0] == 0xFF && b[1]&0xE0 == 0xE0 }

// scanAudio tries ADTS and then MPEG audio on an elementary stream. A format change inside the data
// is reported as it is; otherwise MPEG audio is the last try and its error stands.
func scanAudio(b []byte) ([]byte, Format, error) {
	frames, f, err := scanADTS(b)
	if err == nil || err == ErrChanged {
		return frames, f, err
	}
	return scanMPEG(b)
}

// demuxPacked reads packed audio (RFC 8216 section 3.4: a raw ADTS or MP3 stream with ID3 tags in
// front of it).
func demuxPacked(seg []byte) ([]byte, Format, []string, error) {
	var titles []string
	b := seg
	for range 8 { // a few tags in a row at most
		n := id3Len(b)
		if n == 0 {
			break
		}
		titles = appendTitle(titles, id3Title(b))
		b = b[min(n, len(b)):]
	}
	frames, f, err := scanAudio(b)
	if err != nil {
		return nil, Format{}, nil, err
	}
	return frames, f, titles, nil
}

// appendTitle adds t unless it is empty or repeats the last title.
func appendTitle(titles []string, t string) []string {
	if t == "" || (len(titles) > 0 && titles[len(titles)-1] == t) {
		return titles
	}
	return append(titles, t)
}
