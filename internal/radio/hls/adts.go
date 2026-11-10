package hls

// adtsRates is the sampling_frequency_index table of ISO 14496-3 section 1.6.3.4 (index 13-15 are
// reserved or escape values and not usable in an ADTS header).
var adtsRates = [...]int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

// adtsChannels maps channel_configuration to a channel count (ISO 14496-3 table 1.19). 0 means the
// layout is in the stream itself; it is kept as 0 channels.
var adtsChannels = [...]int{0, 1, 2, 3, 4, 5, 6, 8}

// adtsHead reads an ADTS fixed and variable header (ISO 14496-3 section 1.A.2.2): 12 sync bits, layer 0,
// frame length 7-8191 including the header.
func adtsHead(b []byte) (int, Format, bool) {
	if len(b) < 7 || b[0] != 0xFF || b[1]&0xF6 != 0xF0 {
		return 0, Format{}, false
	}
	sr := int(b[2]>>2) & 0x0F
	if sr >= len(adtsRates) {
		return 0, Format{}, false
	}
	ch := int(b[2]&1)<<2 | int(b[3]>>6)
	n := int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5)
	if n < 7 {
		return 0, Format{}, false
	}
	return n, Format{Codec: "aac", SampleRate: adtsRates[sr], Channels: adtsChannels[ch]}, true
}

// scanADTS returns the ADTS frames in b (see scanFrames) and their format.
func scanADTS(b []byte) ([]byte, Format, error) { return scanFrames(b, adtsHead) }

// adtsHeader builds the 7-byte header (no CRC, MPEG-4 id) for one raw AAC access unit of payloadLen
// bytes. profile is the audio object type minus one (0 Main, 1 LC, 2 SSR, 3 LTP).
func adtsHeader(profile, srIndex, chCfg, payloadLen int) [7]byte {
	n := payloadLen + 7
	return [7]byte{
		0xFF,
		0xF1, // sync, MPEG-4, layer 0, protection absent
		byte(profile&3)<<6 | byte(srIndex&15)<<2 | byte(chCfg>>2&1),
		byte(chCfg&3)<<6 | byte(n>>11&3),
		byte(n >> 3),
		byte(n&7)<<5 | 0x1F, // buffer fullness 0x7FF: variable bit rate
		0xFC,
	}
}
