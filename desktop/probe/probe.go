//go:build tendshelf_probe

// Package probe drives the real window through its own message channel and records what happened.
// It exists only in a build with the tag tendshelf_probe (make desktop-probe); the shipped binary
// has none of it. Go runs a step by ExecJS, the page answers with "tendshelf:probe <id> <json>",
// and every answer lands in result.json under TENDSHELF_PROBE_OUT.
//
// Environment:
//
//	TENDSHELF_PROBE         "personal" or "connect"; anything else disables the probe
//	TENDSHELF_PROBE_OUT     folder for result.json (default .local/tests/desktop/<run>)
//	TENDSHELF_PROBE_LIBRARY folder holding the synthetic book the probe adds
//	TENDSHELF_PROBE_SERVER  connect mode: the test server's address
//	TENDSHELF_PROBE_CODE    connect mode: the test server's setup code
//	TENDSHELF_PROBE_PLAN    "" (first run), "relaunch" (saved mode: sign-in kept, then the reader)
//	                        or "switch" (saved mode, then Change mode and a new choice)
//	TENDSHELF_PROBE_STAY    set: the window stays open after "done"; the exit-run driver ends it
package probe

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Attach returns the handler of the window's raw messages. It reports true for messages that are
// the probe's own. exec runs a script in the window; origin is the session's origin; quit ends the
// application once the script is done.
func Attach(exec func(js string), origin func() string, change func(), quit func()) func(string) bool {
	mode := os.Getenv("TENDSHELF_PROBE")
	if mode != "personal" && mode != "connect" {
		return nil
	}
	out := os.Getenv("TENDSHELF_PROBE_OUT")
	if out == "" {
		out = filepath.Join(".local", "tests", "desktop", time.Now().UTC().Format("20060102-150405"))
	}
	p := &run{mode: mode, out: out, exec: exec, origin: origin, change: change, quit: quit, results: map[string]any{}}
	return p.handle
}

type run struct {
	mode   string
	out    string
	exec   func(string)
	origin func() string
	change func()
	quit   func()

	mu       sync.Mutex
	stage    int
	switched bool
	results  map[string]any
}

// handle sees every message. A page load ("tendshelf:ready") moves the script one stage on.
func (p *run) handle(message string) bool {
	switch {
	case message == "tendshelf:ready":
		p.pageLoaded()
		return false // the bridge still needs it
	case strings.HasPrefix(message, "tendshelf:probe "):
		rest := strings.TrimPrefix(message, "tendshelf:probe ")
		id, body, _ := strings.Cut(rest, " ")
		if id == "switchNow" {
			// The saved mode has loaded its page; open the picker as the menu's "Change mode or server…" does.
			p.mu.Lock()
			p.switched = true
			p.mu.Unlock()
			go p.change()
			return true
		}
		p.record(id, body)
		return true
	}
	return false
}

func (p *run) pageLoaded() {
	p.mu.Lock()
	p.stage++
	stage := p.stage
	p.mu.Unlock()
	if script := p.scriptFor(stage); script != "" {
		p.exec(script)
	}
}

// scriptFor picks the script for the stage-th page load of this launch (exit.go has the other plans).
func (p *run) scriptFor(stage int) string {
	switch os.Getenv("TENDSHELF_PROBE_PLAN") {
	case "relaunch":
		return relaunchScript()
	case "switch":
		p.mu.Lock()
		switched := p.switched
		p.mu.Unlock()
		return switchScript(p.mode, switched, os.Getenv("TENDSHELF_PROBE_SERVER"))
	}
	switch stage {
	case 1: // the picker
		return pickerScript(p.mode, os.Getenv("TENDSHELF_PROBE_SERVER"))
	case 2: // the library or server, first page
		return libraryScript(os.Getenv("TENDSHELF_PROBE_LIBRARY"), os.Getenv("TENDSHELF_PROBE_CODE"), p.origin())
	}
	return ""
}

func (p *run) record(id, body string) {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		v = body
	}
	p.mu.Lock()
	p.results[id] = v
	snapshot, _ := json.MarshalIndent(p.results, "", "  ")
	p.mu.Unlock()
	if err := os.MkdirAll(p.out, 0o755); err == nil {
		err = os.WriteFile(filepath.Join(p.out, "result.json"), snapshot, 0o644)
		if err != nil {
			slog.Error("probe: write result", "err", err)
		}
	}
	if id == "done" {
		fmt.Fprintln(os.Stderr, "probe: done, results in", p.out)
		if os.Getenv("TENDSHELF_PROBE_STAY") == "" {
			p.quit()
		}
	}
}

// post is the shared start of every script: a function that answers Go.
const post = `const reply = (id, value) => window.webkit.messageHandlers.external.postMessage("tendshelf:probe " + id + " " + JSON.stringify(value));`

// pickerScript chooses the mode on the first-run picker; the form is a plain POST.
func pickerScript(mode, server string) string {
	if mode == "personal" {
		return `(() => { ` + post + `
reply("picker", {href: location.href, shell: document.documentElement.dataset.shell || ""});
document.querySelector('form[action="/start/personal"] button').click();
})()`
	}
	return `(() => { ` + post + `
reply("picker", {href: location.href, shell: document.documentElement.dataset.shell || ""});
const form = document.querySelector('form[action="/start/connect"]');
form.querySelector('input[name="server"]').value = ` + js(server) + `;
form.querySelector('button').click();
})()`
}

