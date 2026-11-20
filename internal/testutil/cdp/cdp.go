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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// callTimeout bounds every protocol call.
const callTimeout = 30 * time.Second

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
	args = append(args, RasterArgs()...)
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

// Close closes the page now instead of at the end of the test, for tests that open many tabs. The
// end-of-test cleanup then closes a target that is already gone and does nothing.
func (t *Tab) Close() {
	_ = t.b.call("", "Target.closeTarget", map[string]any{"targetId": t.target}, nil)
}

// Call sends any DevTools command to this page (for example Emulation.setCPUThrottlingRate) and
// decodes the result into out, which may be nil.
func (t *Tab) Call(method string, params, out any) error {
	return t.b.call(t.session, method, params, out)
}

// Navigate loads url and waits until the document, its fonts and its stylesheets have loaded. When
// one of the server's own stylesheets failed to load, it logs that and loads the page once more:
// during a long browser run a stylesheet fetch now and then failed with no response, the page came
// out unstyled, and both the test's checks and its capture saw that page.
func (t *Tab) Navigate(url string) {
	t.t.Helper()
	t.load(url)
	var failed []string
	t.Eval(failedSheetsJS, &failed)
	if len(failed) == 0 {
		return
	}
	t.t.Logf("navigate %s: stylesheets failed to load, loading the page again: %v", url, failed)
	t.load(url)
	t.Eval(failedSheetsJS, &failed)
	if len(failed) > 0 {
		t.t.Errorf("navigate %s: stylesheets failed to load twice: %v", url, failed)
	}
}

