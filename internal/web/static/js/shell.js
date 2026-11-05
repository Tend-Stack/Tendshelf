// Tendshelf app shell. The navigation sits outside #main, so a boosted swap does not
// re-render it: after each swap of #main, mark the current destination and move focus
// to the new content. htmx 4 event names use colons.
function isCurrent(href, path) {
  return href === "/" ? path === "/" : path === href || path.startsWith(href + "/");
}

// The most specific destination wins: /me/downloads is Downloads, not Profile as well.
function markCurrent(path) {
  const links = document.querySelectorAll("nav [data-nav]");
  let best = "";
  for (const link of links) {
    if (isCurrent(link.dataset.nav, path) && link.dataset.nav.length > best.length) best = link.dataset.nav;
  }
  for (const link of links) {
    if (link.dataset.nav === best) {
      link.setAttribute("aria-current", "page");
    } else {
      link.removeAttribute("aria-current");
    }
  }
}

// A destination is marked as soon as its request starts (htmx leaves modified clicks to the browser),
// so the nav never lags behind the click; the swap below marks it again from the address.
document.addEventListener("htmx:before:request", (event) => {
  const link = event.target.closest("nav [data-nav]");
  if (link) markCurrent(link.dataset.nav);
});

document.addEventListener("htmx:after:swap", (event) => {
  const target = event.detail && event.detail.ctx && event.detail.ctx.target;
  const main = document.getElementById("main");
  if (!main || (target && target !== main && !main.contains(target))) return;
  markCurrent(location.pathname);
  main.focus({ preventScroll: true });
});

// Progress values: data-ts-value (0 to 100) becomes the --ts-value custom property through the
// CSSOM, which the CSP allows (an inline style attribute would not be). Without JS the fill is 0.
function applyValues(root) {
  for (const el of root.querySelectorAll("[data-ts-value]")) {
    const v = Math.min(100, Math.max(0, Number.parseFloat(el.dataset.tsValue) || 0));
    el.style.setProperty("--ts-value", String(v));
  }
}
applyValues(document);
document.addEventListener("htmx:after:settle", () => applyValues(document));

// Cover tints: data-ts-tint ("#rrggbb", checked by the server for readable ink) becomes the
// --cover-tint custom property through the CSSOM, and .is-tinted switches the spine or blank cover
// from surface and ink to the tint. Without JS, or in the contrast theme, the tint is not used.
const tintPattern = /^#[0-9a-f]{6}$/;
function applyTints(root) {
  for (const el of root.querySelectorAll("[data-ts-tint]")) {
    const tint = el.dataset.tsTint;
    if (!tintPattern.test(tint)) continue;
    el.style.setProperty("--cover-tint", tint);
    el.classList.add("is-tinted");
  }
}
applyTints(document);
document.addEventListener("htmx:after:settle", () => applyTints(document));

// Shelf fit: the server sends the newest 40 books; a shelf marked data-ts-fit shows only the spines
// that fit whole on one row and hides the rest. "hidden" takes the others out of the tab order and
// the accessibility tree (pages-home.css makes .ts-spine[hidden] display: none). Without JS the row
// keeps its sideways scroll.
function fitShelf(shelf) {
  const spines = Array.from(shelf.children);
  if (spines.length === 0) return;
  // Show everything first, so every spine has a real width to measure.
  for (const spine of spines) {
    spine.hidden = false;
  }
  const style = getComputedStyle(shelf);
  const gap = parseFloat(style.columnGap) || 0;
  const padding = (parseFloat(style.paddingLeft) || 0) + (parseFloat(style.paddingRight) || 0);
  const room = shelf.clientWidth - padding;
  let used = 0;
  let shown = 0;
  for (const spine of spines) {
    const width = spine.getBoundingClientRect().width;
    const next = shown === 0 ? width : used + gap + width;
    // At least one spine always stays, however narrow the shelf is.
    if (next > room && shown > 0) break;
    used = next;
    shown++;
  }
  spines.forEach((spine, i) => {
    if (i < shown) return;
    spine.hidden = true;
  });
}

