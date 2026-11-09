// Reader comfort (G5): warm light, keeping the screen on, and a bar that hides itself. It is loaded only once a
// person has turned one of the three on, and it reacts when they change them (ts-reader-look).

const IDLE_MS = 10 * 60 * 1000; // the screen lock is let go after this long without input
const HIDE_MS = 3000; // the bar hides this long after the last input
const WARM_CHECK_MS = 5 * 60 * 1000; // "in the evening" is looked at again this often

// warmAt says whether the warm tint is on for a setting ("off", "evening" or "always") at a local hour (0 to 23).
export function warmAt(setting, hour) {
  if (setting === "always") return true;
  return setting === "evening" && (hour >= 20 || hour < 6);
}

let running = null; // the AbortController of the mount in force; a second mount replaces it

export function mount() {
  running?.abort();
  const root = document.getElementById("reader");
  const page = root?.closest(".ts-readerpage");
  if (!page) return;
  const ctl = new AbortController();
  running = ctl;
  const { signal } = ctl;
  const on = (target, type, fn) => target.addEventListener(type, fn, { signal });
  const wants = (name) => root.dataset[name] === "true";

  // ---- Warm light ----
  const warm = () => {
    if (warmAt(root.dataset.warm, new Date().getHours())) page.dataset.warm = "on";
    else delete page.dataset.warm;
  };

  // ---- Keeping the screen on ----
  let lock = null;
  let idle = false;
  let idleTimer = 0;
  const release = (l) => Promise.resolve(l?.release()).catch(() => {});
  // The Android app's stand-in (native-feel.js, decision 0033) wins: its WebView grants no navigator.wakeLock.
  const take = async () => {
    const api = window.tsNativeWake || navigator.wakeLock;
    if (!api || document.visibilityState !== "visible" || lock || idle || !wants("keepAwake")) return;
    try {
      const l = await api.request("screen");
      if (lock || signal.aborted || !wants("keepAwake")) return release(l); // the answer came too late
      lock = l;
      l.addEventListener("release", () => lock === l && (lock = null));
    } catch {
      // refused (battery saver, no permission): the screen may dim by itself, nothing else breaks
    }
  };
  const drop = () => {
    const l = lock;
    lock = null;
    release(l);
  };

  // ---- Hiding the bar ----
  let hideTimer = 0;
  let hid = false;
  const blocked = () =>
    document.activeElement?.closest(".ts-readerbar, .ts-readdock") ||
    document.querySelector("#sheet [role=dialog], dialog[open]") ||
    !getSelection().isCollapsed;
  const hide = () => {
    if (blocked()) return arm();
    page.dataset.chrome = "off";
    hid = true;
  };
  const arm = () => {
    clearTimeout(hideTimer);
    if (wants("autoHide")) hideTimer = setTimeout(hide, HIDE_MS);
  };

  // A bar that hid itself comes back for a mouse moved to the top or bottom band, and for a key (a key that only
  // turns the page or scrolls does not count, or reading by keyboard would keep calling the bar back).
  const BAND_PX = 64;
  const READING_KEYS = new Set(["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " ", "[", "]"]);
  const reveal = () => {
    if (page.dataset.chrome !== "off" || !wants("autoHide")) return;
    page.dataset.chrome = "on";
    arm();
  };
  on(window, "pointermove", (event) => {
    if (event.pointerType === "mouse" && (event.clientY < BAND_PX || event.clientY > window.innerHeight - BAND_PX)) reveal();
  });
  on(window, "keydown", (event) => READING_KEYS.has(event.key) || event.ctrlKey || event.metaKey || event.altKey || reveal());

  // One place for all three: called at mount and whenever a setting changes.
  const refresh = () => {
    warm();
    if (wants("keepAwake")) take();
    else drop();
    arm();
    if (!wants("autoHide") && hid) {
      hid = false;
      page.dataset.chrome = "on";
    }
  };

  const touched = () => {
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => {
      idle = true;
      drop();
    }, IDLE_MS);
    if (idle) {
      idle = false;
      take();
    }
    arm();
  };

  // Leaving the page (a boosted link swaps #main; a plain one unloads) ends everything.
  const warmTimer = setInterval(() => (root.isConnected ? warm() : ctl.abort()), WARM_CHECK_MS);
  signal.addEventListener("abort", () => {
    clearTimeout(hideTimer);
    clearTimeout(idleTimer);
    clearInterval(warmTimer);
    drop();
    if (running === ctl) running = null;
  });
  // Capture phase on window: the reader stops the keys it handles (arrows, volume turns), which must still count as input.
  for (const type of ["pointerdown", "pointermove", "keydown"]) window.addEventListener(type, touched, { signal, capture: true });
  on(document, "ts-reader-look", refresh);
  on(document, "visibilitychange", () => (document.visibilityState === "visible" ? take() : null));
  on(document, "htmx:after:swap", () => root.isConnected || ctl.abort());
  on(window, "pagehide", () => ctl.abort());
  refresh();
  touched();
}
