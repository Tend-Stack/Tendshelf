package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// origin is a fake station. It answers through a handler directly (no sockets), so the hls package
// is tested without ever creating an HTTP client.
type origin struct {
	mu       sync.Mutex
	newest   int64 // newest sequence number in the live window
	window   int   // segments in the window
	endList  bool
	key      bool
	format   [2]int // sampling index, channel configuration
	title    func(seq int64) string
	segFail  func(seq int64, call int) bool
	segCalls map[int64]int
	listCall int
	listFail func(call int) bool
	reqs     []string
	disc     func(seq int64) bool
}

func newOrigin() *origin {
	return &origin{newest: 10, window: 4, format: [2]int{4, 2}, segCalls: map[int64]int{}}
}

func (o *origin) playlist() string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:2\n")
	first := o.newest - int64(o.window) + 1
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", first)
	if o.key {
		b.WriteString("#EXT-X-KEY:METHOD=AES-128,URI=\"k\"\n")
	}
	for s := first; s <= o.newest; s++ {
		if o.disc != nil && o.disc(s) {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		t := ""
		if o.title != nil {
			t = o.title(s)
		}
		fmt.Fprintf(&b, "#EXTINF:2.0,%s\nseg%d.aac\n", t, s)
	}
	if o.endList {
		b.WriteString("#EXT-X-ENDLIST\n")
	}
	return b.String()
}

// segment is one ADTS frame whose payload bytes all equal the sequence number.
func (o *origin) segment(seq int64) []byte {
	h := adtsHeader(1, o.format[0], o.format[1], 50)
	return append(h[:], bytes.Repeat([]byte{byte(seq)}, 50)...)
}

func (o *origin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reqs = append(o.reqs, r.URL.Path)
	switch {
	case r.URL.Path == "/live/index.m3u8":
		o.listCall++
		if o.listFail != nil && o.listFail(o.listCall) {
			http.Error(w, "down", 503)
			return
		}
		io.WriteString(w, o.playlist())
	case strings.HasPrefix(r.URL.Path, "/live/seg"):
		var seq int64
		fmt.Sscanf(r.URL.Path, "/live/seg%d.aac", &seq)
		o.segCalls[seq]++
		if o.segFail != nil && o.segFail(seq, o.segCalls[seq]) {
			http.Error(w, "down", 503)
			return
		}
		w.Write(o.segment(seq))
	default:
		http.NotFound(w, r)
	}
}

// fetcher serves the origin without a network.
type fetcher struct{ h http.Handler }

func (f fetcher) get(ctx context.Context, raw string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, httptest.NewRequest("GET", raw, nil))
	if rec.Code != 200 {
		return nil, "", fmt.Errorf("status %d", rec.Code)
	}
	return rec.Body.Bytes(), raw, nil
}

func (f fetcher) Playlist(ctx context.Context, raw string) ([]byte, string, error) {
	return f.get(ctx, raw)
}

func (f fetcher) Segment(ctx context.Context, raw string, _ time.Duration) ([]byte, error) {
	b, _, err := f.get(ctx, raw)
	return b, err
}

const liveURL = "http://radio.example/live/index.m3u8"

// sleeps records waits and lets a hook move the origin's live window on.
type sleeps struct {
	d    []time.Duration
	hook func(n int)
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.d = append(s.d, d)
	if s.hook != nil {
		s.hook(len(s.d))
	}
	return nil
}

// seqs reads n ADTS frames and returns their sequence markers.
func readSeqs(t *testing.T, s *Session, n int) []int64 {
	t.Helper()
	var out []int64
	frame := make([]byte, 57)
	for len(out) < n {
		if _, err := io.ReadFull(readerOf{s}, frame); err != nil {
			t.Fatalf("after %v: %v", out, err)
		}
		out = append(out, int64(frame[7]))
	}
	return out
}

type readerOf struct{ s *Session }

func (r readerOf) Read(p []byte) (int, error) { return r.s.Read(p) }