// A shelf is re-fitted when its width changes (a rotated phone, a resized window), not when its
// content does: fitting never changes the shelf's width, so the observer cannot loop.
const shelfWidths = new WeakMap();
const shelfObserver = new ResizeObserver((entries) => {
  for (const entry of entries) {
    const width = entry.contentRect.width;
    if (shelfWidths.get(entry.target) === width) continue;
    shelfWidths.set(entry.target, width);
    fitShelf(entry.target);
  }
});
function fitShelves(root) {
  for (const shelf of root.querySelectorAll("[data-ts-fit]")) {
    fitShelf(shelf);
    shelfObserver.observe(shelf);
  }
}
fitShelves(document);
document.addEventListener("htmx:after:settle", () => fitShelves(document));

// Text size (P3-11): choosing Standard or Larger on the Settings page applies at once; the form's own
// hx-post saves it. A failed save is not reverted (its toast says so, and the next full load
// reconciles the page), and the plain form post redirects with the attribute rendered by the server.
document.addEventListener("change", (event) => {
  const input = event.target;
  if (!(input instanceof HTMLInputElement) || input.name !== "text_size" || !input.checked) return;
  if (!input.closest('form[action="/me/settings"]')) return;
  if (input.value === "large") document.documentElement.dataset.textSize = "large";
  else delete document.documentElement.dataset.textSize;
  fitShelves(document);
});

// Page enter without View Transitions: outerSync resets the class of #main to the response's, so
// adding ts-entering after each swap of #main restarts the keyframe every time.
document.addEventListener("htmx:after:swap", (event) => {
  if (document.startViewTransition) return;
  const target = event.detail && event.detail.ctx && event.detail.ctx.target;
  const main = document.getElementById("main");
  if (main && target === main) main.classList.add("ts-entering");
});

// Sheets live in #sheet, outside #main. Opening one (a swap into #sheet, or a full page load with
// the sheet in it) focuses it; closing plays the exit motion, then returns focus to the opener.
let opener = null;
// The reader page is a full-screen layer; behind an open sheet it is inert, so Tab cannot land under the scrim.
function holdReaderPage() {
  const page = document.querySelector(".ts-readerpage");
  if (page) page.inert = Boolean(document.querySelector("#sheet .ts-sheet"));
}
function focusSheet(source) {
  holdReaderPage();
  const sheet = document.querySelector("#sheet .ts-sheet");
  if (!sheet) return;
  opener = source || null;
  sheet.focus();
}
function closeSheet() {
  const layer = document.querySelector("#sheet .ts-sheet-layer");
  if (!layer) return false;
  for (const el of layer.querySelectorAll(".ts-sheet, .ts-scrim")) el.classList.add("is-leaving");
  return true;
}
focusSheet(null);
document.addEventListener("htmx:after:swap", (event) => {
  const ctx = event.detail && event.detail.ctx;
  if (ctx && ctx.target && ctx.target.id === "sheet") focusSheet(ctx.sourceElement);
});
document.addEventListener("click", (event) => {
  if (event.target.closest && event.target.closest("[data-ts-close]") && closeSheet()) event.preventDefault();
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && closeSheet()) event.preventDefault();
});

