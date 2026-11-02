package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// streamEnq is a fake mediajobs.Enqueuer that records the file ids it was asked to convert.
type streamEnq struct {
	mu  sync.Mutex
	ids []string
}

func (q *streamEnq) Enqueue(_ context.Context, _ string, payload any, _ jobs.EnqueueOptions) (string, error) {
	p, ok := payload.(mediajobs.CompatPayload)
	if !ok {
		return "", fmt.Errorf("payload %T", payload)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ids = append(q.ids, p.AudioFileID)
	return "job_" + strconv.Itoa(len(q.ids)), nil
}

func (q *streamEnq) ActiveKeys(context.Context, string, string) (map[string]bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	m := map[string]bool{}
	for _, id := range q.ids {
		m[id] = true
	}
	return m, nil
}

func (q *streamEnq) count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.ids)
}

// streamEnv builds an app with the compat service wired to a fake queue, and a book of two real WAV files.
func streamEnv(t *testing.T) (a *testApp, owner, member, guest *http.Cookie, book library.Book, files []library.AudioFile, enq *streamEnq) {
	return streamEnvWith(t, true)
}

func streamEnvWith(t *testing.T, withCompat bool) (a *testApp, owner, member, guest *http.Cookie, book library.Book, files []library.AudioFile, enq *streamEnq) {
	t.Helper()
	enq = &streamEnq{}
	a = newTestApp(t, func(o *Options) {
		if withCompat {
			o.Compat = mediajobs.NewCompat(o.Library, media.Tools{}, mediajobs.CompatDir(o.DataDir), 1<<30, enq, nil)
		}
	})
	owner = a.owner(t)
	member = a.as(t, users.RoleMember)
	guest = a.as(t, users.RoleGuest)
	_, f := librarytest.NewLibrary(t, a.lib, "Stream library", t.TempDir())
	book = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Streamword Harbour", Files: 2})
	files = streamWriteFiles(t, a, book)
	return
}

