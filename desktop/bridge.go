package main

import (
	"log/slog"
	"net/url"
	"strings"
	"sync"
)

// The page talks to Go through one channel (decision 0020, point 6): webkit.messageHandlers.external.
// Go honours only these two messages, and only from the origin of the session.
const (
	msgReady = "tendshelf:ready"
	msgOpen  = "tendshelf:open "

	// msgWindow is followed by one verb from windowVerbs (decision 0023, point 10).
	msgWindow = "tendshelf:window "

	maxOpenBytes = 2048

	// pickerOrigin is the scheme Wails serves the first-run picker on (the full address is not fixed,
	// so only the scheme is compared). It is the only page that is not on the
	// session's origin and may talk to the window.
	pickerOrigin = "wails://"
)

// command builds the fixed script for one ts:desktop value.
func command(detail string) string {
	return `window.dispatchEvent(new CustomEvent("ts:desktop",{detail:"` + detail + `"}))`
}

// Commands Go may send to the page: media keys, the window's state, and whether a tray exists. The
// list is fixed: nothing from a page ever reaches the script that is run.
var commands = map[string]string{
	"toggle":    command("toggle"),
	"back":      command("back"),
	"forward":   command("forward"),
	"active":    command("active"),
	"inactive":  command("inactive"),
	"maximised": command("maximised"),
	"restored":  command("restored"),
	"tray":      command("tray"),
}

// windowControl is what the page's own title bar may ask of the window.
type windowControl interface {
	Minimise()
	ToggleMaximise()
	Close()
	Quit()
}

// windowVerbs maps the verbs of "tendshelf:window" to the window. Anything else is refused.
var windowVerbs = map[string]func(windowControl){
	"minimise": func(w windowControl) { w.Minimise() },
	"maximise": func(w windowControl) { w.ToggleMaximise() },
	"close":    func(w windowControl) { w.Close() },
	"quit":     func(w windowControl) { w.Quit() },
}

// allowOpen decides whether a page may hand a link to the system. currentURI is the page that sent
// it (Wails' OriginInfo.Origin; IsMainFrame read false for the main frame in the probe, so it is
// not used). The page must belong to the session's origin, and the link must be a short http,
// https or mailto address.
func allowOpen(allowedOrigin, currentURI, target string) bool {
	if allowedOrigin == "" || !strings.HasPrefix(currentURI, allowedOrigin+"/") {
		return false
	}
	if len(target) > maxOpenBytes {
		return false
	}
	u, err := url.Parse(target)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https":
		return u.Host != ""
	case "mailto":
		return true
	}
	return false
}

// Bridge turns page messages into actions and sends media commands back.
type Bridge struct {
	origin func() string      // the session's origin
	open   func(string) error // hands a link to the system
	exec   func(js string)    // runs a script in the window
	win    windowControl      // the page's own title bar buttons act on this
	onLoad func()             // a first page, the session's or the picker's, says it is ready

	mu       sync.Mutex
	ready    bool // a page of the session said ready
	pickerUp bool // the picker said ready: it hears the window's state, never the media keys
}

// Handle is the window's raw message handler.
func (b *Bridge) Handle(message, currentURI string) {
	switch {
	case message == msgReady:
		if b.fromSession(currentURI) {
			b.mu.Lock()
			b.ready = true
			b.mu.Unlock()
		}
		if fromPicker(currentURI) {
			b.mu.Lock()
			b.pickerUp = true
			b.mu.Unlock()
		}
		if b.fromSession(currentURI) || fromPicker(currentURI) {
			if b.onLoad != nil {
				b.onLoad()
			}
		} else {
			slog.Debug("a page said ready from an origin the window does not know", "origin", currentURI)
		}
	case strings.HasPrefix(message, msgWindow):
		b.handleWindow(strings.TrimPrefix(message, msgWindow), currentURI)
	case strings.HasPrefix(message, msgOpen):
		target := strings.TrimPrefix(message, msgOpen)
		if !allowOpen(b.origin(), currentURI, target) {
			slog.Warn("refused a link from the page", "bytes", len(target))
			return
		}
		if err := b.open(target); err != nil {
			slog.Warn("could not open a link", "err", err)
		}
	default:
		// Anything else, including unknown tendshelf: messages, is ignored.
	}
}

// fromPicker reports whether the page is the first-run picker that Wails serves.
func fromPicker(currentURI string) bool {
	return strings.HasPrefix(currentURI, pickerOrigin)
}

// handleWindow runs one verb of the page's title bar. Only the session's pages and the picker may
// ask, and only for a verb on the list.
func (b *Bridge) handleWindow(verb, currentURI string) {
	if !b.fromSession(currentURI) && !fromPicker(currentURI) {
		slog.Warn("refused a window command from the page", "bytes", len(verb))
		return
	}
	run, ok := windowVerbs[verb]
	if !ok || b.win == nil {
		slog.Warn("ignored an unknown window command", "bytes", len(verb))
		return
	}
	run(b.win)
}

func (b *Bridge) fromSession(currentURI string) bool {
	origin := b.origin()
	return origin != "" && strings.HasPrefix(currentURI, origin+"/")
}

// Reset forgets that a page is ready; the next page says so again when it loads.
func (b *Bridge) Reset() {
	b.mu.Lock()
	b.ready = false
	b.pickerUp = false
	b.mu.Unlock()
}

// stateCommands tell the page about the window; the picker hears them too. The rest are media keys.
var stateCommands = map[string]bool{"active": true, "inactive": true, "maximised": true, "restored": true, "tray": true}

// Send gives the page a command. It reports whether the page was ready: before that the
// command is dropped, because nothing listens yet. The picker is never sent a media key.
func (b *Bridge) Send(command string) bool {
	js, ok := commands[command]
	if !ok {
		return false
	}
	b.mu.Lock()
	ready := b.ready || (b.pickerUp && stateCommands[command])
	b.mu.Unlock()
	if !ready {
		return false
	}
	b.exec(js)
	return true
}
