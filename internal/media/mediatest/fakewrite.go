package mediatest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// splitFake returns the description JSON and the pad bytes of a fake audio file.
func splitFake(data []byte) (desc, pad []byte, ok bool) {
	if !bytes.HasPrefix(data, []byte(fakeMagic)) {
		return nil, nil, false
	}
	rest := data[len(fakeMagic):]
	i := bytes.IndexByte(rest, '\n')
	if i < 0 {
		return rest, nil, true
	}
	return rest[:i], rest[i+1:], true
}

// fakeAudioHash prints SHA256= plus the digest of the pad bytes of a fake file (tags and chapters
// do not enter it) or of the whole file when it is not a fake.
func fakeAudioHash(in string, stdout, stderr io.Writer) int {
	data, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintf(stderr, "%s: No such file or directory\n", in)
		return 1
	}
	body := data
	if _, pad, ok := splitFake(data); ok {
		body = pad
	}
	sum := sha256.Sum256(body)
	_, err = fmt.Fprintf(stdout, "SHA256=%s\n", hex.EncodeToString(sum[:]))
	return exitFor(err, stderr)
}

// fakeWriteTags plays the tag write (see runFakeFFmpeg, f).
func fakeWriteTags(args []string, in string, isFake bool, stderr io.Writer) int {
	out, ok := outputArg(args)
	if !ok || !isFake {
		fmt.Fprintln(stderr, "fake ffmpeg: unexpected arguments")
		return 64
	}
	data, err := os.ReadFile(in)
	if err != nil {
		return exitFor(err, stderr)
	}
	desc, pad, _ := splitFake(data)
	var ff FakeFile
	if err := json.Unmarshal(desc, &ff); err != nil {
		fmt.Fprintln(stderr, "fake ffmpeg: bad description: "+err.Error())
		return 65
	}
	tags := map[string]string{}
	for k, v := range ff.Tags {
		tags[k] = v
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-metadata" {
			k, v, _ := strings.Cut(args[i+1], "=")
			tags[strings.ToLower(k)] = v
		}
	}
	ff.Tags = tags
	if hasPair(args, "-map_chapters", "1") {
		meta, ok := secondInput(args)
		if !ok {
			fmt.Fprintln(stderr, "fake ffmpeg: -map_chapters 1 without a second input")
			return 64
		}
		text, err := os.ReadFile(meta)
		if err != nil {
			return exitFor(err, stderr)
		}
		ff.Chapters = parseFFMetaChapters(string(text))
	}
	body, err := json.Marshal(ff)
	if err != nil {
		return exitFor(err, stderr)
	}
	var buf bytes.Buffer
	buf.WriteString(fakeMagic)
	buf.Write(body)
	buf.WriteByte('\n')
	buf.Write(pad)
	return exitFor(os.WriteFile(out, buf.Bytes(), 0o600), stderr)
}

// secondInput is the path of the second -i ("file:" plus an absolute path).
func secondInput(args []string) (string, bool) {
	n := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-i" {
			n++
			if n == 2 {
				if !strings.HasPrefix(args[i+1], "file:/") {
					return "", false
				}
				return strings.TrimPrefix(args[i+1], "file:"), true
			}
		}
	}
	return "", false
}

// parseFFMetaChapters reads the [CHAPTER] sections of an ffmetadata file, undoing its escapes.
func parseFFMetaChapters(text string) []FakeChapter {
	var lines []string
	var cur strings.Builder
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		switch {
		case rs[i] == '\\' && i+1 < len(rs):
			cur.WriteRune('\\')
			cur.WriteRune(rs[i+1])
			i++
		case rs[i] == '\n':
			lines = append(lines, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(rs[i])
		}
	}
	var out []FakeChapter
	var c *FakeChapter
	for _, line := range lines {
		if line == "[CHAPTER]" {
			out = append(out, FakeChapter{})
			c = &out[len(out)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || c == nil {
			continue
		}
		switch strings.ToLower(k) {
		case "start":
			c.StartMs, _ = strconv.ParseInt(v, 10, 64)
		case "end":
			c.EndMs, _ = strconv.ParseInt(v, 10, 64)
		case "title":
			c.Title = unescapeFFMeta(v)
		}
	}
	return out
}

func unescapeFFMeta(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] == '\\' && i+1 < len(rs) {
			i++
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}
