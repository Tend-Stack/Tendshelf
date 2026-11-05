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

	maxOpenBytes = 2048
)

// Media commands Go may send to the page. The list is fixed: nothing from a page ever reaches the
// script that is run.
var commands = map[string]string{
	"toggle":  `window.dispatchEvent(new CustomEvent("ts:desktop",{detail:"toggle"}))`,
	"back":    `window.dispatchEvent(new CustomEvent("ts:desktop",{detail:"back"}))`,
	"forward": `window.dispatchEvent(new CustomEvent("ts:desktop",{detail:"forward"}))`,
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

	mu    sync.Mutex
	ready bool
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

func (b *Bridge) fromSession(currentURI string) bool {
	origin := b.origin()
	return origin != "" && strings.HasPrefix(currentURI, origin+"/")
}

// Reset forgets that a page is ready; the next page says so again when it loads.
func (b *Bridge) Reset() {
	b.mu.Lock()
	b.ready = false
	b.mu.Unlock()
}

// Send gives the page a media command. It reports whether the page was ready: before that the
// command is dropped, because nothing listens yet.
func (b *Bridge) Send(command string) bool {
	js, ok := commands[command]
	if !ok {
		return false
	}
	b.mu.Lock()
	ready := b.ready
	b.mu.Unlock()
	if !ready {
		return false
	}
	b.exec(js)
	return true
}
