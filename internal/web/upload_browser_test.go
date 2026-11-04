//go:build dev

package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/upload"
)

// uploadBrowser is a running app whose Audiobooks library takes uploads (64 KiB pieces, a real
// ffprobe, a running job runner), served to a browser that is always signed in as the owner. It
// counts every PUT and records its offset; delay slows each piece so a test can pause mid-file.
type uploadBrowser struct {
	a         *testApp
	URL       string
	libID     string
	folderDir string
	folder    library.Folder

	mu      sync.Mutex
	puts    []int64 // the offset of every PUT the server saw
	creates int
	delay   time.Duration
}

func newUploadBrowser(t *testing.T) *uploadBrowser {
	t.Helper()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	tools := media.Tools{FFprobe: "ffprobe", FFmpeg: "ffmpeg"}
	var runner *jobs.Runner
	a := newTestApp(t, func(o *Options) {
		r, err := jobs.New(o.Store, jobs.Options{PollEvery: 50 * time.Millisecond, ProgressEvery: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		o.Jobs, runner = r, r
		if err := scan.New(o.Library, tools, r, quiet).Register(r); err != nil {
			t.Fatal(err)
		}
		svc := upload.New(upload.Config{DataDir: o.DataDir, ChunkSize: 64 << 10, Margin: 0}, o.Store, o.Library, tools, r, quiet)
		if err := svc.Register(r); err != nil {
			t.Fatal(err)
		}
		o.Uploads = svc
	})
	if err := runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runner.Stop(context.Background()) })
	owner := a.owner(t)
	u := &uploadBrowser{a: a, folderDir: t.TempDir()}
	l, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", u.folderDir)
	u.libID, u.folder = l.ID, f
	if err := a.lib.SetUploadFolder(context.Background(), l.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		switch {
		case r.Method == http.MethodPut:
			off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
			u.mu.Lock()
			u.puts = append(u.puts, off)
			d := u.delay
			u.mu.Unlock()
			time.Sleep(d)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/uploads"):
			u.mu.Lock()
			u.creates++
			u.mu.Unlock()
		}
		a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	u.URL = srv.URL
	return u
}

func (u *uploadBrowser) putCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return len(u.puts)
}

func (u *uploadBrowser) setDelay(d time.Duration) {
	u.mu.Lock()
	u.delay = d
	u.mu.Unlock()
}

// uploads are the owner's uploads as the server lists them.
func (u *uploadBrowser) uploads(t *testing.T) []upload.View {
	t.Helper()
	ownerID := sweepUserID(t, u.a, u.a.owner(t))
	v, err := u.a.srv.uploads.List(context.Background(), ownerID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// chooseFiles puts files into a real file input, as the picker does: Chromium fires its change event.
func chooseFiles(t *testing.T, tab *cdp.Tab, selector string, files ...string) {
	t.Helper()
	var doc struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	if err := tab.Call("DOM.getDocument", map[string]any{"depth": 0}, &doc); err != nil {
		t.Fatal(err)
	}
	var found struct {
		NodeID int `json:"nodeId"`
	}
	if err := tab.Call("DOM.querySelector", map[string]any{"nodeId": doc.Root.NodeID, "selector": selector}, &found); err != nil || found.NodeID == 0 {
		t.Fatalf("no input %s: %v", selector, err)
	}
	if err := tab.Call("DOM.setFileInputFiles", map[string]any{"nodeId": found.NodeID, "files": files}, nil); err != nil {
		t.Fatal(err)
	}
}

func rowText(t *testing.T, tab *cdp.Tab) string {
	t.Helper()
	var s string
	tab.Eval(`[...document.querySelectorAll(".ts-upload__row")].map(r => r.textContent.replace(/\s+/g, " ").trim()).join(" | ")`, &s)
	return s
}

func writeTone(t *testing.T, name string, ms int) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := wav.Write(p, ms, 440); err != nil {
		t.Fatal(err)
	}
	return p
}

func openAddBooks(t *testing.T, u *uploadBrowser, b *cdp.Browser) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(u.URL + "/libraries/" + u.libID + "/add")
	tab.WaitFor(`document.querySelector("[data-ts-pick=files]") !== null`, 10*time.Second)
	// The loader imports the module on load; its change handler is there once a row can be made.
	tab.WaitFor(`document.querySelector("[data-ts-module]") !== null`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	return tab
}

// placedAudio lists the audio files below the library folder.
func placedAudio(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() && strings.HasSuffix(p, ".wav") {
			out = append(out, p)
		}
		return nil
	})
	return out
}

