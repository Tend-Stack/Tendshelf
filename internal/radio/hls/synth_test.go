package hls

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// Synthetic audio for the tests: nothing here is real audio, only bytes with the right headers.

// synthADTS returns n ADTS frames (LC) with the given sampling index, channel configuration and
// payload size.
func synthADTS(n, srIndex, chCfg, payload int) []byte {
	var b []byte
	for i := range n {
		h := adtsHeader(1, srIndex, chCfg, payload)
		b = append(b, h[:]...)
		b = append(b, bytes.Repeat([]byte{byte(0x20 + i)}, payload)...)
	}
	return b
}

// synthMPEG returns n MPEG-1 Layer III frames at 128 kbit/s and 44.1 kHz (417 bytes each).
func synthMPEG(n int, mono bool) []byte {
	var b []byte
	mode := byte(0x00)
	if mono {
		mode = 0xC0
	}
	for i := range n {
		b = append(b, 0xFF, 0xFB, 0x90, mode)
		b = append(b, bytes.Repeat([]byte{byte(0x30 + i)}, 417-4)...)
	}
	return b
}

// synthID3 builds an ID3v2 tag with TPE1 and TIT2 frames (empty strings are left out).
func synthID3(version byte, enc byte, title, artist string) []byte {
	var body []byte
	frame := func(id, text string) {
		if text == "" {
			return
		}
		data := append([]byte{enc}, encodeID3(enc, text)...)
		body = append(body, id...)
		if version == 4 {
			body = append(body, syncsafeBytes(len(data))...)
		} else {
			body = binary.BigEndian.AppendUint32(body, uint32(len(data)))
		}
		body = append(body, 0, 0)
		body = append(body, data...)
	}
	frame("TPE1", artist)
	frame("TIT2", title)
	tag := []byte{'I', 'D', '3', version, 0, 0}
	tag = append(tag, syncsafeBytes(len(body))...)
	return append(tag, body...)
}

func syncsafeBytes(n int) []byte {
	return []byte{byte(n >> 21 & 0x7F), byte(n >> 14 & 0x7F), byte(n >> 7 & 0x7F), byte(n & 0x7F)}
}

func encodeID3(enc byte, s string) []byte {
	switch enc {
	case 0:
		var b []byte
		for _, r := range s {
			b = append(b, byte(r))
		}
		return b
	case 1:
		b := []byte{0xFF, 0xFE}
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u), byte(u>>8))
		}
		return b
	case 2:
		var b []byte
		for _, u := range utf16.Encode([]rune(s)) {
			b = append(b, byte(u>>8), byte(u))
		}
		return b
	}
	return []byte(s)
}

// tsPacketOf wraps a payload (at most 184 bytes) in one 188-byte transport packet, padding with an
// adaptation field.
func tsPacketOf(pid int, pusi bool, payload []byte) []byte {
	p := make([]byte, tsPacket)
	p[0] = tsSync
	p[1] = byte(pid >> 8 & 0x1F)
	if pusi {
		p[1] |= 0x40
	}
	p[2] = byte(pid)
	if len(payload) == 184 {
		p[3] = 0x10
		copy(p[4:], payload)
		return p
	}
	p[3] = 0x30
	p[4] = byte(183 - len(payload))
	for i := 5; i < 5+int(p[4]); i++ {
		p[i] = 0xFF
	}
	if p[4] > 0 {
		p[5] = 0
	}
	copy(p[188-len(payload):], payload)
	return p
}

// tsPackets splits a payload over as many packets of one PID as needed; the first has the start flag.
func tsPackets(pid int, payload []byte) []byte {
	var out []byte
	for first := true; len(payload) > 0 || first; first = false {
		n := min(184, len(payload))
		out = append(out, tsPacketOf(pid, first, payload[:n])...)
		payload = payload[n:]
	}
	return out
}

func synthPES(streamID byte, es []byte) []byte {
	pes := []byte{0, 0, 1, streamID, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1}
	return append(pes, es...)
}

type tsStreamSpec struct {
	typ int
	pid int
}