// Dragging a sheet down closes it: from its handle, header, or a list scrolled to its top; under 80 px it snaps back.
let drag = null;
document.addEventListener("touchstart", (event) => {
  const target = event.target;
  const sheet = target.closest(".ts-sheet:not(.is-leaving)");
  const list = sheet && !target.closest(".ts-sheet__grab, .ts-sheet__head") && target.closest("ol, ul");
  const scrolled = sheet && (sheet.scrollTop > 0 || (list && list.scrollTop > 0));
  drag = sheet && !scrolled && !target.closest("input") ? { sheet, y0: event.touches[0].clientY } : null;
});
document.addEventListener("touchmove", (event) => {
  if (!drag) return;
  const dy = event.touches[0].clientY - drag.y0;
  if (!drag.on && dy < -6) drag = null;
  else if (dy > 6) drag.on = true;
  if (!drag || !drag.on) return;
  if (event.cancelable) event.preventDefault();
  drag.dy = Math.max(dy, 0);
  drag.sheet.classList.add("is-dragging");
  drag.sheet.style.setProperty("--ts-sheet-drag", "translateY(" + drag.dy + "px)");
}, { passive: false });
function endDrag() {
  const { sheet, dy, on } = drag || {};
  drag = null;
  if (!on) return;
  sheet.classList.remove("is-dragging");
  if (dy >= 80 && sheet.closest("#sheet")) return closeSheet();
  sheet.style.setProperty("--ts-sheet-drag", "");
  if (dy >= 80) sheet.parentElement.querySelector("[data-ts-sheet-close]").click();
}
for (const type of ["touchend", "touchcancel"]) document.addEventListener(type, endDrag);

// A sheet can also be closed by the server: a saved note answers with an empty #sheet (out of band),
// so no exit animation runs. When the region empties while focus was inside it (focus falls back to
// the body), focus returns to the opener, as it does after the animation below; an opener that is
// gone or cannot take focus leaves it at the start of the page content.
// The answer may replace #sheet itself (an out-of-band swap) or empty it, so the body's own children
// and the current #sheet's children are both watched.
function returnFocusIfSheetGone() {
  holdReaderPage();
  const region = document.getElementById("sheet");
  if (region && region.childElementCount > 0) return;
  // htmx finishes its own swap work (settling, restoring focus by id) after the mutation, so the
  // check waits a moment rather than racing it.
  setTimeout(() => {
    const focusLost = !document.activeElement || document.activeElement === document.body;
    if (!focusLost) return;
    // An opener can still be in the document yet unfocusable (a toast link inside the closed full
    // player), so the page content takes focus whenever the opener does not.
    if (opener && opener.isConnected) opener.focus();
    const main = document.getElementById("main");
    if (document.activeElement !== opener && main) main.focus();
  }, 60);
}
let watchedSheet = null;
const sheetWatcher = new MutationObserver(returnFocusIfSheetGone);
function watchSheetRegion() {
  const region = document.getElementById("sheet");
  if (!region || region === watchedSheet) return;
  watchedSheet = region;
  sheetWatcher.observe(region, { childList: true });
}
watchSheetRegion();
new MutationObserver(() => {
  watchSheetRegion();
  returnFocusIfSheetGone();
}).observe(document.body, { childList: true });

// Leaving sheets and toasts are removed when their exit animation ends (a sheet takes its layer along).
document.addEventListener("animationend", (event) => {
  const el = event.target;
  if (!(el instanceof Element)) return;
  if (el.classList.contains("is-leaving")) {
    const layer = el.classList.contains("ts-sheet") && el.closest(".ts-sheet-layer");
    (layer || el).remove();
    holdReaderPage();
    if (layer && opener && opener.isConnected) opener.focus();
  } else if (el.classList.contains("ts-entering")) el.classList.remove("ts-entering");
});

// Filter chip menus (<details data-ts-chipmenu>): they open and their links work without this file.
// With it, opening one closes the others, a click elsewhere or Escape closes it, and Escape puts
// focus back on its chip.
function openChipMenus() {
  return document.querySelectorAll("details[data-ts-chipmenu][open]");
}
document.addEventListener("toggle", (event) => {
  const menu = event.target;
  if (!(menu instanceof Element) || !menu.matches("details[data-ts-chipmenu]") || !menu.open) return;
  for (const other of openChipMenus()) if (other !== menu) other.open = false;
}, true);
document.addEventListener("click", (event) => {
  for (const menu of openChipMenus()) if (!menu.contains(event.target)) menu.open = false;
});
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape") return;
  for (const menu of openChipMenus()) {
    menu.open = false;
    const chip = menu.querySelector("summary");
    if (chip) chip.focus();
    event.preventDefault();
  }
});

