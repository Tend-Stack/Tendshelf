// Method ported from Audiobookshelf (GPL-3.0): server/libs/nodeFfprobe/index.js, server/utils/prober.js; see NOTICES.md.

package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// How the encryption of a file was noticed (Probe.Protection).
const (
	ProtectionExtension  = "extension"
	ProtectionCodecTag   = "codec_tag"
	ProtectionStreamSide = "stream_side_data"
	ProtectionPacketSide = "packet_side_data"
)

// Probe is what Tendshelf keeps of one ffprobe run. It is stored as audio_files.probe_json, so it
// carries no file path and its field names are stable (V is the format version).
type Probe struct {
	V          int               `json:"v"`
	FormatName string            `json:"format_name"`
	Container  string            `json:"container"` // mp4, mp3, flac, ogg, wav, webm, ...
	DurationMs int64             `json:"duration_ms"`
	BitRate    int64             `json:"bit_rate"`
	Size       int64             `json:"size"`
	Tags       map[string]string `json:"tags,omitempty"` // keys lower-cased, values trimmed, empty skipped
	Audio      []AudioStream     `json:"audio"`
	Pictures   []Picture         `json:"pictures,omitempty"` // attached pictures (embedded covers)
	HasVideo   bool              `json:"has_video,omitempty"`
	Encrypted  bool              `json:"encrypted,omitempty"`
	Protection string            `json:"protection,omitempty"`
	Chapters   []ProbeChapter    `json:"chapters,omitempty"`
}

// AudioStream is one audio stream of a file.
type AudioStream struct {
	Index      int    `json:"index"`
	Codec      string `json:"codec"`
	CodecTag   string `json:"codec_tag,omitempty"`
	Profile    string `json:"profile,omitempty"`
	Channels   int    `json:"channels"`
	SampleRate int    `json:"sample_rate"`
	BitRate    int64  `json:"bit_rate"`
	DurationMs int64  `json:"duration_ms"`
}

