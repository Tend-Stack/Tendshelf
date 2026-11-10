package hls

const (
	tsPacket = 188  // ISO 13818-1 section 2.4.3.2
	tsSync   = 0x47 // sync_byte

	maxPSI = 4096 // a PAT or PMT section is far smaller; larger is dropped as junk
)

// Stream types from ISO 13818-1 table 2-34 (0x81 and 0x87 are the registered AC-3 and E-AC-3 types).
const (
	stMPEG1Audio = 0x03
	stMPEG2Audio = 0x04
	stADTS       = 0x0F // AAC with ADTS
	stLATM       = 0x11 // AAC with LATM, not supported
	stMetadata   = 0x15 // metadata in PES packets (HLS timed ID3)
	stAC3        = 0x81
	stEAC3       = 0x87
)

// psi collects one program-specific-information section that may span packets.
type psi struct {
	buf  []byte
	want int // section length including its 3-byte header, 0 until known
}

// add feeds one packet payload and returns the whole section once it is complete.
func (p *psi) add(pusi bool, payload []byte) []byte {
	switch {
	case pusi:
		if len(payload) < 1 || int(payload[0]) >= len(payload) {
			p.buf, p.want = nil, 0
			return nil
		}
		p.buf = append(p.buf[:0], payload[1+int(payload[0]):]...) // skip pointer_field
		p.want = 0
	case p.buf == nil:
		return nil
	default:
		p.buf = append(p.buf, payload...)
	}
	if p.want == 0 && len(p.buf) >= 3 {
		p.want = 3 + (int(p.buf[1]&0x0F)<<8 | int(p.buf[2]))
		if p.want > maxPSI {
			p.buf, p.want = nil, 0
			return nil
		}
	}
	if p.want > 0 && len(p.buf) >= p.want {
		s := p.buf[:p.want]
		p.buf, p.want = nil, 0
		return s
	}
	return nil
}

// pes collects the packets of one PES packet; a new one starts at each payload_unit_start.
type pes struct{ buf []byte }

// add feeds one payload and returns the previous PES packet's data when pusi starts a new one.
func (p *pes) add(pusi bool, payload []byte) []byte {
	var done []byte
	if pusi {
		done = p.take()
	}
	if pusi || len(p.buf) > 0 {
		p.buf = append(p.buf, payload...)
	}
	return done
}

// take returns the data of the PES packet collected so far and starts over.
func (p *pes) take() []byte {
	b := p.buf
	p.buf = nil
	return pesData(b)
}

// pesData strips the PES header (ISO 13818-1 section 2.4.3.6: start code 00 00 01, stream id, length,
// two flag bytes, header data length) and returns the payload, or nil when b is not a PES packet.
func pesData(b []byte) []byte {
	if len(b) < 9 || b[0] != 0 || b[1] != 0 || b[2] != 1 {
		return nil
	}
	start := 9 + int(b[8])
	if start > len(b) {
		return nil
	}
	return b[start:]
}

// demuxTS reads an MPEG transport stream segment: PAT, then PMT, then the first supported audio
// stream; timed ID3 metadata supplies titles. Other PIDs are ignored. A scrambled packet is ErrLocked.
func demuxTS(seg []byte) ([]byte, Format, []string, error) {
	var (
		pat, pmt    psi
		pmtPID      = -1
		audioPID    = -1
		audioType   int
		sawVideo    bool
		sawBadAudio bool
		metaPIDs    []int
		audio       []byte
		audioPES    pes
		metaPES     = map[int]*pes{}
		titles      []string
	)
	for off := 0; off+tsPacket <= len(seg); off += tsPacket {
		p := seg[off : off+tsPacket]
		if p[0] != tsSync {
			return nil, Format{}, nil, ErrUnsupported
		}
		if p[1]&0x80 != 0 { // transport_error_indicator
			continue
		}
		pusi := p[1]&0x40 != 0
		pid := int(p[1]&0x1F)<<8 | int(p[2])
		if p[3]>>6 != 0 { // transport_scrambling_control: encrypted
			return nil, Format{}, nil, ErrLocked
		}
		payloadAt := 4
		switch p[3] >> 4 & 3 {
		case 1: // payload only
		case 3: // adaptation field, then payload
			payloadAt = 5 + int(p[4])
		default: // adaptation only, or reserved
			continue
		}
		if payloadAt >= tsPacket {
			continue
		}
		payload := p[payloadAt:]
		switch {
		case pid == 0:
			if s := pat.add(pusi, payload); s != nil {
				pmtPID = firstProgramPID(s)
			}
		case pid == pmtPID:
			s := pmt.add(pusi, payload)
			if s == nil || audioPID >= 0 {
				continue
			}
			for _, st := range parsePMT(s) {
				switch st.typ {
				case stADTS, stMPEG1Audio, stMPEG2Audio:
					if audioPID < 0 {
						audioPID, audioType = st.pid, st.typ
					}
				case stLATM, stAC3, stEAC3:
					sawBadAudio = true
				case stMetadata:
					if len(metaPIDs) < 4 {
						metaPIDs = append(metaPIDs, st.pid)
						metaPES[st.pid] = &pes{}
					}
				case 0x01, 0x02, 0x1B, 0x24: // MPEG-1/2 video, H.264, H.265
					sawVideo = true
				}
			}
		case pid == audioPID:
			audio = append(audio, audioPES.add(pusi, payload)...)
		default:
			if m := metaPES[pid]; m != nil {
				titles = appendTitle(titles, id3Title(m.add(pusi, payload)))
			}
		}
	}
	for _, pid := range metaPIDs {
		titles = appendTitle(titles, id3Title(metaPES[pid].take()))
	}
	if audioPID < 0 {
		switch {
		case sawBadAudio:
			return nil, Format{}, nil, ErrCodec
		case sawVideo:
			return nil, Format{}, nil, ErrVideo
		}
		return nil, Format{}, nil, ErrUnsupported
	}
	audio = append(audio, audioPES.take()...)
	scan := scanMPEG
	if audioType == stADTS {
		scan = scanADTS
	}
	frames, f, err := scan(audio)
	if err != nil {
		return nil, Format{}, nil, err
	}
	return frames, f, titles, nil
}

// firstProgramPID reads a PAT section (ISO 13818-1 table 2-25) and returns the PMT PID of the first
// real program (program_number other than 0, which names the network table), or -1.
func firstProgramPID(s []byte) int {
	if len(s) < 12 || s[0] != 0x00 {
		return -1
	}
	// Entries start after the 8 header bytes and are 4 bytes each, before the 4-byte CRC.
	for i := 8; i+4 <= len(s)-4; i += 4 {
		if prog := int(s[i])<<8 | int(s[i+1]); prog != 0 {
			return int(s[i+2]&0x1F)<<8 | int(s[i+3])
		}
	}
	return -1
}

type tsStream struct{ typ, pid int }

// parsePMT reads a PMT section (ISO 13818-1 table 2-28) into its elementary streams.
func parsePMT(s []byte) []tsStream {
	if len(s) < 16 || s[0] != 0x02 {
		return nil
	}
	i := 12 + (int(s[10]&0x0F)<<8 | int(s[11])) // after program_info
	end := len(s) - 4                           // CRC_32
	var out []tsStream
	for i+5 <= end && len(out) < 32 {
		out = append(out, tsStream{typ: int(s[i]), pid: int(s[i+1]&0x1F)<<8 | int(s[i+2])})
		i += 5 + (int(s[i+3]&0x0F)<<8 | int(s[i+4]))
	}
	return out
}