// streamWriteFiles writes a 3 s WAV for each audio file of the book and makes the rows match.
func streamWriteFiles(t *testing.T, a *testApp, b library.Book) []library.AudioFile {
	t.Helper()
	files, err := a.lib.AudioFiles(context.Background(), b.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	for i, f := range files {
		if err := wav.Write(f.Path, 3000, 440+i*100); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(f.Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3000, size=?, mtime_ns=? WHERE id=?`,
			fi.Size(), fi.ModTime().UnixNano(), f.ID)
	}
	files, err = a.lib.AudioFiles(context.Background(), b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func streamExec(t *testing.T, a *testApp, q string, args ...any) {
	t.Helper()
	if _, err := a.st.Writer().ExecContext(context.Background(), q, args...); err != nil {
		t.Fatal(err)
	}
}

// streamOpens counts the files the handler opens; the test must not run in parallel.
func streamOpens(t *testing.T) *int {
	t.Helper()
	n := new(int)
	old := openAudio
	openAudio = func(name string) (*os.File, error) {
		*n++
		return old(name)
	}
	t.Cleanup(func() { openAudio = old })
	return n
}

func streamGet(a *testApp, method, path string, hdr map[string]string, ck *http.Cookie) (code int, h http.Header, body []byte) {
	rec := a.do(method, path, nil, func(r *http.Request) {
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
	}, ck)
	return rec.Code, rec.Header(), rec.Body.Bytes()
}

func TestStreamServesFile(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnv(t)
	f := files[0]
	want, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	code, h, body := streamGet(a, http.MethodGet, "/stream/"+f.ID, nil, owner)
	if code != http.StatusOK || !bytes.Equal(body, want) {
		t.Fatalf("GET: %d, %d bytes, want %d", code, len(body), len(want))
	}
	for k, v := range map[string]string{
		"Content-Type": "audio/wav", "Accept-Ranges": "bytes", "ETag": `"` + f.QuickHash + `"`, "Cache-Control": "private, no-cache",
	} {
		if h.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, h.Get(k), v)
		}
	}
	if h.Get("Last-Modified") == "" {
		t.Error("no Last-Modified")
	}
	code, h, body = streamGet(a, http.MethodHead, "/stream/"+f.ID, nil, owner)
	if code != http.StatusOK || len(body) != 0 || h.Get("Content-Length") != strconv.Itoa(len(want)) {
		t.Errorf("HEAD: %d, %d bytes, length %q", code, len(body), h.Get("Content-Length"))
	}
}

func TestStreamRanges(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnv(t)
	f := files[1]
	full, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	etag := `"` + f.QuickHash + `"`
	size := len(full)
	for _, tc := range []struct {
		name     string
		hdr      map[string]string
		code     int
		rng      string
		bodyWant []byte
	}{
		{"first 100", map[string]string{"Range": "bytes=0-99"}, 206, fmt.Sprintf("bytes 0-99/%d", size), full[:100]},
		{"open ended", map[string]string{"Range": "bytes=100-"}, 206, fmt.Sprintf("bytes 100-%d/%d", size-1, size), full[100:]},
		{"past the end", map[string]string{"Range": "bytes=999999-"}, 416, fmt.Sprintf("bytes */%d", size), nil},
		{"not modified", map[string]string{"If-None-Match": etag}, 304, "", nil},
		{"If-Range matches", map[string]string{"Range": "bytes=0-9", "If-Range": etag}, 206, fmt.Sprintf("bytes 0-9/%d", size), full[:10]},
		{"If-Range differs", map[string]string{"Range": "bytes=0-9", "If-Range": `"other"`}, 200, "", full},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, h, body := streamGet(a, http.MethodGet, "/stream/"+f.ID, tc.hdr, owner)
			if code != tc.code {
				t.Fatalf("status %d, want %d", code, tc.code)
			}
			if got := h.Get("Content-Range"); got != tc.rng {
				t.Errorf("Content-Range %q, want %q", got, tc.rng)
			}
			if tc.bodyWant != nil && !bytes.Equal(body, tc.bodyWant) {
				t.Errorf("body is %d bytes, want %d", len(body), len(tc.bodyWant))
			}
			if code == 304 && len(body) != 0 {
				t.Error("304 with a body")
			}
		})
	}
}

func TestStreamAccess(t *testing.T) {
	a, owner, member, guest, book, files, _ := streamEnv(t)
	opens := streamOpens(t)
	path := "/stream/" + files[0].ID
	for name, ck := range map[string]*http.Cookie{"member": member, "guest": guest} {
		code, _, body := streamGet(a, http.MethodGet, path, nil, ck)
		if code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, code)
		}
		for _, w := range []string{book.Title, files[0].ID, files[0].Path, ".wav", "audio/wav"} {
			if strings.Contains(string(body), w) {
				t.Errorf("%s: the answer leaks %q", name, w)
			}
		}
	}
	if *opens != 0 {
		t.Errorf("%d files opened for people without access", *opens)
	}
	code, h, _ := streamGet(a, http.MethodGet, path, nil, nil)
	if code != http.StatusSeeOther || h.Get("Location") != "/login?next="+url.QueryEscape(path) {
		t.Errorf("anonymous: %d to %q", code, h.Get("Location"))
	}
	if code, _, _ := streamGet(a, http.MethodGet, "/stream/af_unknown", nil, owner); code != http.StatusNotFound {
		t.Errorf("unknown id: %d", code)
	}
	if *opens != 0 {
		t.Errorf("%d files opened before the owner's first allowed request", *opens)
	}
	if code, _, _ := streamGet(a, http.MethodGet, path, nil, owner); code != http.StatusOK || *opens != 1 {
		t.Errorf("owner: %d with %d opens", code, *opens)
	}
}

func TestStreamRefusals(t *testing.T) {
	cases := []struct {
		name string
		set  func(t *testing.T, a *testApp, b library.Book, f library.AudioFile)
	}{
		{"encrypted", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			streamExec(t, a, `UPDATE audio_files SET encrypted=1 WHERE id=?`, f.ID)
		}},
		{"drm probe error", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			streamExec(t, a, `UPDATE audio_files SET probe_error='drm' WHERE id=?`, f.ID)
		}},
		{"not audio", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			streamExec(t, a, `UPDATE audio_files SET probe_error='not_audio' WHERE id=?`, f.ID)
		}},
		{"unprobed", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			streamExec(t, a, `UPDATE audio_files SET probe_json=NULL WHERE id=?`, f.ID)
		}},
		{"missing", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			streamExec(t, a, `UPDATE audio_files SET missing_since=1 WHERE id=?`, f.ID)
		}},
		{"swapped for a symlink", func(t *testing.T, _ *testApp, _ library.Book, f library.AudioFile) {
			// After the scan the file is replaced by a link to a secret outside the library.
			secret := filepath.Join(t.TempDir(), "secret.txt")
			if err := os.WriteFile(secret, []byte("not for listeners"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.Path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(secret, f.Path); err != nil {
				t.Fatal(err)
			}
		}},
		{"protected extension", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			b, err := os.ReadFile(f.Path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.Path+".aax", b, 0o644); err != nil {
				t.Fatal(err)
			}
			streamExec(t, a, `UPDATE audio_files SET path=? WHERE id=?`, f.Path+".aax", f.ID)
		}},
		{"refused book", func(t *testing.T, a *testApp, b library.Book, _ library.AudioFile) {
			streamExec(t, a, `UPDATE books SET refused_reason='drm' WHERE id=?`, b.ID)
		}},
		{"outside the book folder", func(t *testing.T, a *testApp, _ library.Book, f library.AudioFile) {
			other := filepath.Join(t.TempDir(), "other.wav")
			if err := wav.Write(other, 500, 300); err != nil {
				t.Fatal(err)
			}
			streamExec(t, a, `UPDATE audio_files SET path=? WHERE id=?`, other, f.ID)
		}},
	}
	for _, tc := range cases {
		for _, compat := range []string{"", "?compat=1"} {
			t.Run(tc.name+compat, func(t *testing.T) {
				a, owner, _, _, book, files, enq := streamEnv(t)
				tc.set(t, a, book, files[0])
				opens := streamOpens(t)
				code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID+compat, nil, owner)
				if code != http.StatusNotFound || *opens != 0 || enq.count() != 0 {
					t.Errorf("status %d, %d opens, %d jobs; want 404, 0, 0", code, *opens, enq.count())
				}
			})
		}
	}

	t.Run("deleted from disk", func(t *testing.T) {
		a, owner, _, _, _, files, _ := streamEnv(t)
		if err := os.Remove(files[0].Path); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID, nil, owner); code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})
	t.Run("directory at the path", func(t *testing.T) {
		a, owner, _, _, _, files, _ := streamEnv(t)
		if err := os.Remove(files[0].Path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(files[0].Path, 0o755); err != nil {
			t.Fatal(err)
		}
		if code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID, nil, owner); code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})
}

func TestStreamETagFollowsFile(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnv(t)
	f := files[0]
	if err := wav.Write(f.Path, 2000, 440); err != nil { // another length; the row is unchanged
		t.Fatal(err)
	}
	code, h, body := streamGet(a, http.MethodGet, "/stream/"+f.ID, nil, owner)
	if code != http.StatusOK || !strings.HasPrefix(h.Get("ETag"), `"m-`) {
		t.Errorf("status %d, ETag %q; want 200 and a tag starting \"m-", code, h.Get("ETag"))
	}
	if len(body) != 44+2000*16 {
		t.Errorf("served %d bytes, want the file as it is now", len(body))
	}
	if h.Get("ETag") == `"`+f.QuickHash+`"` {
		t.Error("the changed file kept the old tag")
	}
}

func TestStreamCompat(t *testing.T) {
	setCodec := func(t *testing.T, a *testApp, id, container, codec string) {
		streamExec(t, a, `UPDATE audio_files SET container=?, codec=? WHERE id=?`, container, codec, id)
	}

	t.Run("not needed", func(t *testing.T) {
		a, owner, _, _, _, files, enq := streamEnv(t)
		code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID+"?compat=1", nil, owner)
		if code != http.StatusNotFound || enq.count() != 0 {
			t.Errorf("status %d, %d jobs", code, enq.count())
		}
	})

	t.Run("queue then serve", func(t *testing.T) {
		a, owner, _, _, _, files, enq := streamEnv(t)
		f := files[0]
		setCodec(t, a, f.ID, "ogg", "opus")
		path := "/stream/" + f.ID + "?compat=1"
		for i := 0; i < 2; i++ {
			code, h, body := streamGet(a, http.MethodGet, path, nil, owner)
			if code != http.StatusServiceUnavailable || h.Get("Retry-After") != "3" || h.Get("Cache-Control") != "no-store" {
				t.Fatalf("call %d: %d, Retry-After %q, Cache-Control %q", i, code, h.Get("Retry-After"), h.Get("Cache-Control"))
			}
			if string(body) != compatWaitText {
				t.Errorf("body %q", body)
			}
			if enq.count() != 1 {
				t.Errorf("call %d: %d jobs, want 1", i, enq.count())
			}
		}
		// HEAD while not ready: 503, no body.
		code, h, body := streamGet(a, http.MethodHead, path, nil, owner)
		if code != http.StatusServiceUnavailable || len(body) != 0 || h.Get("Retry-After") != "3" {
			t.Errorf("HEAD: %d, %d bytes", code, len(body))
		}

		// The copy appears.
		copyBytes := wav.Bytes(1000, 440)
		dir := mediajobs.CompatDir(a.dir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f.QuickHash+"-r"+strconv.Itoa(media.CompatRecipe)+".m4a"), copyBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		code, h, body = streamGet(a, http.MethodGet, path, nil, owner)
		if code != http.StatusOK || !bytes.Equal(body, copyBytes) {
			t.Fatalf("copy: %d, %d bytes", code, len(body))
		}
		if h.Get("Content-Type") != "audio/mp4" || h.Get("ETag") != `"`+f.QuickHash+"-c"+strconv.Itoa(media.CompatRecipe)+`"` || h.Get("Cache-Control") != "private, no-cache" {
			t.Errorf("headers: type %q, ETag %q, cache %q", h.Get("Content-Type"), h.Get("ETag"), h.Get("Cache-Control"))
		}
		code, h, body = streamGet(a, http.MethodGet, path, map[string]string{"Range": "bytes=0-9"}, owner)
		if code != http.StatusPartialContent || !bytes.Equal(body, copyBytes[:10]) || h.Get("Content-Range") == "" {
			t.Errorf("range on the copy: %d, %d bytes", code, len(body))
		}
		if enq.count() != 1 {
			t.Errorf("%d jobs after the copy was served", enq.count())
		}
	})

	t.Run("encrypted", func(t *testing.T) {
		a, owner, _, _, _, files, enq := streamEnv(t)
		setCodec(t, a, files[0].ID, "ogg", "opus")
		streamExec(t, a, `UPDATE audio_files SET encrypted=1 WHERE id=?`, files[0].ID)
		code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID+"?compat=1", nil, owner)
		if code != http.StatusNotFound || enq.count() != 0 {
			t.Errorf("status %d, %d jobs", code, enq.count())
		}
	})

	t.Run("bad value", func(t *testing.T) {
		a, owner, _, _, _, files, enq := streamEnv(t)
		for _, v := range []string{"2", "", "true"} {
			code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID+"?compat="+v, nil, owner)
			if code != http.StatusBadRequest {
				t.Errorf("compat=%q: %d, want 400", v, code)
			}
		}
		if enq.count() != 0 {
			t.Errorf("%d jobs", enq.count())
		}
	})

	t.Run("member", func(t *testing.T) {
		a, _, member, _, _, files, enq := streamEnv(t)
		setCodec(t, a, files[0].ID, "ogg", "opus")
		code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID+"?compat=1", nil, member)
		if code != http.StatusNotFound || enq.count() != 0 {
			t.Errorf("status %d, %d jobs", code, enq.count())
		}
	})

	t.Run("without the service", func(t *testing.T) {
		a, owner, _, _, _, files, _ := streamEnvWith(t, false)
		setCodec(t, a, files[0].ID, "ogg", "opus")
		code, _, _ := streamGet(a, http.MethodGet, "/stream/"+files[0].ID+"?compat=1", nil, owner)
		if code != http.StatusNotFound {
			t.Errorf("status %d, want 404", code)
		}
	})
}

func TestStreamMethodGuard(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnv(t)
	tok := a.tokenFor(t, owner)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := a.do(m, "/stream/"+files[0].ID, url.Values{"_csrf": {tok}}, func(r *http.Request) { r.Header.Set(csrfHeader, tok) }, owner)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: %d, Allow %q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
}
