//go:build tendshelf_probe

package probe

// The exit-run plans (M6 D7). Each plan is one script that runs on every page load of a launch and
// decides what to do from where it is, so a slow start or an extra page does not shift the steps.

// switchScript is the "switch" plan. Before the switch it reports the saved mode's page and asks Go to
// open the picker. After it, on the picker, it chooses the connect mode; on the server's page it reports and ends.
func switchScript(mode string, switched bool, server string) string {
	if !switched {
		return `(() => { ` + post + `
if (!location.protocol.startsWith("http")) return;
reply("switch.before", {href: location.href, origin: location.origin, shell: document.documentElement.dataset.shell || ""});
reply("switchNow", 1);
})()`
	}
	return `(() => { ` + post + `
if (location.protocol === "wails:") {
  if (!location.search.includes("change")) return;
  reply("switch.picker", {href: location.href});
  const form = document.querySelector('form[action="/start/connect"]');
  form.querySelector('input[name="server"]').value = ` + js(server) + `;
  form.querySelector('button').click();
  return;
}
reply("switch.after", {href: location.href, origin: location.origin, shell: document.documentElement.dataset.shell || ""});
reply("done", true);
})()`
}

// relaunchScript is the "relaunch" plan: the app starts with its saved mode. It checks that the sign-in
// survived the restart, drives the reader on a synthetic EPUB, then leaves and comes back to check the
// reader resumes. sessionStorage carries the step across page loads.
func relaunchScript() string {
	return `(async () => { ` + post + `
if (!location.protocol.startsWith("http")) return;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const doc = (html) => new DOMParser().parseFromString(html, "text/html");
const step = sessionStorage.getItem("probeStep") || "start";
const bookHref = async () => {
  const home = doc(await (await fetch("/", {credentials: "same-origin"})).text());
  const a = home.querySelector('a[href^="/books/"]');
  return a ? a.getAttribute("href") : "";
};
try {
  if (step === "start") {
    const res = await fetch("/", {credentials: "same-origin"});
    const book = await bookHref();
    reply("relaunch", {href: location.href, shell: document.documentElement.dataset.shell || "",
      signedIn: res.url.indexOf("/login") < 0 && res.url.indexOf("/setup") < 0, book,
      workers: navigator.serviceWorker ? (await navigator.serviceWorker.getRegistrations()).length : "none"});
    sessionStorage.setItem("probeStep", "readerA");
    sessionStorage.setItem("probeBook", book);
    location.href = book + "/read?ch=2";
  } else if (step === "readerA") {
    // Open the reader, set theme and flow with Aa, then load the page again so it starts in that flow.
    sessionStorage.setItem("probeStep", "readerB");
    await (` + readerSteps + `)(reply, wait, "A");
    location.reload();
  } else if (step === "readerB") {
    sessionStorage.setItem("probeStep", "readerC");
    await (` + readerSteps + `)(reply, wait, "B");
    // Reached only if following the search hit swapped in place instead of loading a page.
    sessionStorage.setItem("probeStep", "leave");
    await (` + readerSteps + `)(reply, wait, "C");
    location.href = "/";
  } else if (step === "readerC") {
    sessionStorage.setItem("probeStep", "leave");
    await (` + readerSteps + `)(reply, wait, "C");
    location.href = "/";
  } else if (step === "leave") {
    sessionStorage.setItem("probeStep", "resume");
    location.href = sessionStorage.getItem("probeBook") + "/read";
  } else if (step === "resume") {
    sessionStorage.setItem("probeStep", "end");
    await (` + resumeStep + `)(reply, wait);
    reply("done", true);
  }
} catch (e) {
  reply("relaunch.error", String(e && e.message || e));
  reply("done", true);
}
})()`
}

