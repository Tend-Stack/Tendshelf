package media

import (
	"reflect"
	"testing"
)

func TestProbeArgsExact(t *testing.T) {
	got := probeArgs("/a b/c.m4b")
	want := []string{"-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe",
		"-show_error", "-show_format", "-show_streams", "-show_chapters", "-show_packets",
		"-read_intervals", "%+#8", "-print_format", "json", "-i", "file:/a b/c.m4b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args =\n%q\nwant\n%q", got, want)
	}
}

func TestParseSecondsMs(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"6.000000", 6000, true},
		{"6.037007", 6037, true},
		{"0.0005", 1, true},
		{"0.0004", 0, true},
		{"0.9995", 1000, true},
		{"5", 5000, true},
		{".5", 500, true},
		{"1e3", 1000000, true},
		{"1E-3", 1, true},
		{"N/A", 0, false},
		{"", 0, false},
		{"-1", 0, false},
		{"-0.5", 0, false},
		{"abc", 0, false},
		{"1.2.3", 0, false},
		{"1e400", 0, false},
		{"99999999999999", 0, false},
	}
	for _, c := range cases {
		got, ok := parseSecondsMs(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("parseSecondsMs(%q) = %d, %v; want %d, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestParseProbe(t *testing.T) {
	cases := []struct {
		name  string
		json  string
		check func(t *testing.T, p Probe)
	}{
		{"m4b cover and chapters", `{
 "packets":[{"stream_index":1},{"stream_index":0},{"stream_index":0}],
 "streams":[
  {"index":0,"codec_name":"aac","codec_type":"audio","codec_tag_string":"mp4a","profile":"LC","sample_rate":"22050","channels":1,"bit_rate":"34000","duration":"6.011000","disposition":{"attached_pic":0}},
  {"index":1,"codec_name":"mjpeg","codec_type":"video","width":64,"height":64,"disposition":{"attached_pic":1}}],
 "chapters":[
  {"start_time":"4.000000","end_time":"6.000000","tags":{"TITLE":" Three "}},
  {"start_time":"0.000000","end_time":"2.000000","tags":{"title":"One"}},
  {"start_time":"2.000000","end_time":"1.000000","tags":{"title":"backwards"}},
  {"start_time":"N/A","end_time":"3.000000"}],
 "format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2","duration":"6.037007","size":"12345","bit_rate":"16000","tags":{"Title":"Book","artist":"  ","ALBUM":"Al"}}}`,
			func(t *testing.T, p Probe) {
				if p.Container != "mp4" || p.DurationMs != 6037 || p.Size != 12345 || p.BitRate != 16000 || p.Encrypted || p.HasVideo {
					t.Fatalf("probe = %+v", p)
				}
				if !reflect.DeepEqual(p.Tags, map[string]string{"title": "Book", "album": "Al"}) {
					t.Errorf("tags = %v", p.Tags)
				}
				if len(p.Audio) != 1 || p.Audio[0].Codec != "aac" || p.Audio[0].Channels != 1 || p.Audio[0].SampleRate != 22050 || p.Audio[0].DurationMs != 6011 {
					t.Errorf("audio = %+v", p.Audio)
				}
				if len(p.Pictures) != 1 || p.Pictures[0].Width != 64 || p.Pictures[0].Codec != "mjpeg" {
					t.Errorf("pictures = %+v", p.Pictures)
				}
				want := []ProbeChapter{{0, 2000, "One"}, {4000, 6000, "Three"}}
				if !reflect.DeepEqual(p.Chapters, want) {
					t.Errorf("chapters = %+v, want %+v", p.Chapters, want)
				}
			}},
		{"opus tags live on the stream", `{"streams":[{"index":0,"codec_name":"opus","codec_type":"audio","channels":2,"sample_rate":"48000","duration":"10.5","tags":{"TITLE":"Spoken","ARTIST":"Ana"}}],
 "format":{"format_name":"ogg","duration":"N/A","tags":{"title":"Format wins"}}}`,
			func(t *testing.T, p Probe) {
				if p.Container != "ogg" || p.DurationMs != 10500 {
					t.Errorf("container %q duration %d", p.Container, p.DurationMs)
				}
				if p.Tags["title"] != "Format wins" || p.Tags["artist"] != "Ana" {
					t.Errorf("tags = %v", p.Tags)
				}
			}},
		{"flac upper-case keys, webm container", `{"streams":[{"index":0,"codec_name":"flac","codec_type":"audio","channels":1,"sample_rate":"44100"}],
 "format":{"format_name":"matroska,webm","duration":"2.0","tags":{"TITLE":"T","ARTIST":"A"}}}`,
			func(t *testing.T, p Probe) {
				if p.Container != "webm" || p.Tags["title"] != "T" || p.Tags["artist"] != "A" {
					t.Errorf("probe = %+v", p)
				}
			}},
		{"cenc packets", `{"packets":[{"stream_index":0,"side_data_list":[{"side_data_type":"Encryption info"}]}],
 "streams":[{"index":0,"codec_name":"aac","codec_type":"audio","codec_tag_string":"mp4a","channels":1}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`,
			func(t *testing.T, p Probe) {
				if !p.Encrypted || p.Protection != ProtectionPacketSide {
					t.Errorf("encrypted=%v protection=%q", p.Encrypted, p.Protection)
				}
			}},
		{"cover packet side data does not count", `{"packets":[{"stream_index":1,"side_data_list":[{"side_data_type":"Encryption info"}]}],
 "streams":[{"index":0,"codec_type":"audio","codec_name":"aac"},{"index":1,"codec_type":"video","codec_name":"mjpeg","disposition":{"attached_pic":1}}],"format":{"format_name":"mp3"}}`,
			func(t *testing.T, p Probe) {
				if p.Encrypted {
					t.Errorf("protection=%q", p.Protection)
				}
			}},
		{"codec tag drms", `{"streams":[{"index":0,"codec_name":"aac","codec_type":"audio","codec_tag_string":"drms"}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`,
			func(t *testing.T, p Probe) {
				if !p.Encrypted || p.Protection != ProtectionCodecTag {
					t.Errorf("encrypted=%v protection=%q", p.Encrypted, p.Protection)
				}
			}},
		{"stream side data", `{"streams":[{"index":0,"codec_name":"aac","codec_type":"audio","side_data_list":[{"side_data_type":"ENCRYPTION INIT info"}]}],"format":{"format_name":"mp4"}}`,
			func(t *testing.T, p Probe) {
				if !p.Encrypted || p.Protection != ProtectionStreamSide {
					t.Errorf("encrypted=%v protection=%q", p.Encrypted, p.Protection)
				}
			}},
		{"no audio, plain video", `{"streams":[{"index":0,"codec_name":"h264","codec_type":"video","disposition":{"attached_pic":0}}],"format":{"format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`,
			func(t *testing.T, p Probe) {
				if len(p.Audio) != 0 || !p.HasVideo || len(p.Pictures) != 0 {
					t.Errorf("probe = %+v", p)
				}
			}},
		{"duration from the longest audio stream", `{"streams":[{"index":0,"codec_type":"audio","duration":"3.5"},{"index":1,"codec_type":"audio","duration":"7.25"}],"format":{"format_name":"wav"}}`,
			func(t *testing.T, p Probe) {
				if p.DurationMs != 7250 {
					t.Errorf("duration = %d", p.DurationMs)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := parseProbe([]byte(c.json))
			if err != nil {
				t.Fatal(err)
			}
			if p.V != 1 {
				t.Errorf("V = %d", p.V)
			}
			c.check(t, p)
		})
	}
	if _, err := parseProbe([]byte("not json")); err == nil {
		t.Error("garbage must not parse")
	}
}