// Picture is an attached picture stream (an embedded cover), not yet extracted.
type Picture struct {
	Index  int    `json:"index"`
	Codec  string `json:"codec"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ProbeChapter is one chapter mark on the file's own timeline.
type ProbeChapter struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Title   string `json:"title"`
}

// probeArgs are the exact ffprobe arguments. -read_intervals %+#8 adds the first 8 packets, which
// is where encrypted audio shows its "Encryption info" side data; the cover packet comes first.
func probeArgs(path string) []string {
	return []string{
		"-hide_banner", "-loglevel", "error", "-protocol_whitelist", "file,pipe",
		"-show_error", "-show_format", "-show_streams", "-show_chapters", "-show_packets",
		"-read_intervals", "%+#8", "-print_format", "json", "-i", "file:" + path,
	}
}

// Probe reads one audio file with ffprobe. The path must be absolute. The file is never modified.
//
// Failures are *Error values with a stable Code. A copy-protected file returns a Probe (so the
// caller can store why it was refused) together with code "drm" wrapping ErrProtected; callers that
// only check err cannot proceed. If ctx ends first, Probe returns context.Cause(ctx).
func (t Tools) Probe(ctx context.Context, path string) (Probe, error) {
	if !filepath.IsAbs(path) {
		return Probe{}, fmt.Errorf("media: probe path must be absolute: %q", path)
	}
	if Classify(path) == KindProtected {
		return Probe{V: 1, Encrypted: true, Protection: ProtectionExtension},
			newError(CodeDRM, "protected file extension", ErrProtected)
	}
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Probe{}, newError(CodeFileMissing, err.Error(), err)
	case err != nil:
		return Probe{}, newError(CodeFileUnreadable, err.Error(), err)
	case !fi.Mode().IsRegular():
		return Probe{}, newError(CodeNotAudio, "not a regular file", fmt.Errorf("%w: %s", ErrNotRegular, path))
	}
	if t.FFprobe == "" {
		return Probe{}, newError(CodeFFprobeMissing, "no ffprobe configured", ErrNotFound)
	}

	timeout := t.ProbeTimeout
	if timeout <= 0 {
		timeout = defaultProbeTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res := run(runCtx, t.FFprobe, probeArgs(path), t.Env)

	if res.err != nil {
		if ctx.Err() != nil {
			return Probe{}, context.Cause(ctx)
		}
		if runCtx.Err() != nil {
			return Probe{}, newError(CodeProbeTimeout, fmt.Sprintf("no answer within %s", timeout), runCtx.Err())
		}
	}
	if res.capped {
		return Probe{}, newError(CodeProbeOutputTooBig, fmt.Sprintf("ffprobe wrote more than %d bytes", stdoutCap), nil)
	}
	if res.err != nil {
		var exit *exec.ExitError
		if !errors.As(res.err, &exit) {
			if errors.Is(res.err, fs.ErrNotExist) || errors.Is(res.err, exec.ErrNotFound) {
				return Probe{}, newError(CodeFFprobeMissing, res.err.Error(), ErrNotFound)
			}
			return Probe{}, newError(CodeProbeFailed, res.err.Error(), res.err)
		}
		return Probe{}, probeFailure(res)
	}
	var pe probeErrorJSON
	if json.Unmarshal(res.stdout, &pe) == nil && pe.Error != nil { // an error object even with exit 0
		return Probe{}, probeFailure(res)
	}
	p, perr := parseProbe(res.stdout)
	if perr != nil {
		return Probe{}, newError(CodeProbeFailed, describe(res, perr), perr)
	}
	if len(p.Audio) == 0 {
		return Probe{}, newError(CodeNotAudio, "no audio stream", nil)
	}
	if p.Encrypted {
		return p, newError(CodeDRM, "encrypted audio ("+p.Protection+")", ErrProtected)
	}
	return p, nil
}

type probeErrorJSON struct {
	Error *struct {
		Code   int    `json:"code"`
		String string `json:"string"`
	} `json:"error"`
}

// probeFailure maps a failed ffprobe run (its -show_error JSON, else its exit) to an Error.
func probeFailure(res runResult) *Error {
	detail := describe(res, res.err)
	var pe probeErrorJSON
	if json.Unmarshal(res.stdout, &pe) == nil && pe.Error != nil {
		if pe.Error.String != "" && strings.TrimSpace(string(res.stderr)) == "" {
			detail = pe.Error.String
		}
		switch pe.Error.Code {
		case -2: // AVERROR(ENOENT)
			return newError(CodeFileMissing, detail, nil)
		case -13: // AVERROR(EACCES)
			return newError(CodeFileUnreadable, detail, nil)
		case -1094995529, // AVERROR_INVALIDDATA (ffprobe 7 and 8)
			-22: // AVERROR(EINVAL): ffprobe 6.1 (Ubuntu 24.04) answers this for a non-audio file
			// The file was already checked to exist and be regular, so either code means its
			// contents are not audio.
			return newError(CodeNotAudio, detail, nil)
		}
	}
	return newError(CodeProbeFailed, detail, res.err)
}

// ---- parsing ----

// flex reads a JSON value that ffprobe may print as a string ("6.0") or a number (6).
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var u string
		if err := json.Unmarshal(b, &u); err != nil {
			return err
		}
		*f = flex(u)
		return nil
	}
	*f = flex(s)
	return nil
}

func (f flex) int64() int64 {
	n, err := strconv.ParseInt(strings.TrimSpace(string(f)), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (f flex) int() int { return int(f.int64()) }

type sideData struct {
	Type string `json:"side_data_type"`
}

func hasEncryptionSide(sd []sideData) bool {
	for _, s := range sd {
		if strings.Contains(strings.ToLower(s.Type), "encryption") {
			return true
		}
	}
	return false
}

type ffStream struct {
	Index       int               `json:"index"`
	CodecName   string            `json:"codec_name"`
	CodecType   string            `json:"codec_type"`
	CodecTag    string            `json:"codec_tag_string"`
	Profile     string            `json:"profile"`
	Channels    flex              `json:"channels"`
	SampleRate  flex              `json:"sample_rate"`
	BitRate     flex              `json:"bit_rate"`
	Duration    flex              `json:"duration"`
	Width       flex              `json:"width"`
	Height      flex              `json:"height"`
	Disposition map[string]int    `json:"disposition"`
	Tags        map[string]string `json:"tags"`
	SideData    []sideData        `json:"side_data_list"`
}

type ffPacket struct {
	StreamIndex int        `json:"stream_index"`
	SideData    []sideData `json:"side_data_list"`
}

type ffChapter struct {
	StartTime flex              `json:"start_time"`
	EndTime   flex              `json:"end_time"`
	Tags      map[string]string `json:"tags"`
}

type ffOutput struct {
	Streams  []ffStream  `json:"streams"`
	Packets  []ffPacket  `json:"packets"`
	Chapters []ffChapter `json:"chapters"`
	Format   struct {
		FormatName string            `json:"format_name"`
		Duration   flex              `json:"duration"`
		Size       flex              `json:"size"`
		BitRate    flex              `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
}

var encryptedTags = map[string]bool{"enca": true, "encv": true, "drms": true, "drmi": true, "aavd": true}

