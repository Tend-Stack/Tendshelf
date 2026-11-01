// Package mediatest gives tests a fake ffprobe and, when TENDSHELF_TEST_FFMPEG is set, real
// ffmpeg-made audio files.
//
// The fake is a helper process: the test binary runs itself again with TENDSHELF_FAKE_FFPROBE=1
// (the pattern os/exec's own tests use). No build step, it works under -race, and any package's
// tests can use it by calling RunFakeIfRequested at the top of TestMain:
//
//	func TestMain(m *testing.M) { mediatest.RunFakeIfRequested(); os.Exit(m.Run()) }
//
// A fake audio file is the line "TSFAKE1" followed by a JSON FakeFile description and optional pad
// bytes, so a library of hundreds of books costs no ffmpeg run. The fake rejects an argument list
// that lacks the runner's safety flags, so a regression there fails every test that probes.
package mediatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
)

const (
	fakeEnv   = "TENDSHELF_FAKE_FFPROBE"
	fakeMagic = "TSFAKE1\n"
)

// FakeChapter is one chapter of a fake or synthesised file, in milliseconds.
type FakeChapter struct {
	StartMs, EndMs int64
	Title          string
}

// Chapter is a FakeChapter with its fields named by position, for short literals.
func Chapter(startMs, endMs int64, title string) FakeChapter {
	return FakeChapter{StartMs: startMs, EndMs: endMs, Title: title}
}

// FakeFile describes what the fake ffprobe reports for one file. Zero values give a mono 22.05 kHz
// AAC audio in an MP4 container of 6 seconds.
type FakeFile struct {
	Raw        json.RawMessage `json:",omitempty"` // if set, printed as is instead of the rendered description
	Container  string          // mp4 (default), mp3, flac, ogg, webm, wav, ...
	Codec      string          // default aac
	CodecTag   string          // default mp4a
	DurationMs int64           // default 6000
	BitRate    int64           // default 32000
	Channels   int             // default 1
	SampleRate int             // default 22050
	Tags       map[string]string
	StreamTags map[string]string
	Chapters   []FakeChapter
	Cover      bool // adds an attached picture stream 1 (mjpeg 64x64) whose packet comes first
	Encrypted  bool // audio packets carry "Encryption info" side data

	Exit       int    // exit status; with Raw unset a nonzero exit prints nothing on stdout
	SleepMs    int    // delay before answering
	FloodBytes int    // bytes of junk written to stdout before the answer
	Stderr     string // written to stderr
	PadBytes   int    // bytes of filler after the description, to make big files
}

// WriteFake writes a fake audio file.
func WriteFake(t testing.TB, path string, f FakeFile) {
	t.Helper()
	body, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteString(fakeMagic)
	buf.Write(body)
	buf.WriteByte('\n')
	for i := 0; i < f.PadBytes; i++ {
		buf.WriteByte(byte(i % 251))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// FakeTools returns Tools that run the fake ffprobe (this test binary; GORACE keeps the race
// detector from sleeping a second at exit). The package under test
// must call RunFakeIfRequested in TestMain.
func FakeTools(t testing.TB) media.Tools {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return media.Tools{FFprobe: exe, ProbeTimeout: 5 * time.Second, Env: []string{fakeEnv + "=1", "GORACE=atexit_sleep_ms=0"}}
}

// RunFakeIfRequested turns this process into the fake ffprobe when TENDSHELF_FAKE_FFPROBE=1 and
// never returns in that case. Otherwise it does nothing.
func RunFakeIfRequested() {
	if os.Getenv(fakeEnv) != "1" {
		return
	}
	os.Exit(runFake(os.Args[1:], os.Stdout, os.Stderr))
}

func runFake(args []string, stdout, stderr io.Writer) int {
	n := len(args)
	if !hasPair(args, "-protocol_whitelist", "file,pipe") || n < 2 || args[n-2] != "-i" || !strings.HasPrefix(args[n-1], "file:/") {
		fmt.Fprintln(stderr, "fake ffprobe: unexpected arguments")
		return 64
	}
	path := strings.TrimPrefix(args[n-1], "file:")
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(stdout, `{"error":{"code":-2,"string":"No such file or directory"}}`)
		return 1
	}
	if !bytes.HasPrefix(data, []byte(fakeMagic)) {
		fmt.Fprintln(stderr, path+": Invalid data found when processing input")
		fmt.Fprintln(stdout, `{"error":{"code":-1094995529,"string":"Invalid data found when processing input"}}`)
		return 1
	}
	var f FakeFile
	if err := json.NewDecoder(bytes.NewReader(data[len(fakeMagic):])).Decode(&f); err != nil {
		fmt.Fprintln(stderr, "fake ffprobe: bad description: "+err.Error())
		return 65
	}
	if f.SleepMs > 0 {
		time.Sleep(time.Duration(f.SleepMs) * time.Millisecond)
	}
	if f.Stderr != "" {
		fmt.Fprint(stderr, f.Stderr)
	}
	if f.FloodBytes > 0 {
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for left := f.FloodBytes; left > 0; {
			k := min(left, len(chunk))
			if _, err := stdout.Write(chunk[:k]); err != nil {
				return 1
			}
			left -= k
		}
	}
	if f.Raw != nil {
		stdout.Write(f.Raw)
		return f.Exit
	}
	if f.Exit != 0 {
		return f.Exit
	}
	out, err := json.Marshal(render(path, int64(len(data)), f))
	if err != nil {
		fmt.Fprintln(stderr, "fake ffprobe: "+err.Error())
		return 65
	}
	stdout.Write(out)
	return 0
}

func hasPair(args []string, key, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == key && args[i+1] == val {
			return true
		}
	}
	return false
}

