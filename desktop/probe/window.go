//go:build tendshelf_probe

package probe

// windowScript is the "window" plan (decision 0023, X1): the picker page, a stand-in title bar and a
// text field, all reporting what real pointer and key events did. The real bar is the next package;
// this one only proves the window under it: drag, resize, double-click, close, copy and paste.
func windowScript() string {
	return `(() => { ` + post + `
if (window.__tsWindowProbe) return;
window.__tsWindowProbe = true;
const send = (m) => window.webkit.messageHandlers.external.postMessage(m);
const events = [];
addEventListener("ts:desktop", (e) => { events.push(e.detail); reply("events", events); });

const bar = document.createElement("div");
const set = (el, css) => Object.assign(el.style, css);
set(bar, {position: "fixed", top: "0", left: "0", right: "0", height: "44px", background: "#444", zIndex: "99999"});
const close = document.createElement("button");
close.textContent = "x";
set(close, {position: "absolute", top: "0", right: "0", width: "60px", height: "44px", background: "#a33", color: "#fff"});
bar.append(close);
document.body.append(bar);

let armed = false;
bar.addEventListener("mousedown", (e) => { e.preventDefault(); armed = e.target === bar && e.detail === 1 && (e.buttons & 1) === 1; });
addEventListener("mouseup", () => { armed = false; });
bar.addEventListener("dblclick", (e) => { if (e.target === bar) send("tendshelf:window maximise"); });
close.addEventListener("click", () => send("tendshelf:window close"));

let pending = null;
addEventListener("mousemove", (e) => {
  if (armed && (e.buttons & 1) === 1) { armed = false; send("wails:drag"); }
  const wasIdle = pending === null;
  pending = {x: e.clientX, y: e.clientY};
  // The last position always arrives: a trailing report 40 ms after the first move of a burst.
  if (wasIdle) setTimeout(() => { reply("pointer", pending); pending = null; }, 40);
});
addEventListener("resize", () => reply("size", {w: innerWidth, h: innerHeight}));

const field = document.querySelector('input[name="server"]') || document.querySelector("input");
if (field) {
  field.addEventListener("input", () => reply("field", field.value));
  field.focus();
}
reply("size", {w: innerWidth, h: innerHeight});
reply("ready", true);
})()`
}
