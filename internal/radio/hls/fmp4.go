package hls

import "encoding/binary"

// maxSamples caps the samples read from one trun box.
const maxSamples = 1 << 16

// ascCfg is what the initialisation section says about the one audio track.
type ascCfg struct {
	trackID uint32
	profile int // ADTS profile: core audio object type minus one
	srIndex int // ADTS sampling_frequency_index of the core rate
	chCfg   int
	mp3     bool // samples are MPEG audio frames, not AAC access units
}

// box is one ISO 14496-12 box: its four-character type and its payload (after the header).
type box struct {
	typ  string
	data []byte
	size int // header and payload
}

// readBox reads the box at the start of b: 32-bit size, size 0 meaning "to the end", size 1 meaning a
// 64-bit size after the type (ISO 14496-12 section 4.2). ok is false for a truncated or impossible box.
func readBox(b []byte) (box, bool) {
	if len(b) < 8 {
		return box{}, false
	}
	size, hdr := uint64(binary.BigEndian.Uint32(b)), 8
	switch size {
	case 0:
		size = uint64(len(b))
	case 1:
		if len(b) < 16 {
			return box{}, false
		}
		size, hdr = binary.BigEndian.Uint64(b[8:]), 16
	}
	if size < uint64(hdr) || size > uint64(len(b)) {
		return box{}, false
	}
	return box{typ: string(b[4:8]), data: b[hdr:size], size: int(size)}, true
}

// boxes lists the boxes laid end to end in b, stopping at the first one that is broken.
func boxes(b []byte) []box {
	var out []box
	for len(out) < 4096 {
		bx, ok := readBox(b)
		if !ok {
			break
		}
		out = append(out, bx)
		b = b[bx.size:]
	}
	return out
}

// find walks down a path of box types (at most 8 deep) and returns the first box found at the end.
func find(b []byte, path ...string) (box, bool) {
	if len(path) == 0 || len(path) > 8 {
		return box{}, false
	}
	for _, bx := range boxes(b) {
		if bx.typ != path[0] {
			continue
		}
		if len(path) == 1 {
			return bx, true
		}
		if r, ok := find(bx.data, path[1:]...); ok {
			return r, true
		}
	}
	return box{}, false
}

// parseInit reads the first audio track of an initialisation section (moov): the track id, and from
// the esds box the decoder configuration. An encrypted track (enca) is ErrLocked; any other audio
// entry than mp4a (ac-3, Opus, FLAC...) is ErrCodec.
func parseInit(init []byte) (ascCfg, error) {
	moov, ok := find(init, "moov")
	if !ok {
		return ascCfg{}, ErrUnsupported
	}
	sawOther := false
	for _, trak := range boxes(moov.data) {
		if trak.typ != "trak" {
			continue
		}
		stsd, ok := find(trak.data, "mdia", "minf", "stbl", "stsd")
		if !ok || len(stsd.data) < 8 { // version/flags, entry_count
			continue
		}
		entry, ok := readBox(stsd.data[8:])
		if !ok {
			continue
		}
		switch entry.typ {
		case "mp4a":
		case "enca":
			return ascCfg{}, ErrLocked
		case "ac-3", "ec-3", "Opus", "fLaC", "alac", "mhm1", "mha1":
			sawOther = true
			continue
		default: // video or text track
			continue
		}
		cfg, err := parseMP4A(entry.data)
		if err != nil {
			return ascCfg{}, err
		}
		if tkhd, ok := find(trak.data, "tkhd"); ok && len(tkhd.data) >= 16 {
			// version 0: flags, creation, modification, track id; version 1 has 64-bit times.
			at := 12
			if tkhd.data[0] == 1 {
				at = 20
			}
			if len(tkhd.data) >= at+4 {
				cfg.trackID = binary.BigEndian.Uint32(tkhd.data[at:])
			}
		}
		return cfg, nil
	}
	if sawOther {
		return ascCfg{}, ErrCodec
	}
	return ascCfg{}, ErrUnsupported
}

// parseMP4A reads an AudioSampleEntry (ISO 14496-12 section 12.2.3) and its esds child.
func parseMP4A(entry []byte) (ascCfg, error) {
	// 6 reserved + 2 data reference index, then the sound description version decides the rest.
	if len(entry) < 28 {
		return ascCfg{}, ErrUnsupported
	}
	skip := 28
	switch binary.BigEndian.Uint16(entry[8:]) { // QuickTime sound description version
	case 1:
		skip += 16
	case 2:
		skip += 36
	}
	if skip > len(entry) {
		return ascCfg{}, ErrUnsupported
	}
	esds, ok := find(entry[skip:], "esds")
	if !ok || len(esds.data) < 4 {
		return ascCfg{}, ErrUnsupported
	}
	return parseESDS(esds.data[4:]) // after version/flags
}

