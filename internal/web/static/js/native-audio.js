// native-audio.js is loaded only inside the Tendshelf Android app's window (data-shell="android").
//
// It does nothing yet. A4 of the Android plan will let the page hand playback to the app's native
// player here: the app injects no script and holds the device token itself, so this file must never
// read, store or send a token (decision 0026). It exists now so the page already names the one script
// the app window may load, and so a browser never gets it.
export {};
