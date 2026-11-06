package main

import (
	"log/slog"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"
)

// The window's notifications (decision 0023, point 6): at the end of a book and when the sleep timer
// stops playback, and only when the window is not in front.

const (
	noteEnd   = "Finished. Tendshelf has stopped."
	noteSleep = "The sleep timer stopped playback."
)

// noteFor decides whether a change of state needs a notification, and what it says. The page says why
// playback stopped (nowState.Stop); it is told once, so only a change of that word counts.
func noteFor(prev, cur nowState, windowActive bool) (title, body string, ok bool) {
	if windowActive || cur.Title == "" || cur.Stop == "" || cur.Stop == prev.Stop {
		return "", "", false
	}
	if cur.Stop == "sleep" {
		return plainWords(cur.Title), noteSleep, true
	}
	return plainWords(cur.Title), noteEnd, true
}

// inFront says whether the person is looking at the window: it has focus, is shown and is not
// minimised. A hidden window (Keep playing) or a minimised one is not in front, even where the
// desktop sends no "lost focus" for it.
func inFront(focused, visible, minimised bool) bool { return focused && visible && !minimised }

// nowWatcher follows what the page says is playing and fans it out: the tray, the notifications and
// the saved theme. Each is a function, so a test needs no desktop.
type nowWatcher struct {
	active func() bool           // the window is in front
	tray   func(nowState)        // redraw the tray
	notify func(title, b string) // show a notification
	ground func(theme string)    // remember the theme for the next start

	mu  sync.Mutex
	cur nowState
}

// Set takes the page's newest state.
func (w *nowWatcher) Set(s nowState) {
	w.mu.Lock()
	prev := w.cur
	w.cur = s
	w.mu.Unlock()

	if w.tray != nil {
		w.tray(s)
	}
	if title, body, ok := noteFor(prev, s, w.active != nil && w.active()); ok && w.notify != nil {
		w.notify(title, body)
	}
	if s.Theme != "" && s.Theme != prev.Theme && w.ground != nil {
		w.ground(s.Theme)
	}
}

// Playing reports whether a book is playing now.
func (w *nowWatcher) Playing() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cur.Playing
}

// sendNote shows a notification through Wails' notifications service. The service is bound to
// Wails' own runtime, which no http page can reach (TestDeskNotifyNotReachable); only Go calls it.
func sendNote(ns *notifications.NotificationService) func(title, body string) {
	return func(title, body string) {
		go func() {
			slog.Info("showing a notification") // no words from the page in the log
			err := ns.SendNotification(notifications.NotificationOptions{
				ID:    "tendshelf-stopped", // one at a time: a new one replaces the last
				Title: title,
				Body:  body,
			})
			if err != nil {
				slog.Warn("could not show a notification", "err", err)
			}
		}()
	}
}
