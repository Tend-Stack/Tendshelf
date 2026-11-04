// Package fakeworker is a stand-in for runtimes/align/worker.py that speaks tendshelf-emissions-v1,
// so Go tests run with no Python. The test binary runs itself again (the pattern of
// internal/media/mediatest): TestMain calls Main when TENDSHELF_FAKE_ALIGN_WORKER is set.
//
// Modes (the value of the variable):
//
//	ok           normal; every log-probability is -ln(32)
//	slow-ok      like ok, 150 ms per request
//	no-ready     serve never prints the ready line
//	noise        serve prints a junk line before the ready line
//	bad-nonce    ready line carries another nonce
//	bad-identity describe and ready carry a modelSha256 that matches no manifest
//	crash        exit 3 after reading a request
//	crash-once   like crash, but only while the file named by TENDSHELF_FAKE_ALIGN_MARK does not exist
//	hang         never answer a request
//	oversize     answer with a payload past the 4 MiB cap
//	bad-frames   answer with a wrong frame count
//	env          ok, with the sorted environment in identity.versions["env"]
package fakeworker

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Identity values the fake reports; tests build a manifest with them.
const (
	EnvSwitch  = "TENDSHELF_FAKE_ALIGN_WORKER"
	EnvMark    = "TENDSHELF_FAKE_ALIGN_MARK"
	Repository = "fake/model"
	Revision   = "0123456789abcdef0123456789abcdef01234567"
	VocabSize  = 32
	minusLn32  = 0xC2EE // -ln(32) as a half-precision float
)

// Main runs the fake in mode and returns the exit code.
func Main(mode string) int {
	var describe, serve bool
	var dir, lang string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--describe":
			describe = true
		case "--serve":
			serve = true
		case "--model-dir":
			i++
			dir = args[i]
		case "--language":
			i++
			lang = args[i]
		}
	}
	ident, err := identity(mode, dir, lang)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	out := bufio.NewWriter(os.Stdout)
	switch {
	case describe:
		writeJSON(out, ident)
		return 0
	case serve:
		return serveLoop(mode, ident, out)
	}
	return 2
}

func identity(mode, dir, lang string) (map[string]any, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(b)
		lines = append(lines, fmt.Sprintf("%s\t%d\t%s\n", e.Name(), len(b), hex.EncodeToString(sum[:])))
	}
	sort.Strings(lines) // names are distinct, so line order is name order
	sha := sha256.Sum256([]byte(strings.Join(lines, "")))
	modelSHA := hex.EncodeToString(sha[:])
	if mode == "bad-identity" {
		modelSHA = strings.Repeat("0", 64)
	}
	vocab := make([]string, VocabSize)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("t%d", i)
	}
	versions := map[string]string{"fake": "1"}
	if mode == "env" {
		env := os.Environ()
		sort.Strings(env)
		versions["env"] = strings.Join(env, "\n")
	}
	return map[string]any{
		"protocol": "tendshelf-emissions-v1", "model": Repository, "revision": Revision,
		"modelSha256": modelSHA, "language": lang, "delimiter": "|", "vocab": vocab,
		"blank": 0, "frameMs": 20, "sampleRate": 16000, "versions": versions,
	}, nil
}

func writeJSON(w *bufio.Writer, v any) {
	b, _ := json.Marshal(v)
	w.Write(b)
	w.WriteByte('\n')
	w.Flush()
}

func serveLoop(mode string, ident map[string]any, out *bufio.Writer) int {
	nonce := os.Getenv("TENDSHELF_ALIGN_NONCE")
	switch mode {
	case "no-ready":
		time.Sleep(time.Hour)
	case "noise":
		out.WriteString("warming up\n")
		out.Flush()
	case "bad-nonce":
		nonce = strings.Repeat("f", 64)
	}
	writeJSON(out, map[string]any{"status": "ready", "nonce": nonce, "identity": ident})
	if mode == "bad-nonce" {
		nonce = os.Getenv("TENDSHELF_ALIGN_NONCE")
	}
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return 0 // stdin closed: the server is gone
		}
		var h struct {
			ID, AudioSHA256 string
			Samples         int
		}
		var raw map[string]any
		if json.Unmarshal(line, &raw) != nil {
			return 2
		}
		h.ID, _ = raw["id"].(string)
		h.AudioSHA256, _ = raw["audioSha256"].(string)
		if f, ok := raw["samples"].(float64); ok {
			h.Samples = int(f)
		}
		if _, err := io.CopyN(io.Discard, in, int64(h.Samples)*2); err != nil {
			return 0
		}
		switch mode {
		case "crash":
			return 3
		case "crash-once":
			mark := os.Getenv(EnvMark)
			if _, err := os.Stat(mark); err != nil {
				_ = os.WriteFile(mark, []byte("x"), 0o600)
				return 3
			}
		case "hang":
			time.Sleep(time.Hour)
		case "slow-ok":
			time.Sleep(150 * time.Millisecond)
		}
		frames := (h.Samples-400)/320 + 1
		if mode == "bad-frames" {
			frames += 50
		}
		size := frames * VocabSize * 2
		if mode == "oversize" {
			size = 4<<20 + 2*VocabSize
			frames = size / (VocabSize * 2)
		}
		data := bytes.Repeat(binary.LittleEndian.AppendUint16(nil, minusLn32), size/2)
		writeJSON(out, map[string]any{
			"id": h.ID, "nonce": nonce, "audioSha256": h.AudioSHA256, "frames": frames,
			"vocabSize": VocabSize, "frameMs": 20, "bytes": len(data),
		})
		out.Write(data)
		out.Flush()
	}
}
