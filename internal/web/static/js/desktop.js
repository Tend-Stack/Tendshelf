// The desktop app window (decision 0020). The server loads this file only when the cookie ts_shell
// says "desktop". It does four small things, all through the one message channel the window gives a page:
// tell the app the page is ready, hand outside links to the system, quiet the browser context menu, and
// press the player's own buttons when the app forwards a media key.

// Only the app window has this channel. A browser that fakes the cookie lands here, finds nothing,
// and so loses nothing but offline use.
const channel = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external;

if (channel) {
  const post = (message) => channel.postMessage(message);

  // The app queues its scripts until the runtime says it is ready, then waits for the page's own word.
  post("wails:runtime:ready");
  post("tendshelf:ready");

  // Links to another origin open in the person's own browser or mail program. The app checks the
  // address again; this side only keeps the window from navigating away from Tendshelf.
  const openers = ["http:", "https:", "mailto:"];
  document.addEventListener(
    "click",
    (event) => {
      const link = event.target instanceof Element ? event.target.closest("a[href]") : null;
      if (!link) return;
      const url = new URL(link.href, location.href);
      if (url.origin === location.origin || !openers.includes(url.protocol)) return;
      event.preventDefault();
      post("tendshelf:open " + url.href);
    },
    true,
  );

  // The browser's context menu (Back, Reload, Inspect) means nothing in an app. Text fields and a
  // selection keep it, so copy and paste still work.
  document.addEventListener("contextmenu", (event) => {
    const selection = window.getSelection();
    const selected = selection && !selection.isCollapsed;
    const inField = event.target instanceof Element && event.target.closest("input, textarea, [contenteditable]");
    if (!selected && !inField) event.preventDefault();
  });

  // Media keys: the app sends ts:desktop with "toggle", "back" or "forward". Pressing the player's
  // own button keeps one code path for the audio. Nothing happens while the player is hidden.
  const buttons = { toggle: "[data-ts-toggle]", back: "[data-ts-back]", forward: "[data-ts-forward]" };
  window.addEventListener("ts:desktop", (event) => {
    const player = document.getElementById("player");
    const selector = buttons[event.detail];
    if (!player || player.hidden || !selector) return;
    const button = player.querySelector(selector);
    if (button) button.click();
  });
}