// Keys (P1c-4): "/" search, "g" then "l" or "h" libraries or home, "?" the shortcuts sheet. Space and
// the arrows belong to player.js. Nothing fires with a modifier, in a field, or while a dialog or sheet is open.
let chord = 0;
function goTo(href) {
  const link = [...document.querySelectorAll('nav a[href="' + href + '"]')].find((a) => a.offsetParent);
  if (link) link.click();
}
document.addEventListener("keydown", (event) => {
  if (event.ctrlKey || event.metaKey || event.altKey) return;
  const t = event.target instanceof Element ? event.target : document.body;
  if (t.closest("input, textarea, select, [contenteditable]:not([contenteditable=false])") || document.querySelector("dialog[open], #sheet .ts-sheet")) return;
  const second = chord && performance.now() - chord < 1500;
  chord = 0;
  if (second && event.key === "l") goTo("/libraries");
  else if (second && event.key === "h") goTo("/");
  else if (event.key === "g") { chord = performance.now(); return; }
  else if (event.key === "/") {
    const field = document.querySelector("input[type=search]");
    if (field) field.focus(); else goTo("/search");
  } else if (event.key === "?") document.getElementById("keys-help").showModal();
  else return;
  event.preventDefault();
});

// Measurement only (make perf): the moment the shell script has run. No data leaves the browser.
performance.mark("ts-ready");

// A click during a page change's view transition reaches <html>, not the link under the pointer
// (the browser hit-tests the transition, whatever CSS says), so a quick second click was lost. Such
// a click ends the transition at once and is replayed on the element that is really there.
document.addEventListener("click", (event) => {
  const transition = document.activeViewTransition;
  if (!transition || event.target !== document.documentElement) return;
  event.preventDefault();
  event.stopPropagation();
  const x = event.clientX;
  const y = event.clientY;
  transition.skipTransition();
  transition.finished.finally(() => {
    // An icon (SVG) has no click(): replay on the control around it.
    const target = document.elementFromPoint(x, y)?.closest("a, button, summary, label, input, [role=button]");
    if (target) target.click();
  });
}, true);

// Time zone (decision 0013): listening days end at the person's midnight, so the server keeps the
// zone of their browser. When it differs from the stored one (data-ts-tz on <body>), send it once to
// the settings route. sessionStorage remembers what was sent, so a refused or ignored post is not
// retried on every page load. Link pages and sign-in pages have no account and send nothing.
function sendTimeZone() {
  const body = document.body;
  if (body.dataset.tsLink || !document.documentElement.dataset.tsPerson) return;
  let zone = "";
  try {
    zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return;
  }
  if (!zone || zone === body.dataset.tsTz) return;
  try {
    if (sessionStorage.getItem("ts.tz.sent") === zone) return;
    sessionStorage.setItem("ts.tz.sent", zone);
  } catch {
    return; // storage is blocked: skip rather than post on every page
  }
  let token = "";
  try {
    token = JSON.parse(body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return;
  }
  fetch("/me/settings", {
    method: "POST",
    credentials: "same-origin",
    headers: { "X-CSRF-Token": token, "HX-Request": "true", "Content-Type": "application/x-www-form-urlencoded" },
    body: "time_zone=" + encodeURIComponent(zone),
  }).catch(() => {});
}
sendTimeZone();

// Page modules: an element with data-ts-module names a script only its page needs (the uploader).
// Each is imported on first use and mounted on every fresh element, at start and after a swap.
const mounted = new WeakSet();
function loadPageModules() {
  for (const el of document.querySelectorAll("#main [data-ts-module]")) {
    if (mounted.has(el)) continue;
    mounted.add(el);
    import(el.dataset.tsModule).then((m) => m.mount(el)).catch(() => {});
  }
}
loadPageModules();
document.addEventListener("htmx:after:swap", loadPageModules);
