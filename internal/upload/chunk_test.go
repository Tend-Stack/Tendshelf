package upload

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

// failingReader yields n bytes of data, then an error, like a connection that drops.
type failingReader struct {
	data []byte
	n    int
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, r.err
	}
	k := copy(p, r.data[:r.n])
	r.n -= k
	r.data = r.data[k:]
	return k, nil
}

func oneFile(t *testing.T, e *env, size int) (View, []byte) {
	t.Helper()
	data := bytes.Repeat([]byte("0123456789abcdef"), size/16)
	v, err := e.svc.Create(bg, me, e.lib.ID, manifestOf("B", book{"a.mp3": data}))
	if err != nil {
		t.Fatal(err)
	}
	return v, data
}

func partSize(t *testing.T, e *env, v View) int64 {
	t.Helper()
	fi, err := os.Stat(e.svc.partPath(v.ID, v.Files[0].ID))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return fi.Size()
}

func offsetOf(t *testing.T, e *env, v View) int64 {
	t.Helper()
	g, err := e.svc.Get(bg, me, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	return g.Files[0].Offset
}

func TestChunksInOrderAndChecksum(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2500) // 1024 + 1024 + 428
	e.sendFrom(v, book{"a.mp3": data}, hexSum)
	if got := offsetOf(t, e, v); got != int64(len(data)) {
		t.Fatalf("offset = %d", got)
	}
	part, _ := os.ReadFile(e.svc.partPath(v.ID, v.Files[0].ID))
	if !bytes.Equal(part, data) {
		t.Error("staged bytes differ from what was sent")
	}
	if fi, _ := os.Stat(e.svc.partPath(v.ID, v.Files[0].ID)); fi.Mode().Perm() != 0o600 {
		t.Errorf("part mode %v", fi.Mode().Perm())
	}
	if u := e.upload(v.ID); u.BytesReceived != int64(len(data)) {
		t.Errorf("upload bytes_received = %d", u.BytesReceived)
	}
}

func TestChunkWrongOffsetAndResend(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	f := v.Files[0]
	_, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 1024, "", bytes.NewReader(data[1024:]))
	if ue := wantErr(t, err, 409, "wrong_offset"); ue.Offset != 0 {
		t.Errorf("offset = %d", ue.Offset)
	}
	if _, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024])); err != nil {
		t.Fatal(err)
	}
	// The same chunk again: already applied.
	_, err = e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024]))
	if ue := wantErr(t, err, 409, "wrong_offset"); ue.Offset != 1024 {
		t.Errorf("offset = %d, want 1024", ue.Offset)
	}
	if got := offsetOf(t, e, v); got != 1024 || partSize(t, e, v) != 1024 {
		t.Errorf("offset %d part %d", got, partSize(t, e, v))
	}
}

func TestChunkBadChecksumOversizedShort(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 1800) // 1792 bytes: a chunk of 1024 and one of 768
	f := v.Files[0]
	_, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, hexSum([]byte("other")), bytes.NewReader(data[:1024]))
	wantErr(t, err, 422, "checksum")
	_, err = e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1025]))
	wantErr(t, err, 413, "too_large")
	_, err = e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:500]))
	wantErr(t, err, 422, "short_chunk")
	_, err = e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(nil))
	wantErr(t, err, 422, "short_chunk")
	if got := offsetOf(t, e, v); got != 0 || partSize(t, e, v) != 0 {
		t.Errorf("a refused chunk moved the offset to %d (part %d bytes)", got, partSize(t, e, v))
	}
	// A last chunk of a file must be exactly the rest, no more.
	if _, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024])); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.WriteChunk(bg, me, v.ID, f.ID, 1024, "", bytes.NewReader(append(append([]byte(nil), data[1024:]...), 'x')))
	wantErr(t, err, 422, "short_chunk")
	if _, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 1024, hexSum(data[1024:]), bytes.NewReader(data[1024:])); err != nil {
		t.Fatal(err)
	}
}

func TestChunkResumeAfterInterrupt(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	f := v.Files[0]
	if _, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024])); err != nil {
		t.Fatal(err)
	}
	// The second chunk drops half-way.
	r := &failingReader{data: data[1024:], n: 400, err: errors.New("connection reset")}
	_, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 1024, "", r)
	wantErr(t, err, 400, "interrupted")
	if got := offsetOf(t, e, v); got != 1024 {
		t.Fatalf("offset after interrupt = %d", got)
	}
	if got := partSize(t, e, v); got != 1024 {
		t.Errorf("stale bytes kept: part is %d bytes", got)
	}
	// The client asks where the server is and carries on from there.
	g, _ := e.svc.Get(bg, me, v.ID)
	if g.Files[0].Offset != 1024 {
		t.Fatalf("Get offset = %d", g.Files[0].Offset)
	}
	e.sendFrom(g, book{"a.mp3": data}, nil)
	part, _ := os.ReadFile(e.svc.partPath(v.ID, f.ID))
	if !bytes.Equal(part, data) {
		t.Error("resumed file differs")
	}
}