// readerSteps drives the reader rows of docs/manual-checks.md (M5 book reader) that a script can. A row
// that cannot be driven is not here; it stays for a person. Each step answers "reader.N" with what it saw,
// and a failure in one step does not stop the next.
const readerSteps = `async (reply, wait, part) => {
  const waitFor = async (fn, ms) => { const end = Date.now() + ms; while (Date.now() < end) { try { const v = fn(); if (v) return v; } catch (e) {} await wait(100); } return false; };
  const key = (k, opts) => window.dispatchEvent(new KeyboardEvent("keydown", Object.assign({key: k, bubbles: true, cancelable: true}, opts || {})));
  const box = () => document.querySelector("section[data-part]");
  const stepW = () => { const s = getComputedStyle(document.getElementById("reader")); return parseFloat(s.getPropertyValue("--ts-cols")) * (parseFloat(s.getPropertyValue("--ts-col")) + parseFloat(s.getPropertyValue("--ts-gap"))); };
  const page = () => Math.round(box().scrollLeft / stepW());
  const onPage = (el) => { const b = box().getBoundingClientRect(); return [...el.getClientRects()].some((r) => r.right > b.left + 1 && r.left < b.right - 1); };
  const shown = () => [...document.querySelectorAll(".ts-s")].filter(onPage).map((e) => e.dataset.sid);
  const run = async (id, fn) => { try { reply(id, await fn()); } catch (e) { reply(id, {error: String(e && e.message || e)}); } };
  const press = (el) => { const ev = new MouseEvent("click", {bubbles: true, cancelable: true}); el.dispatchEvent(ev); return ev.defaultPrevented; };
  const dialogOpen = () => document.querySelector("#sheet [role=dialog]") !== null;
  const closeSheet = async () => {
    const x = document.querySelector("#sheet [role=dialog] [data-ts-close]");
    if (x) x.click();
    await waitFor(() => !dialogOpen(), 3000);
    await wait(200);
  };

  if (part !== "C") await run("reader.1.open." + part, async () => {
    const ok = await waitFor(() => window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages, #reader.ts-reader--scroll"), 15000);
    const r = document.getElementById("reader");
    const b = box() && box().getBoundingClientRect();
    const first = document.querySelector(".ts-s");
    const fr = first && first.getBoundingClientRect();
    return {ok: !!ok, flow: r ? r.className : "", chrome: document.querySelector(".ts-readerpage")?.dataset.chrome,
      columns: r ? getComputedStyle(r).getPropertyValue("--ts-cols") : "", pageScrollsSideways: document.documentElement.scrollWidth > innerWidth,
      firstSentenceInBox: !!(b && fr && fr.left >= b.left - 1 && fr.right <= b.right + 1 && fr.top >= b.top - 1), viewport: [innerWidth, innerHeight], parts: document.querySelectorAll("section[data-part]").length};
  });

  if (part === "A") await run("reader.4.aa", async () => {
    const aa = document.querySelector("a.ts-aa");
    aa.click();
    const open = await waitFor(dialogOpen, 8000);
    if (!open) return {error: "settings sheet did not open"};
    const html = document.documentElement;
    const before = {theme: html.dataset.theme || "", style: html.getAttribute("style") || ""};
    const sepia = document.getElementById("rs-theme-sepia");
    if (sepia) sepia.click();
    const themed = await waitFor(() => (html.dataset.theme || "") === "sepia", 5000);
    const radios = [...document.querySelectorAll("#sheet input[type=radio]")].map((r) => r.name + "=" + r.value + (r.checked ? "*" : ""));
    sessionStorage.setItem("probeTheme", html.dataset.theme || "");
    // Pages flow for the page-turn rows (the default, Auto, shows Scroll in a window this wide).
    const pagesRadio = document.querySelector('#sheet input[name="flow"][value="pages"]');
    if (pagesRadio) pagesRadio.click();
    const pages = await waitFor(() => document.querySelector("#reader.ts-reader--pages"), 8000);
    await closeSheet();
    await wait(500);
    return {before, after: html.dataset.theme || "", changedAtOnce: !!themed, flowPagesAtOnce: !!pages, readerClass: document.getElementById("reader").className, controls: radios.slice(0, 24)};
  });

  if (part === "B") await run("reader.7.footnote", async () => {
    const link = document.querySelector("a[data-note-href]");
    if (!link) return {error: "no footnote link on the page"};
    const before = location.search;
    link.focus();
    link.click();
    const open = await waitFor(() => document.querySelector("dialog.ts-readernote--note[open]"), 5000);
    if (!open) return {error: "popup did not open"};
    const text = open.textContent;
    const focusInside = !!document.activeElement.closest("dialog");
    open.click(); // the backdrop: a synthetic Escape key does not close a dialog, so only the backdrop is driven
    const closed = await waitFor(() => !document.querySelector("dialog.ts-readernote--note[open]"), 5000);
    await wait(200);
    return {popupText: text.trim().slice(0, 80), focusInside, closedByBackdrop: !!closed, focusReturned: document.activeElement === link || !!document.activeElement.hasAttribute("data-note-href"), pageDidNotMove: location.search === before, escapeNotDriven: true};
  });

  if (part === "B") await run("reader.2.turn", async () => {
    if (!document.querySelector("#reader.ts-reader--pages")) return {error: "not in Pages flow: " + document.getElementById("reader").className};
    const p0 = page();
    const cs = getComputedStyle(box());
    const lastS = [...document.querySelectorAll(".ts-s")].pop();
    const lr = lastS.getClientRects();
    reply("reader.2.css", {display: cs.display, columnCount: cs.columnCount, columnWidth: cs.columnWidth, columnGap: cs.columnGap, columnFill: cs.columnFill, width: cs.width, height: cs.height, overflowX: cs.overflowX, overflowY: cs.overflowY,
      scrollHeight: box().scrollHeight, offsetWidth: box().offsetWidth, boxLeft: box().getBoundingClientRect().left, lastSentenceLeft: lr.length ? lr[lr.length - 1].left : null, sentences: document.querySelectorAll(".ts-s").length,
      children: [...box().children].map((c) => c.tagName + "." + c.className + ":" + c.scrollWidth).slice(0, 4)});
    const layout = {scrollWidth: box().scrollWidth, clientWidth: box().clientWidth, step: stepW(), scrollLeft: box().scrollLeft, cols: getComputedStyle(document.getElementById("reader")).getPropertyValue("--ts-cols")};
    key("ArrowRight");
    const forward = await waitFor(() => page() === p0 + 1, 5000);
    reply("reader.2.layout", Object.assign(layout, {afterKey: box().scrollLeft, active: document.activeElement && document.activeElement.tagName}));
    key("ArrowLeft");
    const back = await waitFor(() => page() === p0, 5000);
    // Tap zones: a click in the right quarter and then the left quarter of the reading box.
    const b = box().getBoundingClientRect();
    const tap = (x) => box().dispatchEvent(new MouseEvent("click", {bubbles: true, cancelable: true, clientX: x, clientY: b.top + b.height / 2}));
    tap(innerWidth * 0.92);
    const tapForward = await waitFor(() => page() === p0 + 1, 5000);
    tap(innerWidth * 0.08);
    const tapBack = await waitFor(() => page() === p0, 5000);
    return {keysForward: !!forward, keysBack: !!back, tapForward: !!tapForward, tapBack: !!tapBack, swipe: "not driven (touch)", slideOnce: "not checked"};
  });

  if (part === "B") await run("reader.5-6.marks", async () => {
    const sid = shown()[3] || shown()[0];
    const s = document.querySelector('.ts-s[data-sid="' + sid + '"]');
    const select = (a, z) => {
      const w = document.createTreeWalker(s, NodeFilter.SHOW_TEXT); const nodes = []; for (let n = w.nextNode(); n; n = w.nextNode()) nodes.push(n);
      const spot = (k) => { for (const n of nodes) { if (k <= n.length) return [n, k]; k -= n.length; } };
      const r = new Range(); r.setStart(...spot(a)); r.setEnd(...spot(z));
      getSelection().removeAllRanges(); getSelection().addRange(r);
    };
    const out = {sid, colours: {}};
    select(2, 9);
    const shownBar = await waitFor(() => !document.getElementById("reader-marks").hidden, 5000);
    const bar = document.getElementById("reader-marks").getBoundingClientRect();
    out.toolbar = {shown: !!shownBar, top: bar.top, bottom: bar.bottom, buttons: [...document.querySelectorAll("#reader-marks [data-ts-mark]")].map((b) => (b.dataset.tsMark) + (b.dataset.colour ? ":" + b.dataset.colour : ""))};
    for (const colour of ["green"]) {
      const btn = document.querySelector('#reader-marks [data-ts-mark="highlight"][data-colour="' + colour + '"]');
      if (btn) btn.click();
      out.colours[colour] = !!(await waitFor(() => document.querySelector('.ts-hl[data-colour="' + colour + '"]'), 8000));
    }
    const s2 = document.querySelector('.ts-s[data-sid="' + sid + '"]');
    select(12, 18);
    await waitFor(() => !document.getElementById("reader-marks").hidden, 5000);
    document.querySelector('#reader-marks [data-ts-mark="note"]').click();
    out.noteDialog = !!(await waitFor(() => document.getElementById("reader-note-dialog").open, 5000));
    document.querySelector("#reader-note-dialog textarea").value = "a thought";
    document.querySelector('#reader-note-dialog button[value="save"]').click();
    out.noteDot = !!(await waitFor(() => document.querySelector(".ts-notedot"), 8000));
    out.sameNode = !!s2;
    return out;
  });

  if (part === "B") await run("reader.8.contents", async () => {
    reply('reader.8.diag', {handled: press(document.querySelector('.ts-readerbar a[href*="sheet=contents"]'))});
    const open = await waitFor(dialogOpen, 8000);
    const inertBehind = !!document.querySelector("#reader")?.closest("[inert]") || !!document.querySelector("[inert]");
    const items = document.querySelectorAll("#sheet [role=dialog] a").length;
    await closeSheet();
    return {opened: !!open, inertBehind, items};
  });

  if (part === "B") await run("reader.8.search", async () => {
    reply('reader.8.diag2', {handled: press(document.querySelector('.ts-readerbar a[href*="sheet=search"]'))});
    if (!(await waitFor(() => document.getElementById("reader-q"), 8000))) return {error: "search sheet did not open"};
    document.getElementById("reader-q").value = "zebra";
    document.getElementById("reader-q").form.requestSubmit();
    const hits = await waitFor(() => document.querySelector("#reader-hits mark"), 8000);
    if (!hits) return {error: "no hit listed"};
    // Following a hit may load a new page (part C then runs there) or swap in place (it runs right after this).
    reply("reader.8.hitFollowed", {hits: document.querySelectorAll("#reader-hits a").length});
    document.querySelector("#reader-hits a").click();
    await wait(4000);
    return {hitListed: true, unloaded: false};
  });

  if (part === "C") await run("reader.8.hit", async () => {
    const arrived = await waitFor(() => location.search.includes("hit=") && document.querySelector("#reader.ts-reader--pages"), 8000);
    await wait(800);
    const marked = CSS.highlights && CSS.highlights.has("ts-hit") ? [...CSS.highlights.get("ts-hit")].map((r) => r.toString()).join("") : "";
    return {arrived: !!arrived, search: location.search, markedText: marked, highlightsApi: !!CSS.highlights};
  });

  if (part === "C") await run("reader.10.nextpart", async () => {
    const r = document.getElementById("reader");
    if (!r || !r.classList.contains("ts-reader--pages")) return {error: "not in Pages flow"};
    const partsBefore = document.querySelectorAll("section[data-part]").length;
    const first = shown()[0];
    let turns = 0;
    let lastSid = first;
    let monotone = true;
    const num = (sid) => sid.split(".").map((x) => parseInt(x.replace(/\D/g, ""), 10));
    const less = (a, b) => { for (let i = 0; i < Math.max(a.length, b.length); i++) { if ((a[i] || 0) !== (b[i] || 0)) return (a[i] || 0) < (b[i] || 0); } return false; };
    const chapter0 = location.search;
    for (; turns < 80; turns++) {
      const before = location.search + "|" + page() + "|" + document.querySelectorAll("section[data-part]").length;
      key("ArrowRight");
      await wait(450);
      const s = shown()[0];
      if (s && lastSid && less(num(s), num(lastSid))) monotone = false;
      if (s) lastSid = s;
      if (document.querySelectorAll("section[data-part]").length !== partsBefore || location.search !== chapter0) break;
    }
    return {turns, partsBefore, partsAfter: document.querySelectorAll("section[data-part]").length, search: location.search, firstSid: first, lastSid, orderKept: monotone};
  });
  if (part === "C") {
    const sid = shown()[0] || "";
    sessionStorage.setItem("probeSid", sid);
    reply("reader.9.before", {sid, search: location.search});
    await wait(3000); // the reader saves the place as it goes; give it time before leaving
  }
}`

// resumeStep runs after the person left the reader and opened it again with no place in the address.
const resumeStep = `async (reply, wait) => {
  const waitFor = async (fn, ms) => { const end = Date.now() + ms; while (Date.now() < end) { try { const v = fn(); if (v) return v; } catch (e) {} await wait(100); } return false; };
  await waitFor(() => window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages, #reader.ts-reader--scroll"), 15000);
  await wait(1500);
  const box = document.querySelector("section[data-part]");
  const onPage = (el) => { const b = box.getBoundingClientRect(); return [...el.getClientRects()].some((r) => r.right > b.left + 1 && r.left < b.right - 1); };
  const sids = [...document.querySelectorAll(".ts-s")].filter(onPage).map((e) => e.dataset.sid);
  const want = sessionStorage.getItem("probeSid");
  reply("reader.9.resume", {want, shownNow: sids.slice(0, 3), resumed: sids.includes(want), search: location.search,
    themeKept: (document.documentElement.dataset.theme || "") === sessionStorage.getItem("probeTheme"), theme: document.documentElement.dataset.theme || ""});
}`