// TestUploadBrowserAddsABook: a generated tone goes through the real file input and becomes a book
// under "Unknown author"; the row ends "Added to Audiobooks" with Open book; the book page offers
// "Remove this book", the confirmation lists the file, and removing deletes it. A copy-protected
// name is refused before anything is sent.
func TestUploadBrowserAddsABook(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	tab := openAddBooks(t, u, b)

	// Copy-protected: refused at once; the server sees no upload and no PUT.
	drm := filepath.Join(t.TempDir(), "The Glass Orchard.aax")
	if err := os.WriteFile(drm, []byte("not really audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	chooseFiles(t, tab, "[data-ts-pick=files]", drm)
	tab.WaitFor(`document.querySelector(".ts-upload__row--refused") !== null`, 10*time.Second)
	if text := rowText(t, tab); !strings.Contains(text, "Not added: The Glass Orchard") || !strings.Contains(text, "The Glass Orchard.aax is copy-protected, so Tendshelf will not open it. Nothing was kept.") {
		t.Errorf("refused row = %q", text)
	}
	if u.putCount() != 0 || u.creates != 0 || len(u.uploads(t)) != 0 {
		t.Errorf("a copy-protected name reached the server: %d PUTs, %d creates", u.putCount(), u.creates)
	}

	tone := writeTone(t, "Quiet Harbour.wav", 3000)
	chooseFiles(t, tab, "[data-ts-pick=files]", tone)
	tab.WaitFor(`document.querySelector(".ts-upload__row--added") !== null`, 40*time.Second)
	text := rowText(t, tab)
	if !strings.Contains(text, "Added to Audiobooks") || !strings.Contains(text, "Unknown author/") || !strings.Contains(text, "Open book") {
		t.Errorf("added row = %q", text)
	}
	files := placedAudio(t, u.folderDir)
	if len(files) != 1 || !strings.Contains(files[0], "Unknown author") {
		t.Fatalf("placed files = %v", files)
	}
	want, _ := os.ReadFile(tone)
	got, _ := os.ReadFile(files[0])
	if string(want) != string(got) {
		t.Error("the placed file differs from the one picked")
	}

	// Open the book, find "Remove this book" on its Details tab, confirm and remove.
	var href string
	tab.Eval(`document.querySelector(".ts-upload__row--added a.ts-btn").getAttribute("href")`, &href)
	tab.Navigate(u.URL + href + "/details")
	tab.WaitFor(`document.querySelector(".ts-book__remove") !== null`, 10*time.Second)
	tab.Navigate(u.URL + href + "/remove")
	var list string
	tab.Eval(`document.querySelector(".ts-remove__files").textContent`, &list)
	if !strings.Contains(list, ".wav") {
		t.Errorf("the confirmation does not list the file: %q", list)
	}
	tab.Eval(`document.querySelector(".ts-remove button[type=submit]").click()`, nil)
	deadline := time.Now().Add(15 * time.Second)
	for len(placedAudio(t, u.folderDir)) != 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if left := placedAudio(t, u.folderDir); len(left) != 0 {
		t.Errorf("Remove left %v", left)
	}
}

// TestUploadBrowserPauseResume: Pause stops the requests mid-file, Resume finishes the same upload.
func TestUploadBrowserPauseResume(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	u.setDelay(150 * time.Millisecond)
	tab := openAddBooks(t, u, b)
	tone := writeTone(t, "Long Walk.wav", 60_000) // about 940 KiB, 15 pieces
	chooseFiles(t, tab, "[data-ts-pick=files]", tone)
	tab.WaitFor(`document.querySelector(".ts-upload__row--sending") !== null && document.querySelector("[data-ts-bar]").value > 0`, 20*time.Second)
	tab.Eval(`document.querySelector("[data-ts-act=pause]").click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-upload__row--paused") !== null`, 5*time.Second)
	time.Sleep(500 * time.Millisecond) // an aborted piece may still be on its way
	paused := u.putCount()
	time.Sleep(900 * time.Millisecond)
	if n := u.putCount(); n != paused {
		t.Errorf("requests continued while paused: %d then %d", paused, n)
	}
	if text := rowText(t, tab); !strings.Contains(text, "Paused.") || !strings.Contains(text, "sent.") || !strings.Contains(text, "Resume") {
		t.Errorf("paused row = %q", text)
	}
	before := u.uploads(t)
	if len(before) != 1 || before[0].Files[0].Offset == 0 || before[0].Files[0].Offset >= before[0].Files[0].Size {
		t.Fatalf("after pause the server has %+v", before)
	}
	u.setDelay(0)
	tab.Eval(`document.querySelector("[data-ts-act=resume]").click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-upload__row--added") !== null`, 40*time.Second)
	after := u.uploads(t)
	if len(after) != 1 || after[0].ID != before[0].ID || u.creates != 1 {
		t.Errorf("resume made a new upload: %d uploads, %d creates", len(after), u.creates)
	}
	if files := placedAudio(t, u.folderDir); len(files) != 1 {
		t.Errorf("placed %v", files)
	}
}

// TestUploadBrowserReloadChooseAgain: a reload mid-file leaves a stopped row; "Choose files again"
// carries on from the server's offset, not from zero.
func TestUploadBrowserReloadChooseAgain(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	u.setDelay(150 * time.Millisecond)
	tab := openAddBooks(t, u, b)
	tone := writeTone(t, "Long Walk.wav", 60_000)
	chooseFiles(t, tab, "[data-ts-pick=files]", tone)
	tab.WaitFor(`document.querySelector("[data-ts-bar]") && document.querySelector("[data-ts-bar]").value > 15`, 20*time.Second)
	tab.Navigate(u.URL + "/libraries/" + u.libID + "/add")
	tab.WaitFor(`document.querySelector(".ts-upload__row--stopped") !== null`, 10*time.Second)
	time.Sleep(600 * time.Millisecond) // a piece in flight at the reload may still land
	text := rowText(t, tab)
	if !strings.Contains(text, "Stopped at") || !strings.Contains(text, "Choose files again") || !strings.Contains(text, "Cancel upload") {
		t.Fatalf("stopped row = %q", text)
	}
	v := u.uploads(t)
	if len(v) != 1 || v[0].State != upload.StateReceiving || v[0].Files[0].Offset == 0 {
		t.Fatalf("server state after reload: %+v", v)
	}
	offset := v[0].Files[0].Offset
	u.mu.Lock()
	u.puts = nil
	u.mu.Unlock()
	u.setDelay(0)
	chooseFiles(t, tab, "[data-ts-again]", tone)
	tab.WaitFor(`document.querySelector(".ts-upload__row--added") !== null`, 40*time.Second)
	u.mu.Lock()
	first := u.puts[0]
	creates := u.creates
	u.mu.Unlock()
	if first < offset {
		t.Errorf("the first piece after choosing again started at %d, the server was at %d", first, offset)
	}
	if creates != 1 {
		t.Errorf("%d uploads were created", creates)
	}
}

// TestUploadBrowserOfflineResumes: the connection drops mid-file, the row says so and retries, and
// when the connection is back the same upload finishes without starting over.
func TestUploadBrowserOfflineResumes(t *testing.T) {
	b := chromium(t)
	u := newUploadBrowser(t)
	u.setDelay(150 * time.Millisecond)
	tab := openAddBooks(t, u, b)
	tone := writeTone(t, "Long Walk.wav", 60_000)
	chooseFiles(t, tab, "[data-ts-pick=files]", tone)
	tab.WaitFor(`document.querySelector("[data-ts-bar]") && document.querySelector("[data-ts-bar]").value > 5`, 20*time.Second)
	net := func(offline bool) {
		if err := tab.Call("Network.emulateNetworkConditions", map[string]any{"offline": offline, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := tab.Call("Network.enable", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	net(true)
	tab.WaitFor(`document.body.textContent.includes("The connection dropped. Trying again in")`, 15*time.Second)
	net(false)
	u.setDelay(0)
	tab.WaitFor(`document.querySelector(".ts-upload__row--added") !== null`, 60*time.Second)
	if v := u.uploads(t); len(v) != 1 || u.creates != 1 {
		t.Errorf("%d uploads, %d creates", len(v), u.creates)
	}
}
