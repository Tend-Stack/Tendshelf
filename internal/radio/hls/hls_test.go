package hls

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func mustURL(t testing.TB, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestParse(t *testing.T) {
	base := mustURL(t, "https://radio.example/live/index.m3u8")
	long := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\n" + strings.Repeat("a", maxLine+1) + "\n"
	many := "#EXTM3U\n#EXT-X-TARGETDURATION:6\n" + strings.Repeat("#EXTINF:6,\nseg.aac\n", MaxSegments+1)
	tests := []struct {
		name   string
		body   string
		err    error
		master bool
		check  func(t *testing.T, m *Master, md *Media)
	}{
		{name: "media with relative URIs", body: "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:100\n" +
			"#EXTINF:5.9,Artist - Song\nseg100.ts\n#EXT-X-DISCONTINUITY\n#EXTINF:6,\n/abs/seg101.ts\n#EXTINF:6,\nhttps://cdn.example/seg102.ts\n",
			check: func(t *testing.T, _ *Master, md *Media) {
				if md.Target != 6*time.Second || md.Sequence != 100 || len(md.Segments) != 3 || md.EndList {
					t.Fatalf("media = %+v", md)
				}
				s := md.Segments
				if s[0].URI != "https://radio.example/live/seg100.ts" || s[0].Seq != 100 || s[0].Title != "Artist - Song" ||
					s[0].Duration != 5900*time.Millisecond || s[0].Discontinuity {
					t.Errorf("seg0 = %+v", s[0])
				}
				if s[1].URI != "https://radio.example/abs/seg101.ts" || !s[1].Discontinuity || s[1].Seq != 101 {
					t.Errorf("seg1 = %+v", s[1])
				}
				if s[2].URI != "https://cdn.example/seg102.ts" || s[2].Discontinuity {
					t.Errorf("seg2 = %+v", s[2])
				}
			}},
		{name: "BOM, CRLF, endlist and map", body: "\xEF\xBB\xBF#EXTM3U\r\n#EXT-X-TARGETDURATION:10\r\n#EXT-X-MAP:URI=\"init.mp4\"\r\n#EXTINF:10,\r\nA.m4s\r\n#EXT-X-ENDLIST\r\n",
			check: func(t *testing.T, _ *Master, md *Media) {
				if !md.EndList || len(md.Segments) != 1 || md.Segments[0].Map != "https://radio.example/live/init.mp4" {
					t.Fatalf("media = %+v", md)
				}
			}},
		{name: "master with audio rendition", body: "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aud\",NAME=\"a, b\",URI=\"audio/a.m3u8\"\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=800000,CODECS=\"avc1.4d401f,mp4a.40.2\",RESOLUTION=640x360,AUDIO=\"aud\"\nv.m3u8\n" +
			"#EXT-X-STREAM-INF:BANDWIDTH=64000,CODECS=\"mp4a.40.5\"\nlow.m3u8\n",
			master: true,
			check: func(t *testing.T, m *Master, _ *Media) {
				if len(m.Variants) != 3 {
					t.Fatalf("variants = %+v", m.Variants)
				}
				v := m.Variants
				if v[0].Bandwidth != 800000 || v[0].Resolution != "640x360" || len(v[0].Codecs) != 2 || v[0].Codecs[1] != "mp4a.40.2" {
					t.Errorf("v0 = %+v", v[0])
				}
				if v[1].URI != "https://radio.example/live/low.m3u8" || v[1].Codecs[0] != "mp4a.40.5" {
					t.Errorf("v1 = %+v", v[1])
				}
				if v[2].URI != "https://radio.example/live/audio/a.m3u8" || len(v[2].Codecs) != 1 || v[2].Codecs[0] != "mp4a.40.2" {
					t.Errorf("rendition = %+v", v[2])
				}
			}},
		{name: "quoted commas in attributes", body: "#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"mp4a.40.2,mp3\",BANDWIDTH=1\nx.m3u8\n", master: true,
			check: func(t *testing.T, m *Master, _ *Media) {
				if len(m.Variants[0].Codecs) != 2 || m.Variants[0].Bandwidth != 1 {
					t.Errorf("v = %+v", m.Variants[0])
				}
			}},
		{name: "title attributes", body: "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,artist=\"A, B\",title=\"T\"\ns.aac\n",
			check: func(t *testing.T, _ *Master, md *Media) {
				if md.Segments[0].Title != "A, B - T" {
					t.Errorf("title = %q", md.Segments[0].Title)
				}
			}},
		{name: "unknown tags and parts ignored", body: "#EXTM3U\n#EXT-X-VERSION:9\n#EXT-X-TARGETDURATION:6\n#EXT-X-PART:DURATION=1,URI=\"p.m4s\"\n#EXT-X-PRELOAD-HINT:TYPE=PART,URI=\"n.m4s\"\n#EXTINF:6,\ns.aac\n#FOO\n#comment\n",
			check: func(t *testing.T, _ *Master, md *Media) {
				if len(md.Segments) != 1 {
					t.Errorf("segments = %d", len(md.Segments))
				}
			}},
		{name: "key none passes", body: "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-KEY:METHOD=NONE\n#EXTINF:6,\ns.aac\n"},
		{name: "empty", body: "", err: ErrNotPlaylist},
		{name: "html", body: "<html>", err: ErrNotPlaylist},
		{name: "plain m3u", body: "#EXTM3U\nhttp://x/stream\n", err: ErrNotPlaylist},
		{name: "aes", body: "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n#EXTINF:6,\ns.aac\n", err: ErrLocked},
		{name: "sample aes", body: "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-KEY:METHOD=SAMPLE-AES,URI=\"k\"\n#EXTINF:6,\ns.aac\n", err: ErrLocked},
		{name: "session key", body: "#EXTM3U\n#EXT-X-SESSION-KEY:METHOD=AES-128,URI=\"k\"\n#EXT-X-STREAM-INF:BANDWIDTH=1\nx.m3u8\n", err: ErrLocked},
		{name: "byterange", body: "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\n#EXT-X-BYTERANGE:100@0\ns.aac\n", err: ErrUnsupported},
		{name: "master naming media tags", body: "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nx.m3u8\n#EXTINF:6,\ns.aac\n", err: ErrUnsupported},
		{name: "target too long", body: "#EXTM3U\n#EXT-X-TARGETDURATION:31\n#EXTINF:6,\ns.aac\n", err: ErrUnsupported},
		{name: "target zero", body: "#EXTM3U\n#EXT-X-TARGETDURATION:0\n#EXTINF:6,\ns.aac\n", err: ErrUnsupported},
		{name: "target missing", body: "#EXTM3U\n#EXTINF:6,\ns.aac\n", err: ErrUnsupported},
		{name: "too many segments", body: many, err: ErrTooLarge},
		{name: "line too long", body: long, err: ErrTooLarge},
		{name: "body too large", body: "#EXTM3U\n" + strings.Repeat("#x\n", maxPlaylist), err: ErrTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, md, err := Parse([]byte(tc.body), base)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if tc.err != nil {
				if m != nil || md != nil {
					t.Fatal("results set with an error")
				}
				return
			}
			if (m != nil) == (md != nil) || (m != nil) != tc.master {
				t.Fatalf("master=%v media=%v", m, md)
			}
			if tc.check != nil {
				tc.check(t, m, md)
			}
		})
	}
}

