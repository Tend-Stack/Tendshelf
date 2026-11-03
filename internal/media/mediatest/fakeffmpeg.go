package mediatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Environment variables a test may add to Tools.Env to steer the fake ffmpeg for any input, not
// only for a fake audio file (FakeFile.FFmpegExit and FFmpegSleepMs cover those).
const (
	// FakeFFmpegLogEnv names a file; every run of the fake ffmpeg appends one line of arguments to it.
	FakeFFmpegLogEnv = "TENDSHELF_FAKE_FFMPEG_LOG"
	// FakeFFmpegExitEnv makes every run exit with this status after logging.
	FakeFFmpegExitEnv = "TENDSHELF_FAKE_FFMPEG_EXIT"
	// FakeFFmpegSleepEnv delays every answer by this many milliseconds.
	FakeFFmpegSleepEnv = "TENDSHELF_FAKE_FFMPEG_SLEEP_MS"
	// FakeFFmpegFloodEnv makes every run write this many bytes of junk to stdout first.
	FakeFFmpegFloodEnv = "TENDSHELF_FAKE_FFMPEG_FLOOD"
	// FakeFFmpegPCMEnv shapes the 8 kHz mono PCM the fake writes for a waveform decode: a list
	// "<ms>:<amplitude>,<ms>:<amplitude>" of square-wave stretches. The default is 6000:8192.
	FakeFFmpegPCMEnv = "TENDSHELF_FAKE_FFMPEG_PCM"
)

const defaultCoverColor = "#2c6660"

var scaleBox = regexp.MustCompile(`min\((\d+),iw\)`)

// runFakeFFmpeg plays ffmpeg for the four commands Tendshelf runs, on fake audio files and on real
// JPEG and PNG images (the fake never reads WebP; that is for real ffmpeg tests):
//
//	a. picture extraction (image2pipe) from a fake audio file: a 64x64 solid CoverColor JPEG
//	   (stream copy) or PNG (-c:v png) on stdout; exit 1 if the fake file has no cover
//	b. image to PNG (image2pipe) from a JPEG or PNG: the decoded image as PNG on stdout
//	c. thumbnail (-vf): the input scaled to fit the box, never enlarged, as JPEG in the output file
//	d. compatibility copy (-c:a): a fake MP4/AAC file of the input's duration in the output file
//
// Two more commands serve the write engine (internal/media/writetags.go):
//
//	e. audio hash (-f hash): "SHA256=" plus the sha256 of the bytes after the fake file's description
//	   line (its pad bytes, the stand-in for audio packets), so tags never change it; for a file
//	   that is not a fake, the sha256 of the whole file
//	f. tag write (-map_metadata 0 -c copy, output is the last argument): the output is the input's
//	   description with every "-metadata k=v" merged into Tags (keys lower-cased, like ffprobe
//	   reports them), the chapters of the -f ffmetadata input if -map_chapters is 1, and the same pad
//	   bytes. The fake ffprobe then reads the new tags and chapters back. A missing "-id3v2_version"
//	   or "-movflags" is not checked here; the argument tests do that.
//
// Like the fake ffprobe it rejects argument lists that lack the runner's safety flags.
func runFakeFFmpeg(args []string, stdout, stderr io.Writer) int {
	in, ok := inputArg(args)
	if !hasPair(args, "-protocol_whitelist", "file,pipe") || !hasArg(args, "-nostdin") || !ok {
		fmt.Fprintln(stderr, "fake ffmpeg: unexpected arguments")
		return 64
	}
	if p := os.Getenv(FakeFFmpegLogEnv); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintln(f, strings.Join(args, " "))
			f.Close()
		}
	}
	if ms, _ := strconv.Atoi(os.Getenv(FakeFFmpegSleepEnv)); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if n, _ := strconv.Atoi(os.Getenv(FakeFFmpegFloodEnv)); n > 0 {
		chunk := bytes.Repeat([]byte("x"), 32<<10)
		for left := n; left > 0; {
			k := min(left, len(chunk))
			if _, err := stdout.Write(chunk[:k]); err != nil {
				return 1
			}
			left -= k
		}
	}
	if code, _ := strconv.Atoi(os.Getenv(FakeFFmpegExitEnv)); code != 0 {
		fmt.Fprintln(stderr, "fake ffmpeg: failing on request")
		return code
	}
	f, err := os.Open(in)
	if err != nil {
		fmt.Fprintf(stderr, "%s: No such file or directory\n", in)
		return 1
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	f.Close()
	if err != nil {
		fmt.Fprintln(stderr, "fake ffmpeg: "+err.Error())
		return 1
	}

	var ff FakeFile
	isFake := bytes.HasPrefix(data, []byte(fakeMagic))
	if isFake {
		if err := json.NewDecoder(bytes.NewReader(data[len(fakeMagic):])).Decode(&ff); err != nil {
			fmt.Fprintln(stderr, "fake ffmpeg: bad description: "+err.Error())
			return 65
		}
		if ff.FFmpegSleepMs > 0 {
			time.Sleep(time.Duration(ff.FFmpegSleepMs) * time.Millisecond)
		}
		if ff.FFmpegExit != 0 {
			fmt.Fprintln(stderr, "fake ffmpeg: failing on request")
			return ff.FFmpegExit
		}
	}

	switch {
	case hasPair(args, "-f", "hash"): // e
		return fakeAudioHash(in, stdout, stderr)
	case hasPair(args, "-map_metadata", "0") && hasPair(args, "-c", "copy"): // f
		return fakeWriteTags(args, in, isFake, stderr)
	case hasArg(args, "image2pipe") && isFake: // a
		if !ff.Cover {
			fmt.Fprintln(stderr, in+": Output file does not contain any stream")
			return 1
		}
		c, err := parseColor(ff.CoverColor)
		if err != nil {
			fmt.Fprintln(stderr, "fake ffmpeg: "+err.Error())
			return 65
		}
		img := solidImage(64, 64, c)
		if hasPair(args, "-c:v", "png") {
			err = png.Encode(stdout, img)
		} else {
			err = jpeg.Encode(stdout, img, &jpeg.Options{Quality: 90})
		}
		return exitFor(err, stderr)
	case hasArg(args, "image2pipe"): // b
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			fmt.Fprintln(stderr, in+": Invalid data found when processing input")
			return 1
		}
		return exitFor(png.Encode(stdout, img), stderr)
	case hasArg(args, "-vf"): // c
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			fmt.Fprintln(stderr, in+": Invalid data found when processing input")
			return 1
		}
		box := 0
		if m := scaleBox.FindStringSubmatch(strings.Join(args, " ")); m != nil {
			box, _ = strconv.Atoi(m[1])
		}
		if box <= 0 {
			fmt.Fprintln(stderr, "fake ffmpeg: no scale box")
			return 64
		}
		out, ok := outputArg(args)
		if !ok {
			fmt.Fprintln(stderr, "fake ffmpeg: unexpected arguments")
			return 64
		}
		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, fit(img, box), &jpeg.Options{Quality: 85}); err != nil {
			return exitFor(err, stderr)
		}
		return exitFor(os.WriteFile(out, buf.Bytes(), 0o600), stderr)
	case hasArg(args, "s16le"): // waveform decode, before d because it also carries -c:a
		pcm, err := fakePCM(os.Getenv(FakeFFmpegPCMEnv))
		if err != nil {
			fmt.Fprintln(stderr, "fake ffmpeg: "+err.Error())
			return 65
		}
		_, err = stdout.Write(pcm)
		return exitFor(err, stderr)
	case hasArg(args, "-c:a"): // d
		out, ok := outputArg(args)
		if !ok || !isFake {
			fmt.Fprintln(stderr, "fake ffmpeg: unexpected arguments")
			return 64
		}
		dur := ff.DurationMs
		if dur == 0 {
			dur = 6000
		}
		body, err := json.Marshal(FakeFile{Container: "mp4", Codec: "aac", DurationMs: dur})
		if err != nil {
			return exitFor(err, stderr)
		}
		return exitFor(os.WriteFile(out, append(append([]byte(fakeMagic), body...), '\n'), 0o600), stderr)
	}
	fmt.Fprintln(stderr, "fake ffmpeg: unexpected arguments")
	return 64
}

