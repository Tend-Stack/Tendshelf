package components

// IsAppShell reports whether shell is one of the two phone apps (decision 0048, D12): "android" or "ios".
// It lives here, not in package web, because the layout and the player (both in this package) need it, and
// package web imports components, not the other way round. Package web's own isAppShell returns this.
func IsAppShell(shell string) bool {
	return shell == "android" || shell == "ios"
}

// NativeScripts returns the native-*.js files the layout loads for shell, in load order. A script is listed
// for a shell only when that shell implements its bridge words (decision 0048, D4): the iPhone app plays
// audio, keeps the screen awake and keeps downloads (I3b), but has no read-aloud voices, so it gets three of
// the four. A browser, the desktop shell and any other value get none.
func NativeScripts(shell string) []string {
	switch shell {
	case "android":
		return []string{"native-audio.js", "native-downloads.js", "native-speech.js", "native-feel.js"}
	case "ios":
		return []string{"native-audio.js", "native-downloads.js", "native-feel.js"}
	}
	return nil
}