func TestChooseVariant(t *testing.T) {
	aac := []string{"mp4a.40.2"}
	tests := []struct {
		name string
		vs   []Variant
		want string
		err  error
	}{
		{name: "highest up to 320k", vs: []Variant{{URI: "64", Bandwidth: 64000, Codecs: aac}, {URI: "128", Bandwidth: 128000, Codecs: aac},
			{URI: "320", Bandwidth: 320000, Codecs: aac}, {URI: "1M", Bandwidth: 1000000, Codecs: aac}}, want: "320"},
		{name: "only above 320k takes the lowest", vs: []Variant{{URI: "512", Bandwidth: 512000, Codecs: aac}}, want: "512"},
		{name: "lowest of the large ones", vs: []Variant{{URI: "900", Bandwidth: 900000, Codecs: aac}, {URI: "512", Bandwidth: 512000, Codecs: aac}}, want: "512"},
		{name: "video only", vs: []Variant{{URI: "v", Bandwidth: 1, Codecs: []string{"avc1.4d401f", "mp4a.40.2"}, Resolution: "640x360"}}, err: ErrVideo},
		{name: "video codec without resolution", vs: []Variant{{URI: "v", Codecs: []string{"hvc1.1.6.L93", "mp4a.40.2"}}}, err: ErrVideo},
		{name: "resolution without codecs is skipped", vs: []Variant{{URI: "v", Resolution: "1280x720"}, {URI: "a", Bandwidth: 64000, Codecs: aac}}, want: "a"},
		{name: "ac-3 only", vs: []Variant{{URI: "x", Codecs: []string{"ac-3"}}, {URI: "y", Codecs: []string{"ec-3"}}}, err: ErrCodec},
		{name: "ac-3 beside aac", vs: []Variant{{URI: "x", Bandwidth: 1, Codecs: []string{"ac-3"}}, {URI: "y", Bandwidth: 2, Codecs: []string{"MP4A.40.2"}}}, want: "y"},
		{name: "mp3 and no codec list", vs: []Variant{{URI: "m", Bandwidth: 128000, Codecs: []string{"mp3"}}, {URI: "n", Bandwidth: 96000}}, want: "m"},
		{name: "empty", err: ErrVideo},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := ChooseVariant(&Master{Variants: tc.vs})
			if !errors.Is(err, tc.err) || v.URI != tc.want {
				t.Fatalf("got %q, %v; want %q, %v", v.URI, err, tc.want, tc.err)
			}
		})
	}
}