// parseProbe turns ffprobe's JSON into a Probe. It does not decide success: the caller checks that
// there is an audio stream and whether the file is encrypted.
func parseProbe(raw []byte) (Probe, error) {
	var o ffOutput
	if err := json.Unmarshal(raw, &o); err != nil {
		return Probe{}, fmt.Errorf("ffprobe output is not JSON: %w", err)
	}
	p := Probe{V: 1, FormatName: o.Format.FormatName, Container: containerOf(o.Format.FormatName),
		Size: o.Format.Size.int64(), BitRate: o.Format.BitRate.int64(), Audio: []AudioStream{}}

	audio := map[int]bool{}
	var maxStream int64
	var firstAudioTags map[string]string
	tagEnc, streamEnc := false, false
	for _, s := range o.Streams {
		if hasEncryptionSide(s.SideData) {
			streamEnc = true
		}
		switch s.CodecType {
		case "audio":
			ms, _ := parseSecondsMs(string(s.Duration))
			a := AudioStream{Index: s.Index, Codec: s.CodecName, CodecTag: s.CodecTag, Profile: s.Profile,
				Channels: s.Channels.int(), SampleRate: s.SampleRate.int(), BitRate: s.BitRate.int64(), DurationMs: ms}
			p.Audio = append(p.Audio, a)
			audio[s.Index] = true
			maxStream = max(maxStream, ms)
			if firstAudioTags == nil {
				firstAudioTags = s.Tags
			}
			if encryptedTags[strings.ToLower(s.CodecTag)] {
				tagEnc = true
			}
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				p.Pictures = append(p.Pictures, Picture{Index: s.Index, Codec: s.CodecName,
					Width: s.Width.int(), Height: s.Height.int()})
			} else {
				p.HasVideo = true
			}
		}
	}

	p.Tags = mergeTags(o.Format.Tags, firstAudioTags)
	if ms, ok := parseSecondsMs(string(o.Format.Duration)); ok {
		p.DurationMs = ms
	} else {
		p.DurationMs = maxStream
	}

	for _, c := range o.Chapters {
		start, ok1 := parseSecondsMs(string(c.StartTime))
		end, ok2 := parseSecondsMs(string(c.EndTime))
		if !ok1 || !ok2 || end < start {
			continue
		}
		p.Chapters = append(p.Chapters, ProbeChapter{StartMs: start, EndMs: end, Title: chapterTitle(c.Tags)})
	}
	sort.SliceStable(p.Chapters, func(i, j int) bool { return p.Chapters[i].StartMs < p.Chapters[j].StartMs })

	pktEnc := false
	for _, k := range o.Packets {
		if audio[k.StreamIndex] && hasEncryptionSide(k.SideData) {
			pktEnc = true
			break
		}
	}
	switch {
	case tagEnc:
		p.Encrypted, p.Protection = true, ProtectionCodecTag
	case streamEnc:
		p.Encrypted, p.Protection = true, ProtectionStreamSide
	case pktEnc:
		p.Encrypted, p.Protection = true, ProtectionPacketSide
	}
	return p, nil
}

func containerOf(format string) string {
	switch {
	case strings.Contains(format, "mp4"):
		return "mp4"
	case strings.Contains(format, "matroska"), strings.Contains(format, "webm"):
		return "webm"
	}
	first, _, _ := strings.Cut(format, ",")
	return first
}

// mergeTags lower-cases keys, trims values and skips empty ones. Format tags win; the first audio
// stream's tags fill the keys the format lacks (Ogg/Opus keep their tags on the stream).
func mergeTags(format, stream map[string]string) map[string]string {
	out := map[string]string{}
	add := func(m map[string]string) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic when two keys differ only by case
		for _, k := range keys {
			v, key := strings.TrimSpace(m[k]), strings.ToLower(strings.TrimSpace(k))
			if v == "" || key == "" {
				continue
			}
			if _, have := out[key]; !have {
				out[key] = v
			}
		}
	}
	add(format)
	add(stream)
	if len(out) == 0 {
		return nil
	}
	return out
}

func chapterTitle(tags map[string]string) string {
	for k, v := range tags {
		if strings.EqualFold(k, "title") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// parseSecondsMs reads ffprobe's decimal seconds ("6.037007") as whole milliseconds, rounding half
// up at the fourth decimal. "N/A", "", negative and unparseable values report false.
func parseSecondsMs(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" || strings.HasPrefix(s, "-") {
		return 0, false
	}
	if strings.ContainsAny(s, "eE") {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(f) || f < 0 || f > 1e12 {
			return 0, false
		}
		return int64(math.Round(f * 1000)), true
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(whole) > 12 || (whole == "" && frac == "") || !digits(whole) || !digits(frac) {
		return 0, false
	}
	frac += "0000"
	var secs int64
	if whole != "" {
		secs, _ = strconv.ParseInt(whole, 10, 64)
	}
	ms := secs*1000 + int64(frac[0]-'0')*100 + int64(frac[1]-'0')*10 + int64(frac[2]-'0')
	if frac[3] >= '5' {
		ms++
	}
	return ms, true
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