func secs(ms int64) string { return fmt.Sprintf("%.6f", float64(ms)/1000) }

// render builds ffprobe-shaped JSON: the audio stream is stream 0, the optional cover stream 1.
func render(path string, size int64, f FakeFile) map[string]any {
	orDefault := func(s, d string) string {
		if s == "" {
			return d
		}
		return s
	}
	container := orDefault(f.Container, "mp4")
	format := container
	switch container {
	case "mp4":
		format = "mov,mp4,m4a,3gp,3g2,mj2"
	case "webm":
		format = "matroska,webm"
	}
	dur := f.DurationMs
	if dur == 0 {
		dur = 6000
	}
	channels, rate, bitrate := f.Channels, f.SampleRate, f.BitRate
	if channels == 0 {
		channels = 1
	}
	if rate == 0 {
		rate = 22050
	}
	if bitrate == 0 {
		bitrate = 32000
	}
	audio := map[string]any{
		"index": 0, "codec_name": orDefault(f.Codec, "aac"), "codec_type": "audio",
		"codec_tag_string": orDefault(f.CodecTag, "mp4a"), "channels": channels,
		"sample_rate": fmt.Sprint(rate), "bit_rate": fmt.Sprint(bitrate), "duration": secs(dur),
		"disposition": map[string]int{"attached_pic": 0},
	}
	if f.StreamTags != nil {
		audio["tags"] = f.StreamTags
	}
	streams := []any{audio}
	var packets []any
	if f.Cover {
		streams = append(streams, map[string]any{
			"index": 1, "codec_name": "mjpeg", "codec_type": "video", "codec_tag_string": "[0][0][0][0]",
			"width": 64, "height": 64, "disposition": map[string]int{"attached_pic": 1},
		})
		packets = append(packets, map[string]any{"codec_type": "video", "stream_index": 1, "pts": 0})
	}
	for i := 0; i < 3; i++ {
		p := map[string]any{"codec_type": "audio", "stream_index": 0, "pts": i * 1024}
		if f.Encrypted {
			p["side_data_list"] = []any{map[string]any{"side_data_type": "Encryption info"}}
		}
		packets = append(packets, p)
	}
	chapters := []any{}
	for i, c := range f.Chapters {
		chapters = append(chapters, map[string]any{
			"id": i, "time_base": "1/1000", "start": c.StartMs, "start_time": secs(c.StartMs),
			"end": c.EndMs, "end_time": secs(c.EndMs), "tags": map[string]string{"title": c.Title},
		})
	}
	fm := map[string]any{
		"filename": path, "nb_streams": len(streams), "format_name": format,
		"duration": secs(dur), "size": fmt.Sprint(size), "bit_rate": fmt.Sprint(bitrate),
	}
	if f.Tags != nil {
		fm["tags"] = f.Tags
	}
	return map[string]any{"packets": packets, "streams": streams, "chapters": chapters, "format": fm}
}