func TestID3Title(t *testing.T) {
	tests := []struct {
		name string
		tag  []byte
		want string
	}{
		{"v2.3 latin1", synthID3(3, 0, "Song", "Band"), "Band - Song"},
		{"v2.3 utf16 bom", synthID3(3, 1, "Sóng", "Bänd"), "Bänd - Sóng"},
		{"v2.4 utf8", synthID3(4, 3, "Sång", "Bånd"), "Bånd - Sång"},
		{"v2.4 utf16be", synthID3(4, 2, "Song", ""), "Song"},
		{"artist only", synthID3(4, 3, "", "Band"), "Band"},
		{"no frames", synthID3(4, 3, "", ""), ""},
		{"not a tag", []byte("hello world, not a tag"), ""},
		{"v2.2 not read", append([]byte{'I', 'D', '3', 2, 0, 0}, syncsafeBytes(0)...), ""},
		{"bad syncsafe", []byte{'I', 'D', '3', 4, 0, 0, 0x80, 0, 0, 0, 1, 2}, ""},
	}
	full := synthID3(4, 3, "Truncated song", "Band")
	for cut := 0; cut < len(full); cut += 3 {
		tests = append(tests, struct {
			name string
			tag  []byte
			want string
		}{"cut " + string(rune('A'+cut/3%26)), full[:cut], "-"}) // only checked for no panic
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := id3Title(tc.tag)
			if tc.want != "-" && got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
	if n := id3Len(synthID3(4, 3, "a", "b")); n != len(synthID3(4, 3, "a", "b")) {
		t.Errorf("id3Len = %d", n)
	}
}

func TestScanADTS(t *testing.T) {
	good := synthADTS(4, 4, 2, 100)
	tests := []struct {
		name   string
		in     []byte
		frames int // expected output length in whole frames of 107 bytes
		f      Format
		err    error
	}{
		{"clean", good, 4, Format{"aac", 44100, 2}, nil},
		{"trailing partial dropped", append(append([]byte{}, good...), good[:50]...), 4, Format{"aac", 44100, 2}, nil},
		{"junk then frames", append(bytes.Repeat([]byte{0x11}, 300), good...), 4, Format{"aac", 44100, 2}, nil},
		{"junk too long", append(bytes.Repeat([]byte{0x11}, maxResync+10), good...), 0, Format{}, ErrCodec},
		{"mono 48k", synthADTS(2, 3, 1, 10), 0, Format{"aac", 48000, 1}, nil},
		{"empty", nil, 0, Format{}, ErrCodec},
		{"only a partial frame", good[:50], 0, Format{}, ErrCodec},
		{"format change", append(synthADTS(2, 4, 2, 100), synthADTS(2, 3, 2, 100)...), 0, Format{}, ErrChanged},
		{"stops at junk", append(append([]byte{}, good...), bytes.Repeat([]byte{0x22}, 40)...), 4, Format{"aac", 44100, 2}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, f, err := scanADTS(tc.in)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			if f != tc.f {
				t.Errorf("format = %+v, want %+v", f, tc.f)
			}
			if tc.frames > 0 && len(frames) != tc.frames*107 {
				t.Errorf("len = %d, want %d", len(frames), tc.frames*107)
			}
		})
	}
}

