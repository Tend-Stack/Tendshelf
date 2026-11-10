package hls

// Bitrates in kbit/s for Layer III (ISO 11172-3 table 2.4.2.3 and ISO 13818-3); index 0 is free format
// (refused) and 15 is invalid.
var (
	mpeg1L3Bitrates = [16]int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0}
	mpeg2L3Bitrates = [16]int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0}
	// Sample rates by version (0 = MPEG-2.5, 2 = MPEG-2, 3 = MPEG-1) and the 2-bit index.
	mpegRates = map[int][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}
)

// mpegHead reads an MPEG audio Layer III frame header: 11 sync bits, a real version, layer III,
// a listed bitrate and a real sample rate. Free format and Layers I and II are refused.
func mpegHead(b []byte) (int, Format, bool) {
	if len(b) < 4 || b[0] != 0xFF || b[1]&0xE0 != 0xE0 {
		return 0, Format{}, false
	}
	version := int(b[1]>>3) & 3
	if version == 1 || int(b[1]>>1)&3 != 1 { // reserved version, or not layer III
		return 0, Format{}, false
	}
	brs := &mpeg1L3Bitrates
	coef := 144
	if version != 3 {
		brs, coef = &mpeg2L3Bitrates, 72
	}
	kbps := brs[b[2]>>4]
	srIdx := int(b[2]>>2) & 3
	if kbps == 0 || srIdx == 3 {
		return 0, Format{}, false
	}
	rate := mpegRates[version][srIdx]
	n := coef*kbps*1000/rate + int(b[2]>>1&1)
	ch := 2
	if b[3]>>6 == 3 {
		ch = 1
	}
	return n, Format{Codec: "mp3", SampleRate: rate, Channels: ch}, true
}

// scanMPEG returns the MPEG audio frames in b (see scanFrames) and their format.
func scanMPEG(b []byte) ([]byte, Format, error) { return scanFrames(b, mpegHead) }
