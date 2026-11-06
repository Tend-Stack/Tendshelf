//go:build tendshelf_probe

package probe

// nowScript is the "now" plan (decision 0023, X4): from a page of the session it says what is playing
// the way desktop-shell.js does (the message is real; the book is made up), hides the window, says
// the book ended, and tries to reach Wails' own runtime from the http origin. The driver reads the
// tray over D-Bus between the steps and watches the notification service.
func nowScript() string {
	return `(async () => { ` + post + `
if (!location.protocol.startsWith("http") || window.__tsNowProbe) return;
window.__tsNowProbe = true;
const send = (m) => window.webkit.messageHandlers.external.postMessage(m);
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const state = (extra) => "tendshelf:state " + JSON.stringify({title: "Probe & <b>Book</b>", chapter: "Chapter 2", left: "3 h left",
  playing: true, theme: "sepia", canAdd: false, stop: "", ...extra});
reply("origin", {href: location.href, shell: document.documentElement.dataset.shell || ""});
await wait(1500);
send(state({}));
reply("playing", true);
await wait(7000);                       // the driver reads the tray now
send("tendshelf:probe hideNow");         // hidden, as Keep playing hides it: not in front
await wait(2500);
send(state({playing: false, stop: "end"}));
reply("ended", true);
await wait(4000);                       // the driver reads the notification now
let reach = "";
try {
  const res = await fetch("wails://localhost/wails/runtime", {method: "POST", body: "{}"});
  reach = "answered " + res.status;
} catch (e) {
  reach = "refused " + String(e).slice(0, 80);
}
reply("reach", reach);
reply("done", true);
})()`
}
