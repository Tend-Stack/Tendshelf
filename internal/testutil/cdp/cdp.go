// Package cdp is a small Chrome DevTools Protocol client for Tendshelf's own visual tests. It drives
// a Chromium-family browser over --remote-debugging-pipe (file descriptors 3 and 4, NUL-delimited
// JSON), so it needs no websocket library and no driver binary. Only tests import it.
package cdp

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// callTimeout bounds every protocol call.
const callTimeout = 15 * time.Second

// Browser is one running headless Chromium.
type Browser struct {
	t    testing.TB
	cmd  *exec.Cmd
	in   io.WriteCloser
	mu   sync.Mutex // guards next, pending and writes
	next int
	wait map[int]chan reply
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type message struct {
	ID        int             `json:"id"`
	Method    string          `json:"method,omitempty"`
	Params    any             `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Start launches the browser at chromePath with a throwaway profile and kills it when the test ends.
func Start(t testing.TB, chromePath string) *Browser {
	t.Helper()
	inR, inW, err := os.Pipe() // we write, chrome reads (fd 3)
	if err != nil {
		t.Fatal(err)
	}
	outR, outW, err := os.Pipe() // chrome writes (fd 4), we read
	if err != nil {
		t.Fatal(err)
	}
	// --mute-audio: the player tests really play their tones (media time still advances, so first
	// sound and file switches are measured the same), but nothing reaches the speakers.
	args := append([]string{"--headless", "--remote-debugging-pipe", "--no-first-run", "--disable-gpu",
		"--hide-scrollbars", "--mute-audio", "--user-data-dir=" + t.TempDir()}, SandboxArgs()...)
	cmd := exec.Command(chromePath, append(args, "about:blank")...)
	cmd.ExtraFiles = []*os.File{inR, outW}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", chromePath, err)
	}
	_ = inR.Close()
	_ = outW.Close()
	b := &Browser{t: t, cmd: cmd, in: inW, wait: map[int]chan reply{}}
	go b.readLoop(outR)
	t.Cleanup(func() {
		// Ask for a clean exit so the helper processes stop writing to the profile; kill after 5 s.
		_ = b.call("", "Browser.close", nil, nil)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = inW.Close()
		_ = outR.Close()
	})
	return b
}

func (b *Browser) readLoop(r io.Reader) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		raw, err := br.ReadBytes(0)
		if err != nil {
			b.mu.Lock()
			for id, ch := range b.wait {
				ch <- reply{Error: &struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}{Message: "browser closed"}}
				delete(b.wait, id)
			}
			b.mu.Unlock()
			return
		}
		var m message
		if json.Unmarshal(bytes.TrimRight(raw, "\x00"), &m) != nil || m.ID == 0 {
			continue // events are not used by the tests
		}
		b.mu.Lock()
		ch := b.wait[m.ID]
		delete(b.wait, m.ID)
		b.mu.Unlock()
		if ch != nil {
			ch <- reply{Result: m.Result, Error: m.Error}
		}
	}
}

func (b *Browser) call(session, method string, params any, out any) error {
	ch := make(chan reply, 1)
	b.mu.Lock()
	b.next++
	id := b.next
	b.wait[id] = ch
	payload, err := json.Marshal(message{ID: id, Method: method, Params: params, SessionID: session})
	if err == nil {
		_, err = b.in.Write(append(payload, 0))
	}
	b.mu.Unlock()
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(r.Result, out)
		}
		return nil
	case <-time.After(callTimeout):
		return fmt.Errorf("%s: no answer in %s", method, callTimeout)
	}
}

// Tab is one page with its own viewport and media emulation.
type Tab struct {
	b       *Browser
	t       testing.TB
	session string
	target  string
}

// NewTab opens a page of w by h CSS pixels. mobile turns on touch-style viewport handling;
// reducedMotion emulates prefers-reduced-motion: reduce.
func (b *Browser) NewTab(w, h int, mobile, reducedMotion bool) *Tab {
	b.t.Helper()
	var created struct {
		TargetID string `json:"targetId"`
	}
	if err := b.call("", "Target.createTarget", map[string]any{"url": "about:blank"}, &created); err != nil {
		b.t.Fatal(err)
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.call("", "Target.attachToTarget", map[string]any{"targetId": created.TargetID, "flatten": true}, &attached); err != nil {
		b.t.Fatal(err)
	}
	tab := &Tab{b: b, t: b.t, session: attached.SessionID, target: created.TargetID}
	motion := "no-preference"
	if reducedMotion {
		motion = "reduce"
	}
	must := func(method string, params any) {
		if err := b.call(tab.session, method, params, nil); err != nil {
			b.t.Fatal(err)
		}
	}
	must("Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": 1, "mobile": mobile})
	must("Emulation.setEmulatedMedia", map[string]any{"features": []map[string]string{{"name": "prefers-reduced-motion", "value": motion}}})
	b.t.Cleanup(func() { _ = b.call("", "Target.closeTarget", map[string]any{"targetId": tab.target}, nil) })
	return tab
}

// Eval runs a JavaScript expression in the page (awaiting a promise) and decodes its JSON value
// into out, which may be nil. DevTools evaluation is not subject to the page's CSP.
func (t *Tab) Eval(expr string, out any) {
	t.t.Helper()
	if err := t.TryEval(expr, out); err != nil {
		t.t.Fatal(err)
	}
}

// TryEval is Eval that returns the error instead of failing the test.
func (t *Tab) TryEval(expr string, out any) error {
	var res struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		Exception *struct {
			Text      string `json:"text"`
			Exception struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	params := map[string]any{"expression": expr, "returnByValue": true, "awaitPromise": true}
	if err := t.b.call(t.session, "Runtime.evaluate", params, &res); err != nil {
		return err
	}
	if res.Exception != nil {
		return fmt.Errorf("eval %.60q: %s %s", expr, res.Exception.Text, res.Exception.Exception.Description)
	}
	if out == nil || len(res.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(res.Result.Value, out)
}

// Call sends any DevTools command to this page (for example Emulation.setCPUThrottlingRate) and
// decodes the result into out, which may be nil.
func (t *Tab) Call(method string, params, out any) error {
	return t.b.call(t.session, method, params, out)
}

// Navigate loads url and waits until the document and its fonts have loaded.
func (t *Tab) Navigate(url string) {
	t.t.Helper()
	t.Eval(`window.__tsNav = 1`, nil)
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := t.b.call(t.session, "Page.navigate", map[string]any{"url": url}, &nav); err != nil {
		t.t.Fatal(err)
	}
	if nav.ErrorText != "" {
		t.t.Fatalf("navigate %s: %s", url, nav.ErrorText)
	}
	t.WaitFor(`window.__tsNav === undefined && document.readyState === 'complete' && document.fonts.status === 'loaded'`, callTimeout)
}

// WaitFor polls a JavaScript condition every 50 ms until it is true or d passes.
func (t *Tab) WaitFor(cond string, d time.Duration) {
	t.t.Helper()
	deadline := time.Now().Add(d)
	for {
		var ok bool
		if err := t.TryEval(`Boolean(`+cond+`)`, &ok); err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			t.t.Fatalf("timed out after %s waiting for %s", d, cond)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// settleJS makes a capture repeatable: it waits for fonts and every image, then jumps each running
// animation and transition to its end (an endless one is cancelled), and hides the text caret,
// which blinks. It lives here, not in the app's CSS, so users see no change.
const settleJS = `(async () => {
	await document.fonts.ready;
	// Every image loaded and decoded (a blurred cover behind a button changes a pixel or two until then),
	// but never more than 3 s: a lazy image far below the fold may not load at all.
	const timeout = new Promise((r) => setTimeout(r, 3000));
	await Promise.race([timeout, Promise.all([...document.images].map(async (i) => {
		if (!i.complete) await new Promise((r) => { i.onload = i.onerror = r; });
		try { await i.decode(); } catch (e) { /* broken image: captured as it is */ }
	}))]);
	// The player bar appears only after the module has fetched the last played book (/hx/player), so a
	// capture taken earlier misses it. Wait for that fetch, at most 5 s; public link pages never make it.
	if (document.querySelector('script[src*="js/player.js"]') && !document.body.dataset.tsLink) {
		const fetched = () => performance.getEntriesByType('resource').some((e) => new URL(e.name).pathname === '/hx/player');
		for (let i = 0; i < 100 && !fetched(); i++) await new Promise((r) => setTimeout(r, 50));
		// then up to 0.5 s for the bar to be adopted (a book to show) or to be known not to come
		for (let i = 0; i < 10 && !document.body.classList.contains('has-player'); i++) await new Promise((r) => setTimeout(r, 50));
		// The bar hides while the page's own Resume button is in view, decided by an observer that
		// reports a frame or more later. Wait until the bar's state and the page height have stayed the same for 250 ms.
		const bar = document.getElementById('player');
		const sig = () => (bar ? bar.className + '|' + bar.hidden + '|' + bar.inert : '') + '|' + document.documentElement.scrollHeight;
		for (let same = 0, last = sig(), i = 0; same < 5 && i < 60; i++) {
			await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 50)));
			const now = sig();
			same = now === last ? same + 1 : 0;
			last = now;
		}
	}
	// Text that holds a random port or token (a share or invite link, shown to be copied) is rewritten
	// to a fixed text of the same length; the page itself is untouched.
	const link = /(https?:\/\/127\.0\.0\.1:)\d+(\/(?:s|invite)\/)([A-Za-z0-9_-]+)/g;
	const fixed = (m, host, path, token) => host + '40000' + path + 'x'.repeat(token.length);
	const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
	for (let n = walker.nextNode(); n; n = walker.nextNode()) n.nodeValue = n.nodeValue.replace(link, fixed);
	for (const i of document.querySelectorAll('input, textarea')) if (typeof i.value === 'string') i.value = i.value.replace(link, fixed);
	// CSS animations and transitions (the listening bars, spinners, a colour fading after a late
	// state change) and the caret blink are switched off for the capture only; unsettle removes the
	// sheet again, because the app waits for transitionend in normal use. Whatever still runs is
	// jumped to its end below.
	if (!document.__tsFreeze) {
		const sheet = new CSSStyleSheet();
		sheet.replaceSync('*,*::before,*::after{caret-color:transparent !important;animation:none !important;transition:none !important}');
		document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
		document.__tsFreeze = sheet;
	}
	for (const a of document.getAnimations()) {
		try { a.finish(); } catch (e) { a.cancel(); }
	}
	// Scrolling and menus that position themselves in script: wait until the scroll offset and every
	// element's box have been the same for 4 frames in a row (at most 2 s).
	const layout = () => {
		let out = scrollX + ',' + scrollY;
		let n = 0;
		for (const el of document.body.querySelectorAll('*')) {
			if (++n > 3000) break;
			const r = el.getBoundingClientRect();
			out += '|' + r.left.toFixed(2) + ',' + r.top.toFixed(2) + ',' + r.width.toFixed(2) + ',' + r.height.toFixed(2);
		}
		return out;
	};
	for (let same = 0, last = layout(), i = 0; same < 4 && i < 120; i++) {
		await new Promise((r) => requestAnimationFrame(() => setTimeout(r, 16)));
		const now = layout();
		same = now === last ? same + 1 : 0;
		last = now;
	}
	// let style, layout and raster of the final state finish before the pixels are read
	await new Promise((r) => setTimeout(r, 150));
	await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
	return true;
})()`

// unsettleJS removes the sheet settleJS added, so the page behaves normally after the capture.
const unsettleJS = `(() => {
	const sheet = document.__tsFreeze;
	if (sheet) document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== sheet);
	document.__tsFreeze = null;
	return true;
})()`

// settle runs settleJS; a page that is mid-navigation is captured as it is.
func (t *Tab) settle() {
	var ok bool
	_ = t.TryEval(settleJS, &ok)
}

func (t *Tab) unsettle() {
	var ok bool
	_ = t.TryEval(unsettleJS, &ok)
}

// Screenshot writes a PNG of the viewport to path.
func (t *Tab) Screenshot(path string) {
	t.t.Helper()
	t.settle()
	defer t.unsettle()
	var shot struct {
		Data string `json:"data"`
	}
	if err := t.b.call(t.session, "Page.captureScreenshot", map[string]any{"format": "png"}, &shot); err != nil {
		t.t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		t.t.Fatal(err)
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.t.Fatal(err)
	}
}

// ScreenshotFull writes a PNG of the whole scrollable page to path.
func (t *Tab) ScreenshotFull(path string) {
	t.t.Helper()
	t.settle()
	defer t.unsettle()
	var size struct {
		W float64 `json:"w"`
		H float64 `json:"h"`
	}
	t.Eval(`({w: document.documentElement.scrollWidth, h: document.documentElement.scrollHeight})`, &size)
	var shot struct {
		Data string `json:"data"`
	}
	params := map[string]any{"format": "png", "captureBeyondViewport": true,
		"clip": map[string]any{"x": 0, "y": 0, "width": size.W, "height": size.H, "scale": 1}}
	if err := t.b.call(t.session, "Page.captureScreenshot", params, &shot); err != nil {
		t.t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		t.t.Fatal(err)
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		t.t.Fatal(err)
	}
}

// Quote returns s as a JavaScript string literal.
func Quote(s string) string {
	b, _ := json.Marshal(s)
	return strings.ReplaceAll(string(b), " ", ` `)
}

// SandboxArgs is "--no-sandbox" when the tests run as root (the nightly CI container), where
// Chromium refuses to start with its sandbox, and nothing otherwise: a developer's own browser
// keeps the sandbox.
func SandboxArgs() []string {
	if os.Geteuid() == 0 {
		return []string{"--no-sandbox"}
	}
	return nil
}