// load navigates to url and waits until the document and its fonts have loaded.
func (t *Tab) load(url string) {
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

// sheetsJS defines sheets(), the page's own linked stylesheets that apply (a design board opened
// from a file links handoff files that are not in the tree, and is left as it is), and failed(l),
// true for one that finished loading without rules: Chromium gives a stylesheet whose fetch failed a
// sheet whose rules cannot be read, as if it came from another origin.
const sheetsJS = `
	const sheets = () => [...document.querySelectorAll('link[rel~="stylesheet"]')]
		.filter((l) => !l.disabled && (!l.media || matchMedia(l.media).matches))
		.filter((l) => /^https?:$/.test(new URL(l.href).protocol) && new URL(l.href).origin === location.origin);
	const failed = (l) => {
		if (!l.sheet) return false; // still loading
		try { return l.sheet.cssRules.length === 0; } catch (e) { return true; }
	};
`

// failedSheetsJS lists the addresses of the page's own stylesheets that failed to load.
const failedSheetsJS = `(() => {` + sheetsJS + `
	return sheets().filter(failed).map((l) => l.getAttribute('href'));
})()`

// WaitFor polls a JavaScript condition every 50 ms until it is true or d passes.
func (t *Tab) WaitFor(cond string, d time.Duration) {
	t.t.Helper()
	deadline := time.Now().Add(d)
	for {
		var ok bool
		err := t.TryEval(`Boolean(`+cond+`)`, &ok)
		if err == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			// Say what the page was doing: a slow page and one whose script never ran look the same otherwise.
			var page string
			pageErr := t.TryEval(`location.pathname + " " + document.readyState`, &page)
			t.t.Fatalf("timed out after %s waiting for %s (last error: %v; page: %q, %v)", d, cond, err, page, pageErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// settleJS makes a capture repeatable: it waits for fonts and every image, then jumps each running
// animation and transition to its end (an endless one is cancelled), and hides the text caret,
// which blinks. It lives here, not in the app's CSS, so users see no change.
const settleJS = `(async () => {
	await document.fonts.ready;
	// Every linked stylesheet loaded, with its rules. A page's own stylesheets are links inside #main,
	// so an htmx swap that puts them back leaves the page unstyled until they load again, and a fetch
	// that failed while the machine was busy leaves the page without them for good (Chromium then gives
	// the link a sheet whose rules cannot be read). Both showed up as captures of a page without its
	// styles. Wait up to 2 s, fetch a failed one once more (a fresh link with the same address, put
	// where the old one was), then wait up to 3 s more; settle logs the retry and fails the test on any
	// stylesheet still missing.
` + sheetsJS + `
	const unstyled = () => sheets().filter((l) => !l.sheet || failed(l)).map((l) => l.getAttribute('href'));
	const status = (l) => {
		const e = performance.getEntriesByName(l.href).pop();
		return e ? String(e.responseStatus) : 'no response';
	};
	for (let i = 0; i < 40 && unstyled().length > 0; i++) await new Promise((r) => setTimeout(r, 50));
	const retried = [];
	for (const l of sheets().filter(failed)) {
		retried.push(l.getAttribute('href') + ' (' + status(l) + ')');
		const again = document.createElement('link');
		for (const a of l.attributes) again.setAttribute(a.name, a.value);
		again.addEventListener('load', () => l.remove(), {once: true});
		l.after(again);
	}
	for (let i = 0; i < 60 && unstyled().length > 0; i++) await new Promise((r) => setTimeout(r, 50));
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
	// A Go test's t.TempDir() is <tmp>/<TestName><random number>/<NNN>, and library folders, file paths
	// and the folder browser show it; the random number becomes a fixed one (its length varied too, so a
	// long path wrapped in one run and not the next).
	const tempDir = /(\/Test[A-Za-z0-9_]*?)\d+(\/\d{3}(?:\/|\b))/g;
	const tempFixed = (m, name, rest) => name + '1000000000' + rest;
	const scrub = (s) => s.replace(link, fixed).replace(tempDir, tempFixed);
	const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
	for (let n = walker.nextNode(); n; n = walker.nextNode()) {
		const v = scrub(n.nodeValue);
		if (v !== n.nodeValue) n.nodeValue = v;
	}
	for (const i of document.querySelectorAll('input, textarea')) {
		if (typeof i.value !== 'string') continue;
		const v = scrub(i.value);
		if (v !== i.value) i.value = v;
	}
	// CSS animations and transitions (the listening bars, spinners, a colour fading after a late
	// state change) and the caret blink are switched off for the capture only; unsettle removes the
	// sheet again, because the app waits for transitionend in normal use. Whatever still runs is
	// jumped to its end below.
	if (!document.__tsFreeze) {
		const sheet = new CSSStyleSheet();
		sheet.replaceSync('*,*::before,*::after{caret-color:transparent !important;animation:none !important;transition:none !important}' +
			'[data-ts-capture-still]{overflow:hidden !important}');
		document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
		document.__tsFreeze = sheet;
	}
	// A box that scrolls (a row of covers) is painted on a layer of its own, and in a capture of the
	// whole page Chromium sometimes drew the part below the window before that layer was rastered:
	// the row came out blank, or its covers' corners a few levels off. For the capture it stops being
	// scrollable (overflow hidden), so it is painted with the page; scrollbars are hidden by the launch
	// switches, so its size and layout stay the same, and it keeps its scroll offset.
	for (const el of document.body.querySelectorAll('*')) {
		if (el.hasAttribute('data-ts-capture-still')) continue;
		const st = getComputedStyle(el);
		if (!/(auto|scroll)/.test(st.overflowX + ' ' + st.overflowY)) continue;
		const left = el.scrollLeft, top = el.scrollTop;
		el.setAttribute('data-ts-capture-still', '');
		if (el.scrollLeft !== left || el.scrollTop !== top) el.scrollTo(left, top);
	}
	for (const a of document.getAnimations()) {
		try { a.finish(); } catch (e) { a.cancel(); }
	}
	// Scrolling and menus that position themselves in script: wait until the scroll offset and every
	// element's box have been the same for 4 frames in a row (at most 2 s).
	const layout = () => {
		let out = unstyled().length + '|' + scrollX + ',' + scrollY;
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
	// Paint everything afresh. Content painted while it sat on a layer of its own (the page rising in,
	// a panel sliding open) can keep that raster after the motion ends, and the rounded corners of
	// covers then came out a few levels different from run to run. Lifting the whole page onto a layer
	// of its own for two frames and back makes Chromium raster all of it again in its final state. It
	// leaves focus, hover and layout alone (hiding the page would blur the focused control).
	{
		const x = scrollX, y = scrollY;
		const lift = new CSSStyleSheet();
		lift.replaceSync('html{transform:translateZ(0) !important}');
		document.adoptedStyleSheets = [...document.adoptedStyleSheets, lift];
		await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
		document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== lift);
		if (scrollX !== x || scrollY !== y) scrollTo(x, y);
	}
	// let style, layout and raster of the final state finish before the pixels are read
	await new Promise((r) => setTimeout(r, 150));
	await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
	return {missing: unstyled(), retried};
})()`

// unsettleJS removes the sheet and marks settleJS added, so the page behaves normally after the capture.
const unsettleJS = `(() => {
	const sheet = document.__tsFreeze;
	if (sheet) document.adoptedStyleSheets = document.adoptedStyleSheets.filter((s) => s !== sheet);
	document.__tsFreeze = null;
	for (const el of document.querySelectorAll('[data-ts-capture-still]')) {
		const left = el.scrollLeft, top = el.scrollTop;
		el.removeAttribute('data-ts-capture-still');
		if (el.scrollLeft !== left || el.scrollTop !== top) el.scrollTo(left, top);
	}
	return true;
})()`

// settle runs settleJS; a page that is mid-navigation is captured as it is. A stylesheet that never
// loaded fails the test: the capture would show a broken page, and differ from the next run's.
func (t *Tab) settle() {
	t.t.Helper()
	var res struct {
		Missing []string `json:"missing"`
		Retried []string `json:"retried"`
	}
	if err := t.TryEval(settleJS, &res); err != nil {
		return
	}
	if len(res.Retried) > 0 {
		t.t.Logf("capture: fetched these stylesheets again after a failed load: %v", res.Retried)
	}
	if len(res.Missing) > 0 {
		t.t.Errorf("capture: stylesheets not loaded after 5 s: %v", res.Missing)
	}
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
	t.capture(path, map[string]any{"format": "png"})
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
	t.capture(path, map[string]any{"format": "png", "captureBeyondViewport": true,
		"clip": map[string]any{"x": 0, "y": 0, "width": size.W, "height": size.H, "scale": 1}})
}

// stableCaptures is how many captures capture takes at most while waiting for two in a row to match.
const stableCaptures = 4

// capture takes captures with params until two in a row are the same bytes, and writes that one to
// path. Chromium rasters the part of a full-page capture below the window, and a scrolling row on its
// own layer, partly before and partly during the capture, depending on timing; the first capture then
// differed from run to run at the rounded corners of covers. A page that is still changing after
// stableCaptures tries (something the test left running) is written as last seen, and logged.
func (t *Tab) capture(path string, params map[string]any) {
	t.t.Helper()
	var last []byte
	for i := 0; i < stableCaptures; i++ {
		var shot struct {
			Data string `json:"data"`
		}
		if err := t.b.call(t.session, "Page.captureScreenshot", params, &shot); err != nil {
			t.t.Fatal(err)
		}
		png, err := base64.StdEncoding.DecodeString(shot.Data)
		if err != nil {
			t.t.Fatal(err)
		}
		if bytes.Equal(png, last) {
			break
		}
		if i == stableCaptures-1 {
			t.t.Logf("capture %s: the page was still changing after %d captures", filepath.Base(path), stableCaptures)
		}
		last = png
	}
	if err := os.WriteFile(path, last, 0o644); err != nil {
		t.t.Fatal(err)
	}
}

// Quote returns s as a JavaScript string literal.
func Quote(s string) string {
	b, _ := json.Marshal(s)
	return strings.ReplaceAll(string(b), " ", ` `)
}

// RasterArgs make the same page paint the same pixels on every run, so a before/after capture diff
// (make visual-compare) shows only real changes. Each switch removes a choice Chromium otherwise
// makes from timing:
//   - --disable-lcd-text: subpixel (coloured) text is turned on and off per layer depending on whether
//     the layer was composited when it was last painted, so the same text came out greyscale in one
//     run and with colour fringes in the next.
//   - --disable-partial-raster: a repaint redraws only the changed rectangle onto the old tile, which
//     leaves a one-pixel seam where the rectangle ended, depending on how many repaints happened.
//   - --force-color-profile=srgb and --font-render-hinting=none: no dependence on the machine's
//     display profile or font settings.
//
// They change only how pixels are drawn, not layout or timing, so the motion and player tests are
// unaffected.
func RasterArgs() []string {
	return []string{"--disable-lcd-text", "--disable-partial-raster", "--force-color-profile=srgb", "--font-render-hinting=none"}
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
