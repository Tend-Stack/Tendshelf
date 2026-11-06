// The desktop app's own title bar (decision 0023, board DesktopShell). The server loads this file only
// when the cookie ts_shell says "desktop", beside desktop.js; a browser never receives it. It moves the
// window by its bar, runs the window buttons and the More menu, keeps Back and Forward honest, and
// writes the place and the playing book into the bar. Every word is in the markup (DesktopBar).

const channel = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.external;
const host = document.getElementById("desktop-bar");

if (channel && host) {
  const post = (message) => channel.postMessage(message);
  const part = (name) => host.querySelector("[data-wbar-" + name + "]");
  const bar = host.querySelector("[data-wbar]");
  const onEmptyBar = (event) => !event.target.closest("a, button");

  // Move: arm on a plain press of the empty bar, and ask for the move on the first movement with
  // the button still held (the window cannot start it from a press alone).
  let armed = false;
  bar.addEventListener("mousedown", (event) => {
    armed = event.button === 0 && event.detail === 1 && onEmptyBar(event);
  });
  bar.addEventListener("mousemove", (event) => {
    if (!armed) return;
    armed = false;
    if (event.buttons & 1) post("wails:drag");
  });
  bar.addEventListener("dblclick", (event) => {
    if (onEmptyBar(event)) post("tendshelf:window maximise");
  });

  // A file dropped anywhere that is not a drop zone would make the window open it as a page.
  for (const type of ["dragover", "drop"]) window.addEventListener(type, (event) => event.preventDefault());

  // Drop a book (decision 0023, X5). The bar only gathers the files; the Add books page's own uploader
  // (upload.js) does the adding and gives the refusals, so a copy-protected file is refused as there.
  // Only a person who may add books has data-ts-can-add; anyone else gets plain words on a drop.
  const drop = part("drop");
  const addLink = part("add");
  const canAdd = () => host.dataset.tsCanAdd === "true";
  const hasFiles = (event) => !!event.dataTransfer && [...event.dataTransfer.types].includes("Files");
  const showDrop = (on) => {
    drop.hidden = !on;
  };
  const say = (note) => {
    const words = host.querySelector("template[data-wbar-note=" + note + "]");
    const toasts = document.getElementById("toasts");
    if (!words || !toasts) return;
    const toast = document.createElement("div");
    toast.className = "ts-toast";
    toast.setAttribute("role", "alert");
    toast.dataset.tsMs = "8000";
    toast.append(words.content.cloneNode(true));
    toasts.append(toast);
  };
  // Files wait here while the page changes to Add books (this file is not reloaded by a boosted visit).
  let waiting = [];
  const hand = async () => {
    if (!waiting.length) return true;
    const root = document.querySelector("#main [data-ts-module][data-ts-library]");
    const input = root && root.querySelector("[data-ts-pick=files]");
    if (!input) return false;
    const list = new DataTransfer();
    for (const file of waiting) list.items.add(file);
    waiting = [];
    // The uploader empties the input once it has taken the files. Until then its listener may not be
    // mounted yet (the module loads after the swap), so offer the files again, a few times.
    input.files = list.files;
    for (let tries = 0; tries < 30 && input.files.length; tries++) {
      input.dispatchEvent(new Event("change", { bubbles: true }));
      if (input.files.length) await new Promise((later) => setTimeout(later, 100));
    }
    return true;
  };
  if (drop) {
    window.addEventListener("dragenter", (event) => {
      if (canAdd() && hasFiles(event)) showDrop(true);
    });
    window.addEventListener("dragleave", (event) => {
      if (!event.relatedTarget) showDrop(false);
    });
    window.addEventListener("dragend", () => showDrop(false));
    window.addEventListener("keydown", (event) => {
      if (event.key === "Escape") showDrop(false);
    });
    window.addEventListener("drop", (event) => {
      showDrop(false);
      // A drop inside the Add books page itself is that page's own, and must not be added twice.
      if (!hasFiles(event) || event.target.closest("[data-ts-library]")) return;
      // Read the items now: they are gone once this handler returns.
      const items = [...event.dataTransfer.items].filter((item) => item.kind === "file");
      const folder = items.some((item) => item.webkitGetAsEntry && (item.webkitGetAsEntry() || {}).isDirectory);
      if (!canAdd()) return say("off");
      if (folder) return say("folder");
      waiting = items.map((item) => item.getAsFile()).filter(Boolean);
      if (!waiting.length) return;
      hand().then((done) => {
        if (done) return;
        document.addEventListener("htmx:after:swap", function once() {
          hand().then((arrived) => arrived && document.removeEventListener("htmx:after:swap", once));
        });
        addLink.click();
      });
    });
  }

  // The More menu: a button that opens a short list, arrow keys inside, Escape closes and gives focus back.
  const more = part("more");
  const menu = part("menu");
  const items = () => [...menu.querySelectorAll("[role^=menuitem]:not([hidden])")];
  const setMenu = (open) => {
    menu.hidden = !open;
    more.setAttribute("aria-expanded", String(open));
    more.classList.toggle("is-open", open);
  };
  if (more && menu) {
    more.addEventListener("click", () => {
      setMenu(menu.hidden);
      if (!menu.hidden) items()[0].focus();
    });
    menu.addEventListener("keydown", (event) => {
      const list = items();
      const at = list.indexOf(document.activeElement);
      const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: list.length - 1 }[event.key];
      if (to === undefined) return;
      event.preventDefault();
      list[(to + list.length) % list.length].focus();
    });
    host.addEventListener("keydown", (event) => {
      if (event.key === "Tab") setMenu(false);
      if (event.key !== "Escape" || menu.hidden) return;
      setMenu(false);
      more.focus();
    });
    document.addEventListener("pointerdown", (event) => {
      if (!event.target.closest("[data-wbar-menu], [data-wbar-more]")) setMenu(false);
    });
  }

  // Window buttons and menu items: one verb each. Keyboard shortcuts opens the app's own sheet.
  host.addEventListener("click", (event) => {
    const button = event.target.closest("[data-wbar-do]");
    if (!button) return;
    const verb = button.dataset.wbarDo;
    if (menu) setMenu(false);
    if (verb === "keys") {
      const sheet = document.getElementById("keys-help");
      if (sheet) sheet.showModal();
    } else if (verb === "keep") {
      // Keep playing is a switch: the page flips it at once, Go saves it.
      const on = button.getAttribute("aria-checked") !== "true";
      button.setAttribute("aria-checked", String(on));
      post("tendshelf:window keep-" + (on ? "on" : "off"));
    } else {
      post("tendshelf:window " + verb);
    }
  });

  // The window tells the page when it is in front and whether it fills the screen.
  const restore = part("restore");
  window.addEventListener("ts:desktop", (event) => {
    const state = event.detail;
    if (state === "active" || state === "inactive") document.documentElement.dataset.window = state;
    // Go says a tray exists (only then Keep playing is offered) and whether the choice is on.
    const keep = part("keep");
    if (state === "tray" && keep) keep.hidden = false;
    if ((state === "keep" || state === "nokeep") && keep) keep.setAttribute("aria-checked", String(state === "keep"));
    if (state === "maximised" || state === "restored") {
      part("max").hidden = state === "maximised";
      restore.hidden = state !== "maximised";
    }
  });

  // Back and Forward. The browser will not say whether it can go back, so every entry carries its
  // position (tsAt) in its state and the furthest one lives in the session. A replaced state keeps
  // its position, so only a real new page counts.
  const back = part("back");
  const forward = part("forward");
  const saved = (key) => Number(sessionStorage.getItem(key));
  let at = history.state && Number.isInteger(history.state.tsAt) ? history.state.tsAt : null;
  let last = saved("ts-last");
  if (at === null) {
    at = sessionStorage.getItem("ts-at") === null ? 0 : saved("ts-at") + 1;
    last = at;
  }
  const settle = () => {
    sessionStorage.setItem("ts-at", at);
    sessionStorage.setItem("ts-last", last);
    if (back) back.disabled = at <= 0;
    if (forward) forward.disabled = at >= last;
  };
  const push = history.pushState.bind(history);
  const replace = history.replaceState.bind(history);
  history.pushState = (state, ...rest) => {
    at += 1;
    last = at;
    push({ ...state, tsAt: at }, ...rest);
    settle();
  };
  history.replaceState = (state, ...rest) => replace({ ...state, tsAt: at }, ...rest);
  // htmx restores a page on Back only from an entry it has marked, so the first entry is marked too.
  replace({ htmx: true, ...history.state, tsAt: at }, "");
  window.addEventListener("popstate", (event) => {
    if (event.state && Number.isInteger(event.state.tsAt)) at = event.state.tsAt;
    settle();
  });
  settle();
  if (back) back.addEventListener("click", () => history.back());
  if (forward) forward.addEventListener("click", () => history.forward());
  document.addEventListener("keydown", (event) => {
    if (!event.altKey || event.ctrlKey || event.metaKey) return;
    if (event.key === "ArrowLeft") history.back();
    if (event.key === "ArrowRight") history.forward();
  });

  // The place: the page's title, and the page above it for a book, series or author.
  const crumb = part("crumb");
  const place = () => {
    const main = document.getElementById("main");
    const href = main && main.dataset.tsCrumbHref;
    crumb.hidden = !href;
    part("sep").hidden = !href;
    if (href) {
      crumb.href = href;
      crumb.textContent = main.dataset.tsCrumb;
    }
    part("title").textContent = document.title.replace(/ · Tendshelf$/, "");
  };
  document.addEventListener("htmx:after:swap", place);
  new MutationObserver(place).observe(document.head, { subtree: true, childList: true, characterData: true });

  // What is playing: the mini-player already knows the title, chapter, cover and place, so the bar
  // copies them. It says what is on and opens the player; it is never a second resume button.
  const player = document.getElementById("player");
  const now = part("now");
  if (player && now) {
    const mini = (name) => player.querySelector("[data-ts-" + name + "]");
    const cover = part("cover");
    const show = () => {
      const open = mini("open");
      const playing = !player.hidden && mini("title").textContent !== "";
      now.hidden = !playing;
      part("line").hidden = !playing;
      if (!playing) return;
      now.href = open.href;
      now.setAttribute("aria-label", open.getAttribute("aria-label"));
      part("nowtitle").textContent = mini("title").textContent;
      part("nowsub").textContent = mini("meta").textContent;
      cover.hidden = mini("cover").hidden;
      if (cover.src !== mini("cover").src) cover.src = mini("cover").src;
      part("line").style.setProperty("--ts-progress", mini("mini-progress").style.getPropertyValue("--ts-progress"));
    };
    now.addEventListener("click", (event) => {
      event.preventDefault();
      mini("open").click();
    });
    const watch = { subtree: true, childList: true, characterData: true, attributes: true };
    new MutationObserver(show).observe(player, { ...watch, attributeFilter: ["hidden", "src", "aria-label", "style"] });
  }

  // Tell Go what is playing and which theme is on (decision 0023, point 10): the tray shows the book
  // and the notifications know why playback stopped. Words are data; Go limits and escapes them.
  // The place left changes every second, so it is sent at most every 5 s; anything else at once.
  const root = document.documentElement;
  const text = (name) => (player && player.querySelector("[data-ts-" + name + "]") || {}).textContent || "";
  const sleepOn = () => !!window.tsPlayerFull && window.tsPlayerFull.sleepInfo().mode !== "off";
  let stop = "";
  let sleepWas = false;
  let sentAt = 0;
  let sentKey = null;
  let sentLeft = "";
  const tell = (event) => {
    const detail = event && event.detail;
    const playing = !!detail && detail.playing === true;
    if (playing) stop = "";
    else if (detail && detail.kind === "pause" && sentKey && JSON.parse(sentKey).playing) {
      if (detail.durationMs && detail.positionMs >= detail.durationMs - 2000) stop = "end";
      else if (sleepWas && !sleepOn()) stop = "sleep";
    }
    sleepWas = sleepOn();
    const open = player && !player.hidden && text("title") !== "";
    const light = matchMedia("(prefers-color-scheme: light)").matches;
    const state = {
      title: open ? text("title") : "",
      chapter: open ? text("meta") : "",
      left: open ? text("bar-left") : "",
      playing: open && (detail ? playing : player.classList.contains("is-playing")),
      theme: root.dataset.theme || (light ? "light" : "dark"),
      canAdd: host.dataset.tsCanAdd === "true",
      stop,
    };
    const { left, ...rest } = state;
    const key = JSON.stringify(rest);
    if (key === sentKey && (left === sentLeft || Date.now() - sentAt < 5000)) return;
    sentKey = key;
    sentLeft = left;
    sentAt = Date.now();
    post("tendshelf:state " + JSON.stringify(state));
  };
  document.addEventListener("ts:player:state", tell);
  document.addEventListener("ts:player:tick", tell);
  new MutationObserver(() => tell()).observe(root, { attributes: true, attributeFilter: ["data-theme"] });
  if (player) new MutationObserver(() => tell()).observe(player, { attributes: true, attributeFilter: ["hidden"] });
  tell();
}