func section(table byte, body []byte) []byte {
	n := len(body) + 4
	s := []byte{table, 0xB0 | byte(n>>8&0x0F), byte(n)}
	s = append(s, body...)
	return append(s, 0, 0, 0, 0) // CRC is not checked
}

// synthTS builds a transport stream with a PAT, a PMT listing the streams and the given PES payloads
// per PID, each payload as one PES packet.
func synthTS(streams []tsStreamSpec, pes map[int][][]byte) []byte {
	pat := section(0, []byte{0, 0, 0xC1, 0, 0, 0, 1, 0xE1, 0x00}) // ts id, version, section numbers, program 1 -> PID 0x100
	pmtBody := []byte{0, 1, 0, 0, 0, 0xE1, 0x01, 0xF0, 0x00}      // program 1, PCR PID 0x101, no program info
	for _, s := range streams {
		pmtBody = append(pmtBody, byte(s.typ), 0xE0|byte(s.pid>>8), byte(s.pid), 0xF0, 0)
	}
	pmt := section(2, pmtBody)
	var out []byte
	out = append(out, tsPackets(0, append([]byte{0}, pat...))...)
	out = append(out, tsPackets(0x100, append([]byte{0}, pmt...))...)
	for _, s := range streams {
		for _, payload := range pes[s.pid] {
			id := byte(0xC0)
			switch s.typ {
			case stMetadata:
				id = 0xFC
			case 0x1B:
				id = 0xE0
			}
			out = append(out, tsPackets(s.pid, synthPES(id, payload))...)
		}
	}
	return out
}

// box builds one MP4 box.
func mkBox(typ string, children ...[]byte) []byte {
	body := bytes.Join(children, nil)
	b := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	b = append(b, typ...)
	return append(b, body...)
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// synthInit builds an initialisation section with one mp4a track: asc is the AudioSpecificConfig
// and oti the objectTypeIndication.
func synthInit(trackID uint32, oti byte, asc []byte, entry string) []byte {
	descr := func(tag byte, body []byte) []byte { return append([]byte{tag, byte(len(body))}, body...) }
	dsi := descr(0x05, asc)
	dc := descr(0x04, append(append([]byte{oti, 0x15, 0, 0, 0}, make([]byte, 8)...), dsi...))
	es := descr(0x03, append([]byte{0, 1, 0}, dc...))
	esds := mkBox("esds", u32(0), es)
	sampleEntry := append(make([]byte, 6), 0, 1)               // reserved, data reference index
	sampleEntry = append(sampleEntry, make([]byte, 8)...)      // version 0, reserved
	sampleEntry = append(sampleEntry, 0, 2, 0, 16, 0, 0, 0, 0) // channels, size, predefined, reserved
	sampleEntry = append(sampleEntry, 0xAC, 0x44, 0, 0)        // rate 16.16
	stsd := mkBox("stsd", u32(0), u32(1), mkBox(entry, sampleEntry, esds))
	tkhd := mkBox("tkhd", u32(0), u32(0), u32(0), u32(trackID), make([]byte, 60))
	trak := mkBox("trak", tkhd, mkBox("mdia", mkBox("minf", mkBox("stbl", stsd))))
	return append(mkBox("ftyp", []byte("iso6")), mkBox("moov", trak)...)
}

// synthFragment builds a moof + mdat holding the samples, with per-sample sizes in the trun.
func synthFragment(trackID uint32, samples [][]byte) []byte {
	build := func(dataOffset uint32) []byte {
		trun := u32(0x000201)
		trun = append(trun, u32(uint32(len(samples)))...)
		trun = append(trun, u32(dataOffset)...)
		for _, s := range samples {
			trun = append(trun, u32(uint32(len(s)))...)
		}
		tfhd := mkBox("tfhd", u32(0x020000), u32(trackID))
		return mkBox("moof", mkBox("mfhd", u32(0), u32(1)), mkBox("traf", tfhd, mkBox("trun", trun)))
	}
	moof := build(0)
	moof = build(uint32(len(moof) + 8))
	return append(moof, mkBox("mdat", bytes.Join(samples, nil))...)
}