func TestADTSHeader(t *testing.T) {
	h := adtsHeader(1, 4, 2, 100)
	n, f, ok := adtsHead(h[:])
	if !ok || n != 107 || f != (Format{"aac", 44100, 2}) {
		t.Fatalf("round trip: %d %+v %v", n, f, ok)
	}
	want := [7]byte{0xFF, 0xF1, 0x50, 0x80, 0x0D, 0x7F, 0xFC}
	if h != want {
		t.Errorf("header = % x, want % x", h, want)
	}
}

func TestScanMPEG(t *testing.T) {
	good := synthMPEG(5, false)
	tests := []struct {
		name string
		in   []byte
		n    int
		f    Format
		err  error
	}{
		{"stereo", good, 5 * 417, Format{"mp3", 44100, 2}, nil},
		{"mono", synthMPEG(2, true), 2 * 417, Format{"mp3", 44100, 1}, nil},
		{"partial dropped", append(append([]byte{}, good...), good[:100]...), 5 * 417, Format{"mp3", 44100, 2}, nil},
		{"free format", []byte{0xFF, 0xFB, 0x00, 0x00, 1, 2, 3, 4, 5, 6, 7, 8}, 0, Format{}, ErrCodec},
		{"layer II", []byte{0xFF, 0xFD, 0x90, 0x00, 1, 2, 3, 4, 5, 6, 7, 8}, 0, Format{}, ErrCodec},
		{"adts is not mpeg", synthADTS(3, 4, 2, 50), 0, Format{}, ErrCodec},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, f, err := scanMPEG(tc.in)
			if !errors.Is(err, tc.err) || (err == nil && (f != tc.f || len(frames) != tc.n)) {
				t.Fatalf("got %d bytes, %+v, %v; want %d, %+v, %v", len(frames), f, err, tc.n, tc.f, tc.err)
			}
		})
	}
}

func TestDemuxPacked(t *testing.T) {
	tag := synthID3(4, 3, "Song", "Band")
	tests := []struct {
		name   string
		in     []byte
		n      int
		f      Format
		titles []string
		err    error
	}{
		{"aac with tag", append(append([]byte{}, tag...), synthADTS(3, 4, 2, 100)...), 3 * 107, Format{"aac", 44100, 2}, []string{"Band - Song"}, nil},
		{"mp3 with two tags", append(append(append([]byte{}, tag...), synthID3(3, 0, "Other", "")...), synthMPEG(2, false)...), 2 * 417, Format{"mp3", 44100, 2}, []string{"Band - Song", "Other"}, nil},
		{"bare adts", synthADTS(2, 6, 1, 20), 2 * 27, Format{"aac", 24000, 1}, nil, nil},
		{"tag only", tag, 0, Format{}, nil, ErrCodec},
		{"tag larger than data", tag[:len(tag)-4], 0, Format{}, nil, ErrCodec},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, f, titles, err := Demux(tc.in, nil)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			if len(frames) != tc.n || f != tc.f || strings.Join(titles, "|") != strings.Join(tc.titles, "|") {
				t.Errorf("got %d, %+v, %q", len(frames), f, titles)
			}
		})
	}
}