// readDescr reads one MPEG-4 descriptor (ISO 14496-1 section 7.2.2.2): a tag and a size of up to four
// 7-bit groups.
func readDescr(b []byte) (tag byte, body, rest []byte, ok bool) {
	if len(b) < 2 {
		return 0, nil, nil, false
	}
	tag = b[0]
	size, i := 0, 1
	for ; i < len(b) && i <= 4; i++ {
		size = size<<7 | int(b[i]&0x7F)
		if b[i]&0x80 == 0 {
			i++
			break
		}
	}
	if i > len(b) || size > len(b)-i {
		return 0, nil, nil, false
	}
	return tag, b[i : i+size], b[i+size:], true
}

// parseESDS reads ES_Descriptor (tag 3), DecoderConfigDescriptor (tag 4) and DecoderSpecificInfo (tag 5).
func parseESDS(b []byte) (ascCfg, error) {
	tag, es, _, ok := readDescr(b)
	if !ok || tag != 0x03 || len(es) < 3 {
		return ascCfg{}, ErrUnsupported
	}
	flags := es[2]
	es = es[3:]          // ES_ID (2), flags (1)
	if flags&0x80 != 0 { // dependsOn_ES_ID
		if len(es) < 2 {
			return ascCfg{}, ErrUnsupported
		}
		es = es[2:]
	}
	if flags&0x40 != 0 { // URL
		if len(es) < 1 || len(es) < 1+int(es[0]) {
			return ascCfg{}, ErrUnsupported
		}
		es = es[1+int(es[0]):]
	}
	if flags&0x20 != 0 { // OCR_ES_ID
		if len(es) < 2 {
			return ascCfg{}, ErrUnsupported
		}
		es = es[2:]
	}
	tag, dc, _, ok := readDescr(es)
	if !ok || tag != 0x04 || len(dc) < 13 {
		return ascCfg{}, ErrUnsupported
	}
	switch dc[0] { // objectTypeIndication (ISO 14496-1 table 5)
	case 0x6B, 0x69: // MPEG-1 and MPEG-2 audio: Layer III in practice
		return ascCfg{mp3: true}, nil
	case 0x40, 0x66, 0x67, 0x68: // MPEG-4 audio, MPEG-2 AAC Main, LC, SSR
	default:
		return ascCfg{}, ErrCodec
	}
	var asc []byte
	tag, asc, _, ok = readDescr(dc[13:])
	if !ok || tag != 0x05 {
		return ascCfg{}, ErrUnsupported
	}
	return parseASC(asc)
}

// bitReader reads big-endian bit fields and remembers a read past the end.
type bitReader struct {
	b   []byte
	pos int
	bad bool
}

func (r *bitReader) read(n int) int {
	v := 0
	for range n {
		if r.pos>>3 >= len(r.b) {
			r.bad = true
			return 0
		}
		v = v<<1 | int(r.b[r.pos>>3]>>(7-r.pos&7)&1)
		r.pos++
	}
	return v
}

// parseASC reads an AudioSpecificConfig (ISO 14496-3 section 1.6.2.1). For HE-AAC (object type 5 or
// 29) the ADTS header carries the core: the core object type and the core sample rate, which come
// first in the explicit signalling.
func parseASC(asc []byte) (ascCfg, error) {
	r := &bitReader{b: asc}
	aot := r.read(5)
	if aot == 31 {
		aot = 32 + r.read(6)
	}
	sr := r.read(4)
	if sr == 15 { // explicit rate: cannot be written into an ADTS header
		return ascCfg{}, ErrCodec
	}
	ch := r.read(4)
	if aot == 5 || aot == 29 {
		if ext := r.read(4); ext == 15 {
			r.read(24)
		}
		aot = r.read(5) // the core object type follows the extension rate
		if aot == 31 {
			aot = 32 + r.read(6)
		}
	}
	switch {
	case r.bad:
		return ascCfg{}, ErrUnsupported
	case aot == 34: // MPEG-1/2 Layer III carried as an object type
		return ascCfg{mp3: true}, nil
	case aot < 1 || aot > 4 || sr >= len(adtsRates) || ch >= len(adtsChannels):
		return ascCfg{}, ErrCodec
	}
	return ascCfg{profile: aot - 1, srIndex: sr, chCfg: ch}, nil
}