func TestHLSSessionLiveWindow(t *testing.T) {
	o := newOrigin()
	sl := &sleeps{}
	// The window advances on every second wait, so the other waits find nothing new.
	sl.hook = func(n int) {
		if n%2 == 0 {
			o.mu.Lock()
			o.newest += 2
			o.mu.Unlock()
		}
	}
	var titles []string
	o.title = func(seq int64) string { return map[bool]string{true: "A", false: "B"}[seq < 13] }
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: sl.sleep, OnTitle: func(s string) { titles = append(titles, s) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if f := s.Format(); f != (Format{"aac", 44100, 2}) {
		t.Fatalf("format %+v", f)
	}
	got := readSeqs(t, s, 6)
	want := []int64{8, 9, 10, 11, 12, 13}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("seqs %v, want %v", got, want)
		}
	}
	// 8..10 were taken at the start (StartBack 3), then each idle round waits half a target.
	if len(sl.d) == 0 || sl.d[0] != 2*time.Second {
		t.Fatalf("waits %v", sl.d)
	}
	sawHalf := false
	for _, d := range sl.d {
		sawHalf = sawHalf || d == time.Second
	}
	if !sawHalf {
		t.Fatalf("no half wait in %v", sl.d)
	}
	if len(titles) != 2 || titles[0] != "A" || titles[1] != "B" {
		t.Fatalf("titles %v", titles)
	}
}

func TestHLSSessionSegmentFailureRecovers(t *testing.T) {
	o := newOrigin()
	o.segFail = func(seq int64, call int) bool { return seq == 9 && call <= 2 }
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: (&sleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := readSeqs(t, s, 3)
	if got[0] != 8 || got[1] != 9 || got[2] != 10 {
		t.Fatalf("seqs %v", got)
	}
}

func TestHLSSessionThreeFailuresEnd(t *testing.T) {
	o := newOrigin()
	o.segFail = func(seq int64, call int) bool { return seq == 9 }
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: (&sleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = io.ReadAll(readerOf{s})
	if err == nil || o.segCalls[9] != MaxFailures {
		t.Fatalf("err %v after %d tries", err, o.segCalls[9])
	}
}

func TestHLSSessionReloadFailures(t *testing.T) {
	o := newOrigin()
	o.listFail = func(call int) bool { return call >= 2 }
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: (&sleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = io.ReadAll(readerOf{s})
	if err == nil || errors.Is(err, io.EOF) {
		t.Fatalf("err %v", err)
	}
	if o.listCall != 1+MaxFailures {
		t.Fatalf("playlist loaded %d times", o.listCall)
	}
}

func TestHLSSessionEndList(t *testing.T) {
	o := newOrigin()
	o.endList = true
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: (&sleeps{}).sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := io.ReadAll(readerOf{s})
	if err != nil || len(b) != 3*57 {
		t.Fatalf("%d bytes, %v", len(b), err)
	}
}

func TestHLSSessionSequenceReset(t *testing.T) {
	o := newOrigin()
	sl := &sleeps{hook: func(n int) {
		o.mu.Lock()
		o.newest = 5 // the origin restarted its numbering
		o.mu.Unlock()
	}}
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := readSeqs(t, s, 6)
	if got[3] != 3 || got[4] != 4 || got[5] != 5 {
		t.Fatalf("seqs %v", got)
	}
}

func TestHLSSessionGapAndDiscontinuity(t *testing.T) {
	o := newOrigin()
	o.disc = func(seq int64) bool { return seq == 12 }
	sl := &sleeps{hook: func(n int) {
		o.mu.Lock()
		o.newest = 20 // far ahead: the segments in between are gone
		o.mu.Unlock()
	}}
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := readSeqs(t, s, 7)
	if got[2] != 10 || got[3] != 17 || got[6] != 20 {
		t.Fatalf("seqs %v", got)
	}
}

func TestHLSSessionFormatChangeEnds(t *testing.T) {
	o := newOrigin()
	sl := &sleeps{hook: func(n int) {
		o.mu.Lock()
		o.newest += 3
		o.format = [2]int{3, 2} // 48 kHz
		o.mu.Unlock()
	}}
	s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: sl.sleep})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := io.ReadAll(readerOf{s})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("err %v", err)
	}
	if len(b) != 3*57 {
		t.Fatalf("%d bytes before the change, want only the first format's", len(b))
	}
}