func js(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// libraryScript runs inside the library's (or server's) first page: it finishes the first-run
// setup with fetch, adds the synthetic book, then checks the page, the cookie and muted playback.
func libraryScript(library, code, origin string) string {
	hold := "false"
	if os.Getenv("TENDSHELF_PROBE_UNMUTED") != "" { // D5: real output, left playing so MPRIS can be read
		hold = "true"
	}
	return `(async () => { ` + post + `
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const once = (el, name, ms) => new Promise((ok, no) => { const t = setTimeout(() => no(new Error("timeout " + name)), ms); el.addEventListener(name, () => { clearTimeout(t); ok(); }, {once: true}); });
const doc = (html) => new DOMParser().parseFromString(html, "text/html");
// Submit the form whose action is given, adding fields to its hidden ones (the CSRF token).
async function submit(page, action, fields) {
  const form = page.querySelector('form[action="' + action + '"]');
  if (!form) throw new Error("no form " + action);
  const body = new URLSearchParams(new FormData(form));
  for (const [k, v] of Object.entries(fields)) body.set(k, v);
  const res = await fetch(action, {method: "POST", body, credentials: "same-origin"});
  return {status: res.status, url: res.url, page: doc(await res.text())};
}
const result = {origin: ` + js(origin) + `, href: location.href, shell: document.documentElement.dataset.shell || ""};
const startOrigin = location.origin;
try {
  result.workers = navigator.serviceWorker ? (await navigator.serviceWorker.getRegistrations()).length : "none";
  let page = document;
  if (location.pathname === "/setup") {
    if (!page.querySelector('input[name="code"]').value) page.querySelector('input[name="code"]').value = ` + js(code) + `;
    let r = await submit(page, "/setup", {code: page.querySelector('input[name="code"]').value, display_name: "Probe", username: "probe", password: "probe-password-1", email: ""});
    result.setup = r.status + " " + r.url;
    r = await submit(r.page, "/setup/server", {name: "Probe shelf"});
    result.server = r.status + " " + r.url;
    r = await submit(r.page, "/setup/folder", {folder: ` + js(library) + `});
    result.folder = r.status + " " + r.url;
  }
  // The book appears after the scan; find a stream link on its page.
  let stream = "";
  for (let i = 0; i < 90 && !stream; i++) {
    const home = doc(await (await fetch("/", {credentials: "same-origin"})).text());
    const book = home.querySelector('a[href^="/books/"]');
    if (book) {
      const bp = doc(await (await fetch(book.getAttribute("href"), {credentials: "same-origin"})).text());
      const s = bp.querySelector('a[href^="/stream/"]');
      if (s) stream = s.getAttribute("href").split("#")[0];
      result.bookHref = book.getAttribute("href");
      result.attachment = result.bookHref + "/bookmarks/export?format=md";
    }
    if (!stream) await wait(1000);
  }
  result.stream = stream;
  const me = await fetch("/", {credentials: "same-origin"});
  result.signedIn = me.url.indexOf("/login") < 0 && me.url.indexOf("/setup") < 0;
  if (stream) {
    const a = new Audio();
    a.muted = true;
    window.tsProbeAudio = a;
    a.preload = "auto";
    a.src = stream;
    await once(a, "loadedmetadata", 20000);
    result.duration = a.duration;
    await a.play();
    for (let i = 0; i < 40 && a.currentTime < 1; i++) await wait(250);
    result.playedTo = a.currentTime;
    a.currentTime = Math.min(120, a.duration - 5);
    await once(a, "seeked", 10000);
    for (let i = 0; i < 40 && a.currentTime < Math.min(120, a.duration - 5) + 1; i++) await wait(250);
    result.seekedTo = a.currentTime;
    if (` + hold + `) {
      // D5: the autoplay policy refuses unmuted play() without a click, so start muted, then unmute.
      // A real key press, sent by the isolated shell's remote-desktop API, is the gesture.
      window.addEventListener("keydown", () => { a.muted = false; if (a.paused) a.play(); }, {once: true});
      for (let i = 0; i < 60 && a.muted; i++) await wait(500);
      await wait(2000);
      result.unmuted = {paused: a.paused, muted: a.muted, t: a.currentTime};
    } else {
      a.pause();
    }
  }
  // D7: the content security policy the window received, for the driver to compare with the server's own.
  result.csp = {};
  for (const path of ["/healthz", "/login"]) {
    result.csp[path] = (await fetch(path, {credentials: "same-origin"})).headers.get("content-security-policy");
  }
  // D7: the Downloads link exists in the page markup but the desktop style sheet hides it.
  const mePage = doc(await (await fetch("/me", {credentials: "same-origin"})).text());
  result.downloadsLinkInMarkup = !!mePage.querySelector('a[href="/me/downloads"]');
  const probeLink = document.createElement("a");
  probeLink.href = "/me/downloads";
  document.body.append(probeLink);
  result.downloadsLinkDisplay = getComputedStyle(probeLink).display;
  probeLink.remove();
  // D7: a link to another site is handed to the app ("tendshelf:open"); the driver's xdg-open stand-in logs it.
  const outside = document.createElement("a");
  outside.href = "https://example.org/probe-link";
  outside.target = "_blank";
  outside.textContent = "outside";
  document.body.append(outside);
  outside.click();
  outside.remove();
  await wait(1500);
  result.stayedOnOrigin = location.origin === startOrigin;
  // D7: an attachment (the bookmarks export) is saved by the window; the driver looks in the download folder.
  if (result.attachment) {
    reply("beforeDownload", result);
    location.href = result.attachment;
    await wait(4000);
    result.afterDownloadHref = location.href;
  }
} catch (e) {
  result.error = String(e && e.message || e);
}
reply("library", result);
reply("done", true);
})()`
}