// demuxFMP4 collects the samples of a fragmented MP4 segment (moof + mdat pairs) in order. AAC
// samples each get an ADTS header; MPEG audio samples are frames already.
func demuxFMP4(seg []byte, cfg ascCfg) ([]byte, Format, error) {
	var out []byte
	top := boxes(seg)
	offset := 0 // start of each top-level box in seg
	for _, bx := range top {
		start := offset
		offset += bx.size
		if bx.typ != "moof" {
			continue
		}
		next := offset + 8 // default data position: right after the following mdat's header
		for _, traf := range boxes(bx.data) {
			if traf.typ != "traf" {
				continue
			}
			var err error
			out, next, err = readTraf(out, seg, traf.data, cfg, start, next)
			if err != nil {
				return nil, Format{}, err
			}
		}
	}
	if len(out) == 0 {
		return nil, Format{}, ErrUnsupported
	}
	if cfg.mp3 {
		return scanMPEG(out)
	}
	f := Format{Codec: "aac", SampleRate: adtsRates[cfg.srIndex], Channels: adtsChannels[cfg.chCfg]}
	return out, f, nil
}

// readTraf appends the samples of one track fragment to out. moofStart is where the moof box begins
// in seg (data offsets are relative to it); next is where samples are read from when a trun has no
// data offset. It returns the updated out and next.
func readTraf(out, seg, traf []byte, cfg ascCfg, moofStart, next int) ([]byte, int, error) {
	tfhd, ok := find(traf, "tfhd")
	if !ok || len(tfhd.data) < 8 {
		return out, next, nil
	}
	flags := binary.BigEndian.Uint32(tfhd.data) & 0xFFFFFF
	if cfg.trackID != 0 && binary.BigEndian.Uint32(tfhd.data[4:]) != cfg.trackID {
		return out, next, nil // another track's fragment
	}
	d := tfhd.data[8:]
	base := moofStart    // default-base-is-moof
	if flags&0x01 != 0 { // base_data_offset
		if len(d) < 8 {
			return out, next, nil
		}
		if v := binary.BigEndian.Uint64(d); v <= uint64(len(seg)) {
			base = int(v)
		}
		d = d[8:]
	}
	if flags&0x02 != 0 { // sample_description_index
		d = d[min(4, len(d)):]
	}
	if flags&0x08 != 0 { // default_sample_duration
		d = d[min(4, len(d)):]
	}
	defSize := -1
	if flags&0x10 != 0 { // default_sample_size
		if len(d) < 4 {
			return out, next, nil
		}
		defSize = int(binary.BigEndian.Uint32(d))
	}
	for _, trun := range boxes(traf) {
		if trun.typ != "trun" || len(trun.data) < 8 {
			continue
		}
		tf := binary.BigEndian.Uint32(trun.data) & 0xFFFFFF
		count := int(binary.BigEndian.Uint32(trun.data[4:]))
		if count > maxSamples {
			return nil, 0, ErrTooLarge
		}
		r := trun.data[8:]
		pos := next
		if tf&0x001 != 0 { // data_offset, signed
			if len(r) < 4 {
				continue
			}
			pos = base + int(int32(binary.BigEndian.Uint32(r)))
			r = r[4:]
		}
		if tf&0x004 != 0 { // first_sample_flags
			r = r[min(4, len(r)):]
		}
		per := 0
		for _, bit := range []uint32{0x100, 0x200, 0x400, 0x800} {
			if tf&bit != 0 {
				per += 4
			}
		}
		if per > 0 && count > len(r)/per {
			continue // table shorter than sample_count says
		}
		for s := 0; s < count; s++ {
			size := defSize
			if tf&0x200 != 0 { // per-sample size, after the optional duration
				at := s * per
				if tf&0x100 != 0 {
					at += 4
				}
				size = int(binary.BigEndian.Uint32(r[at:]))
			}
			if size < 0 {
				return nil, 0, ErrUnsupported
			}
			if pos < 0 || size > len(seg)-pos {
				break // sample outside the segment: stop this run
			}
			sample := seg[pos : pos+size]
			pos += size
			if cfg.mp3 {
				out = append(out, sample...)
			} else {
				if size+7 > 8191 {
					return nil, 0, ErrUnsupported
				}
				h := adtsHeader(cfg.profile, cfg.srIndex, cfg.chCfg, size)
				out = append(out, h[:]...)
				out = append(out, sample...)
			}
			if len(out) > MaxSegment {
				return nil, 0, ErrTooLarge
			}
		}
		next = pos
	}
	return out, next, nil
}