func TestHLSSessionRefusals(t *testing.T) {
	t.Run("encrypted at open", func(t *testing.T) {
		o := newOrigin()
		o.key = true
		if _, err := Open(context.Background(), fetcher{o}, liveURL, Options{}); !errors.Is(err, ErrLocked) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("key appears mid-session", func(t *testing.T) {
		o := newOrigin()
		sl := &sleeps{hook: func(int) { o.mu.Lock(); o.key = true; o.mu.Unlock() }}
		s, err := Open(context.Background(), fetcher{o}, liveURL, Options{Sleep: sl.sleep})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if _, err := io.ReadAll(readerOf{s}); !errors.Is(err, ErrLocked) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("video only", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=900000,RESOLUTION=640x360,CODECS=\"avc1.4d401e,mp4a.40.2\"\nv.m3u8\n")
		})
		if _, err := Open(context.Background(), fetcher{h}, "http://radio.example/master.m3u8", Options{}); !errors.Is(err, ErrVideo) {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("master leads to audio", func(t *testing.T) {
		o := newOrigin()
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/live/master.m3u8" {
				io.WriteString(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=128000,CODECS=\"mp4a.40.2\"\nindex.m3u8\n")
				return
			}
			o.ServeHTTP(w, r)
		})
		s, err := Open(context.Background(), fetcher{h}, "http://radio.example/live/master.m3u8", Options{})
		if err != nil {
			t.Fatal(err)
		}
		s.Close()
	})
	t.Run("not a playlist", func(t *testing.T) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "<html>") })
		if _, err := Open(context.Background(), fetcher{h}, liveURL, Options{}); !errors.Is(err, ErrNotPlaylist) {
			t.Fatalf("err %v", err)
		}
	})
}

func TestHLSSessionCancelStopsSleepingRead(t *testing.T) {
	before := runtime.NumGoroutine()
	o := newOrigin()
	ctx, cancel := context.WithCancel(context.Background())
	s, err := Open(ctx, fetcher{o}, liveURL, Options{}) // real timer, 2 s target
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(readerOf{s})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Read did not stop after cancel")
	}
	s.Close()
	time.Sleep(50 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("goroutines %d -> %d", before, after)
	}
}

func TestHLSSessionCloseStopsSleepingRead(t *testing.T) {
	s, err := Open(context.Background(), fetcher{newOrigin()}, liveURL, Options{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(readerOf{s})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Read did not stop after Close")
	}
}

func TestHLSSessionOversizeSegmentRefused(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".m3u8") {
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nbig.aac\n")
			return
		}
		w.Write(make([]byte, MaxSegment+1))
	})
	s, err := Open(context.Background(), fetcher{h}, liveURL, Options{Sleep: (&sleeps{}).sleep})
	if err == nil {
		s.Close()
		t.Fatal("oversize segment accepted")
	}
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
}

// A trun that claims more than 65,536 samples is refused before any sample is read.
func TestHLSSessionTrunSampleCap(t *testing.T) {
	for _, tc := range []struct {
		count uint32
		err   error
	}{{65537, ErrTooLarge}, {1 << 31, ErrTooLarge}} {
		trun := append(u32(0x000201), u32(tc.count)...)
		trun = append(trun, u32(0)...)
		moof := mkBox("moof", mkBox("mfhd", u32(0), u32(1)), mkBox("traf", mkBox("tfhd", u32(0x020000), u32(1)), mkBox("trun", trun)))
		frag := append(moof, mkBox("mdat", make([]byte, 64))...)
		_, _, _, err := Demux(frag, synthInit(1, 0x40, []byte{0x12, 0x10}, "mp4a"))
		if !errors.Is(err, tc.err) {
			t.Fatalf("count %d: err %v", tc.count, err)
		}
	}
}