// A part file left longer than the offset (a crash after the write, before the update) is cut back.
func TestChunkTruncatesStaleTail(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	f := v.Files[0]
	if err := os.WriteFile(e.svc.partPath(v.ID, f.ID), bytes.Repeat([]byte("Z"), 700), 0o600); err != nil {
		t.Fatal(err)
	}
	e.sendFrom(v, book{"a.mp3": data}, nil)
	part, _ := os.ReadFile(e.svc.partPath(v.ID, f.ID))
	if !bytes.Equal(part, data) {
		t.Error("stale bytes survived")
	}
}

func TestChunkDiskFull(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	f := v.Files[0]
	if _, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024])); err != nil {
		t.Fatal(err)
	}
	for _, errno := range []error{syscall.ENOSPC, syscall.EDQUOT} {
		e.svc.writeAt = func(*os.File, []byte, int64) error { return &os.PathError{Op: "write", Err: errno} }
		_, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 1024, "", bytes.NewReader(data[1024:]))
		wantErr(t, err, 507, "no_space")
		if got := offsetOf(t, e, v); got != 1024 || partSize(t, e, v) != 1024 {
			t.Errorf("%v: offset %d part %d", errno, got, partSize(t, e, v))
		}
	}
	// On the very first chunk nothing is left behind at all.
	v2, d2 := oneFile(t, e, 2048)
	_, err := e.svc.WriteChunk(bg, me, v2.ID, v2.Files[0].ID, 0, "", bytes.NewReader(d2[:1024]))
	wantErr(t, err, 507, "no_space")
	if _, err := os.Stat(e.svc.partPath(v2.ID, v2.Files[0].ID)); !os.IsNotExist(err) {
		t.Errorf("an empty part file was left: %v", err)
	}
}

func TestChunkStatesAndOwner(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	f := v.Files[0]
	_, err := e.svc.WriteChunk(bg, "us_other", v.ID, f.ID, 0, "", bytes.NewReader(data[:1024]))
	wantErr(t, err, 404, "not_found")
	_, err = e.svc.WriteChunk(bg, me, v.ID, "upf_nope", 0, "", bytes.NewReader(data[:1024]))
	wantErr(t, err, 404, "not_found")
	_, err = e.svc.WriteChunk(bg, me, "upl_nope", f.ID, 0, "", bytes.NewReader(data[:1024]))
	wantErr(t, err, 404, "not_found")
	// A file id taken from another upload does not reach this one's staging.
	v2, _ := oneFile(t, e, 2048)
	_, err = e.svc.WriteChunk(bg, me, v.ID, v2.Files[0].ID, 0, "", bytes.NewReader(data[:1024]))
	wantErr(t, err, 404, "not_found")

	if err := e.svc.Cancel(bg, "us_other", v.ID); err == nil {
		t.Error("another person cancelled my upload")
	}
	if err := e.svc.Cancel(bg, me, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
		t.Errorf("staging kept after cancel: %v", err)
	}
	_, err = e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024]))
	wantErr(t, err, 410, "expired")
	wantErr(t, e.svc.Cancel(bg, me, v.ID), 409, "not_cancellable")
}

func TestFinish(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	_, err := e.svc.Finish(bg, me, v.ID)
	wantErr(t, err, 409, "incomplete")
	e.sendFrom(v, book{"a.mp3": data}, nil)
	_, err = e.svc.Finish(bg, "us_other", v.ID)
	wantErr(t, err, 404, "not_found")
	job, err := e.svc.Finish(bg, me, v.ID)
	if err != nil || job == "" {
		t.Fatalf("Finish: %q %v", job, err)
	}
	if u := e.upload(v.ID); u.State != StateQueued || u.JobID != job {
		t.Errorf("upload = %+v", u)
	}
	if q := e.enq.of(KindPlace); len(q) != 1 || !strings.Contains(string(q[0].Payload), v.ID) {
		t.Errorf("jobs = %+v", q)
	}
	_, err = e.svc.Finish(bg, me, v.ID)
	wantErr(t, err, 409, "not_receiving")
	// A chunk after finish is refused.
	_, err = e.svc.WriteChunk(bg, me, v.ID, v.Files[0].ID, 2048, "", io.NopCloser(bytes.NewReader(nil)))
	wantErr(t, err, 409, "not_receiving")
}