func TestDemuxTS(t *testing.T) {
	adts := synthADTS(8, 4, 2, 300)
	mp3 := synthMPEG(6, false)
	id3 := synthID3(4, 3, "Live song", "Station")
	tests := []struct {
		name string
		in   []byte
		n    int
		f    Format
		tit  string
		err  error
	}{
		{"aac", synthTS([]tsStreamSpec{{stADTS, 0x101}}, map[int][][]byte{0x101: {adts}}), len(adts), Format{"aac", 44100, 2}, "", nil},
		{"mp3", synthTS([]tsStreamSpec{{stMPEG1Audio, 0x101}}, map[int][][]byte{0x101: {mp3}}), len(mp3), Format{"mp3", 44100, 2}, "", nil},
		{"mpeg2 audio type", synthTS([]tsStreamSpec{{stMPEG2Audio, 0x101}}, map[int][][]byte{0x101: {mp3}}), len(mp3), Format{"mp3", 44100, 2}, "", nil},
		{"video pid ignored", synthTS([]tsStreamSpec{{0x1B, 0x200}, {stADTS, 0x101}}, map[int][][]byte{0x200: {bytes.Repeat([]byte{9}, 500)}, 0x101: {adts}}), len(adts), Format{"aac", 44100, 2}, "", nil},
		{"id3 pid title", synthTS([]tsStreamSpec{{stADTS, 0x101}, {stMetadata, 0x102}}, map[int][][]byte{0x101: {adts}, 0x102: {id3}}), len(adts), Format{"aac", 44100, 2}, "Station - Live song", nil},
		{"split pes", synthTS([]tsStreamSpec{{stADTS, 0x101}}, map[int][][]byte{0x101: {adts[:1000], adts[1000:]}}), len(adts), Format{"aac", 44100, 2}, "", nil},
		{"latm", synthTS([]tsStreamSpec{{stLATM, 0x101}}, map[int][][]byte{0x101: {adts}}), 0, Format{}, "", ErrCodec},
		{"ac-3", synthTS([]tsStreamSpec{{stAC3, 0x101}}, nil), 0, Format{}, "", ErrCodec},
		{"video only", synthTS([]tsStreamSpec{{0x1B, 0x200}}, map[int][][]byte{0x200: {{1, 2, 3}}}), 0, Format{}, "", ErrVideo},
		{"garbage audio", synthTS([]tsStreamSpec{{stADTS, 0x101}}, map[int][][]byte{0x101: {bytes.Repeat([]byte{7}, 400)}}), 0, Format{}, "", ErrCodec},
	}
	bad := synthTS([]tsStreamSpec{{stADTS, 0x101}}, map[int][][]byte{0x101: {adts}})
	bad[tsPacket*3] = 0x00 // a lost sync byte in the middle
	tests = append(tests, struct {
		name string
		in   []byte
		n    int
		f    Format
		tit  string
		err  error
	}{"bad sync", bad, 0, Format{}, "", ErrUnsupported})
	scrambled := synthTS([]tsStreamSpec{{stADTS, 0x101}}, map[int][][]byte{0x101: {adts}})
	scrambled[tsPacket*2+3] |= 0x80
	tests = append(tests, struct {
		name string
		in   []byte
		n    int
		f    Format
		tit  string
		err  error
	}{"scrambled", scrambled, 0, Format{}, "", ErrLocked})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, f, titles, err := Demux(tc.in, nil)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			if len(frames) != tc.n || f != tc.f {
				t.Errorf("got %d bytes, %+v; want %d, %+v", len(frames), f, tc.n, tc.f)
			}
			if got := strings.Join(titles, "|"); got != tc.tit {
				t.Errorf("titles = %q, want %q", got, tc.tit)
			}
			if f.Codec == "aac" && !bytes.Equal(frames, adts) {
				t.Error("frames differ from the source ADTS")
			}
		})
	}
}