// fakePCM renders the stretches of spec as little-endian s16 samples at 8 kHz: a 1 kHz square wave
// of +-amplitude, whose RMS is the amplitude.
func fakePCM(spec string) ([]byte, error) {
	if spec == "" {
		spec = "6000:8192"
	}
	var out []byte
	for _, part := range strings.Split(spec, ",") {
		msText, ampText, ok := strings.Cut(strings.TrimSpace(part), ":")
		ms, err1 := strconv.Atoi(msText)
		amp, err2 := strconv.Atoi(ampText)
		if !ok || err1 != nil || err2 != nil || ms < 0 || amp < 0 || amp > 32767 {
			return nil, fmt.Errorf("bad %s %q", FakeFFmpegPCMEnv, spec)
		}
		for i := 0; i < ms*8; i++ {
			v := int16(amp)
			if (i/4)%2 == 1 {
				v = -v
			}
			out = append(out, byte(v), byte(uint16(v)>>8))
		}
	}
	return out, nil
}

func exitFor(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(stderr, "fake ffmpeg: "+err.Error())
	return 1
}

// inputArg returns the path after -i, which must be "file:" plus an absolute path.
func inputArg(args []string) (string, bool) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-i" {
			if !strings.HasPrefix(args[i+1], "file:/") {
				return "", false
			}
			return strings.TrimPrefix(args[i+1], "file:"), true
		}
	}
	return "", false
}

// outputArg returns the last argument as a path; it must be "file:" plus an absolute path.
func outputArg(args []string) (string, bool) {
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "file:/") {
		return "", false
	}
	return strings.TrimPrefix(last, "file:"), true
}

func parseColor(s string) (color.RGBA, error) {
	if s == "" {
		s = defaultCoverColor
	}
	if len(s) != 7 || s[0] != '#' {
		return color.RGBA{}, fmt.Errorf("bad cover colour %q", s)
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("bad cover colour %q", s)
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}, nil
}

func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// fit scales img with nearest-neighbour to sit inside box by box, keeping the aspect ratio and
// never enlarging.
func fit(img image.Image, box int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= box && h <= box {
		return img
	}
	nw, nh := box, box
	if w >= h {
		nh = max(1, h*box/w)
	} else {
		nw = max(1, w*box/h)
	}
	out := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		for x := 0; x < nw; x++ {
			out.Set(x, y, img.At(b.Min.X+x*w/nw, b.Min.Y+y*h/nh))
		}
	}
	return out
}