func TestDemuxFMP4(t *testing.T) {
	samples := [][]byte{bytes.Repeat([]byte{1}, 200), bytes.Repeat([]byte{2}, 150), bytes.Repeat([]byte{3}, 90)}
	frag := synthFragment(1, samples)
	tests := []struct {
		name    string
		asc     []byte
		oti     byte
		entry   string
		wantHdr [2]byte // bytes 2 and 3 of the first ADTS header
		f       Format
		err     error
	}{
		{"LC 44.1k stereo", []byte{0x12, 0x10}, 0x40, "mp4a", [2]byte{0x50, 0x80}, Format{"aac", 44100, 2}, nil},
		// HE-AAC v1: object type 5, core rate index 6 (24 kHz), 2 channels, extension rate 48 kHz, core type LC.
		{"HE-AAC v1", []byte{0x2B, 0x11, 0x88}, 0x40, "mp4a", [2]byte{0x58, 0x80}, Format{"aac", 24000, 2}, nil},
		{"encrypted", []byte{0x12, 0x10}, 0x40, "enca", [2]byte{}, Format{}, ErrLocked},
		{"ac-3 track", []byte{0x12, 0x10}, 0x40, "ac-3", [2]byte{}, Format{}, ErrCodec},
		{"unknown object type", []byte{0x12, 0x10}, 0xA5, "mp4a", [2]byte{}, Format{}, ErrCodec},
		{"explicit rate", []byte{0x17, 0x80, 0x00}, 0x40, "mp4a", [2]byte{}, Format{}, ErrCodec},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames, f, _, err := Demux(frag, synthInit(1, tc.oti, tc.asc, tc.entry))
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if err != nil {
				return
			}
			if f != tc.f {
				t.Errorf("format = %+v", f)
			}
			if frames[2] != tc.wantHdr[0] || frames[3] != tc.wantHdr[1] {
				t.Errorf("header bytes = % x", frames[:7])
			}
			n, _, ok := adtsHead(frames)
			if !ok || n != 207 || len(frames) != 207+157+97 {
				t.Errorf("first frame length %d, total %d", n, len(frames))
			}
			if !bytes.Equal(frames[7:207], samples[0]) {
				t.Error("payload changed")
			}
		})
	}
	t.Run("mp3 in mp4", func(t *testing.T) {
		mp3 := synthMPEG(3, false)
		frag := synthFragment(1, [][]byte{mp3[:417], mp3[417:834], mp3[834:]})
		frames, f, _, err := Demux(frag, synthInit(1, 0x6B, []byte{0}, "mp4a"))
		if err != nil || f.Codec != "mp3" || !bytes.Equal(frames, mp3) {
			t.Fatalf("got %d bytes, %+v, %v", len(frames), f, err)
		}
	})
	t.Run("no init", func(t *testing.T) {
		if _, _, _, err := Demux(frag, nil); !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("other track ignored", func(t *testing.T) {
		if _, _, _, err := Demux(synthFragment(2, samples), synthInit(1, 0x40, []byte{0x12, 0x10}, "mp4a")); !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("too large", func(t *testing.T) {
		if _, _, _, err := Demux(make([]byte, MaxSegment+1), nil); !errors.Is(err, ErrTooLarge) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("truncated", func(t *testing.T) {
		init := synthInit(1, 0x40, []byte{0x12, 0x10}, "mp4a")
		for cut := range len(frag) {
			Demux(frag[:cut], init)
		}
		for cut := range len(init) {
			Demux(frag, init[:cut])
		}
	})
}

func FuzzDemux(f *testing.F) {
	adts := synthADTS(3, 4, 2, 100)
	f.Add(adts, []byte(nil))
	f.Add(append(synthID3(4, 3, "t", "a"), synthMPEG(2, false)...), []byte(nil))
	f.Add(synthTS([]tsStreamSpec{{stADTS, 0x101}, {stMetadata, 0x102}}, map[int][][]byte{0x101: {adts}, 0x102: {synthID3(3, 1, "x", "y")}}), []byte(nil))
	f.Add(synthTS([]tsStreamSpec{{stMPEG1Audio, 0x101}}, map[int][][]byte{0x101: {synthMPEG(2, true)}}), []byte(nil))
	f.Add(synthFragment(1, [][]byte{{1, 2, 3}, {4, 5}}), synthInit(1, 0x40, []byte{0x12, 0x10}, "mp4a"))
	f.Add(synthFragment(1, [][]byte{{1, 2, 3}}), synthInit(1, 0x40, []byte{0x2B, 0x11, 0x88}, "mp4a"))
	f.Fuzz(func(t *testing.T, seg, init []byte) {
		frames, _, _, err := Demux(seg, init)
		if err == nil && len(frames) == 0 {
			t.Fatal("success with no frames")
		}
		if len(frames) > MaxSegment+MaxSegment/2 {
			t.Fatalf("output %d bytes from %d", len(frames), len(seg))
		}
	})
}

func FuzzParse(f *testing.F) {
	f.Add([]byte("#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXT-X-MEDIA-SEQUENCE:9\n#EXT-X-MAP:URI=\"i.mp4\"\n#EXTINF:6,t\nS.ts\n"))
	f.Add([]byte("#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"a\",URI=\"x.m3u8\"\n#EXT-X-STREAM-INF:BANDWIDTH=1,CODECS=\"mp4a.40.2\",AUDIO=\"a\"\nv.m3u8\n"))
	f.Add([]byte("\xEF\xBB\xBF#EXTM3U\r\n#EXT-X-KEY:METHOD=AES-128\r\n"))
	f.Add([]byte("#EXTM3U\n#EXTINF:1,title=\"a\",artist=\"b\nx\n"))
	base, _ := url.Parse("https://h.example/p/q.m3u8")
	f.Fuzz(func(t *testing.T, body []byte) {
		m, md, err := Parse(body, base)
		if err == nil && (m == nil) == (md == nil) {
			t.Fatal("exactly one result must be set")
		}
		if m != nil {
			ChooseVariant(m)
		}
		if md != nil && len(md.Segments) > MaxSegments {
			t.Fatal("too many segments")
		}
	})
}
