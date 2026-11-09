//go:build tendshelf_probe

package probe

import "os"

// pagesScript drives the pages reader (plan docs/plans/reader-pages-flip.md, section 5) inside the real
// window: the layout, a flip caught half way (the animations are paused at 50 % for a few seconds so the
// driver can take a screenshot: it waits for the answer "mid.N"), edges, the ribbon, the numbers, the
// frame gaps of a run of turns, and a page change out of the reader. It runs on a saved connect session
// (sign-in kept) and ends with "done".
func pagesScript() string {
	switch os.Getenv("TENDSHELF_PAGES_ONLY") {
	case "nav":
		return navScript()
	case "exp":
		return expScript()
	}
	return `(async () => { ` + post + `
if (!location.protocol.startsWith("http")) return;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const doc = (html) => new DOMParser().parseFromString(html, "text/html");
const step = sessionStorage.getItem("pgStep") || "start";
const violations = (window.tsViolations = window.tsViolations || []);
document.addEventListener("securitypolicyviolation", (e) => violations.push(e.violatedDirective + " " + e.blockedURI));
try {
  if (step === "start") {
    const home = doc(await (await fetch("/", {credentials: "same-origin"})).text());
    const a = home.querySelector('a[href^="/books/"]');
    sessionStorage.setItem("pgStep", "read");
    reply("start", {href: location.href, book: a ? a.getAttribute("href") : ""});
    location.href = a.getAttribute("href") + "/read?ch=2";
    return;
  }
  if (step !== "read") return;
  sessionStorage.setItem("pgStep", "back");
  const R = () => document.getElementById("reader");
  const key = (k) => window.dispatchEvent(new KeyboardEvent("keydown", {key: k, bubbles: true, cancelable: true}));
  const foot = () => (document.getElementById("reader-foot-a")?.textContent || "") + " | " + (document.getElementById("reader-foot-b")?.textContent || "");
  const cssv = (n) => getComputedStyle(R()).getPropertyValue(n).trim();
  for (let i = 0; i < 60 && !R()?.classList.contains("ts-reader--pages"); i++) await wait(250);
  await wait(800);
  const root = R();
  const lay = performance.getEntriesByName("ts-reader-lay").map((e) => Math.round(e.duration));
  const probeFace = (() => { const t = document.createElement("div"); t.className = "ts-flip__face"; document.body.append(t); const c = getComputedStyle(t); const v = c.backfaceVisibility + "/" + c.webkitBackfaceVisibility; t.remove(); return v; })();
  reply("layout", {href: location.href, w: innerWidth, h: innerHeight, dpr: devicePixelRatio, flow: root.dataset.flow, turn: root.dataset.turn,
    pagesClass: root.classList.contains("ts-reader--pages"), cols: cssv("--ts-cols"), col: cssv("--ts-col"), pageH: cssv("--ts-page-h"),
    frameW: cssv("--ts-frame-w"), foot: foot(), layMs: lay, slow: localStorage.getItem("ts.flip.slow"), hinted: localStorage.getItem("ts.reader.hinted"),
    flipSrc: !!root.dataset.flipSrc, vtName: getComputedStyle(document.getElementById("main") || document.body).viewTransitionName,
    sw: navigator.serviceWorker ? (await navigator.serviceWorker.getRegistrations()).length : "none",
    edges: [...document.querySelectorAll(".ts-edge")].map((e) => { const r = e.getBoundingClientRect(); return [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)]; }),
    ribbons: [...document.querySelectorAll(".ts-ribbon")].map((e) => e.hidden ? "hidden" : "shown"),
    probeFace, preserve3d: CSS.supports("transform-style", "preserve-3d"), vt: typeof document.startViewTransition});
  // A gap meter for the frames of every turn: our own rAF loop.
  const gaps = [];
  let meter = 0;
  const startMeter = () => { let last = performance.now(); const tick = () => { const n = performance.now(); gaps.push(n - last); last = n; meter = requestAnimationFrame(tick); }; meter = requestAnimationFrame(tick); };
  const stopMeter = () => cancelAnimationFrame(meter);
  const rect = (el) => { const r = el.getBoundingClientRect(); return [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)]; };
  const stageInfo = () => {
    const st = document.querySelector(".ts-flip");
    if (!st) return null;
    const leaf = st.querySelector(".ts-flip__leaf");
    const faces = [...st.querySelectorAll(".ts-flip__face")];
    const cs = (el) => getComputedStyle(el);
    return {leafTransform: leaf && cs(leaf).transform, leafStyle: leaf && cs(leaf).transformStyle, leafRect: leaf && rect(leaf),
      faces: faces.map((f) => ({bf: cs(f).backfaceVisibility, wbf: cs(f).webkitBackfaceVisibility, tr: cs(f).transform, rect: rect(f)})),
      inert: st.inert, hidden: st.getAttribute("aria-hidden"), persp: cs(st).perspective, anims: document.getAnimations().length, stageRect: rect(st)};
  };
  async function turn(k, name, pauseMs) {
    const before = foot();
    startMeter();
    key(k);
    let info = null;
    for (let i = 0; i < 40 && !info; i++) { await new Promise((r) => requestAnimationFrame(r)); info = stageInfo(); }
    if (pauseMs < 0 && info) {
      // Slow motion: the animations keep running at a tenth of their speed, so the driver can take
      // screenshots of the real, unpaused flight.
      await wait(20);
      for (const a of document.getAnimations()) a.playbackRate = 0.1;
      reply(name, {before, mid: stageInfo()});
    } else if (pauseMs && info) {
      // Hold the flight at 25, 50 and 75 % of its time; the driver takes a screenshot at each answer.
      await wait(40);
      const anims = document.getAnimations();
      for (const at of [25, 50, 75]) {
        for (const a of anims) { a.pause(); a.currentTime = a.effect.getComputedTiming().endTime * at / 100; }
        await new Promise((r) => requestAnimationFrame(r));
        await new Promise((r) => requestAnimationFrame(r));
        reply(name + "@" + at, {before, mid: stageInfo(), n: anims.length});
        await wait(pauseMs);
      }
      for (const a of anims) a.play();
    }
    for (let i = 0; i < 300 && document.querySelector(".ts-flip"); i++) await wait(50);
    stopMeter();
    await wait(150);
    return {before, after: foot(), stageGone: !document.querySelector(".ts-flip"), info};
  }
  const out = {};
  out.t1 = await turn("ArrowRight", "mid.1", 1500);
  gaps.length = 0;
  for (let i = 0; i < 6; i++) { await turn("ArrowRight", "x" + i, 0); await wait(300); }
  const s = gaps.slice(1).sort((a, b) => a - b);
  out.gaps = {n: s.length, p50: Math.round(s[Math.floor(s.length / 2)]), p90: Math.round(s[Math.floor(s.length * 0.9)]), max: Math.round(s[s.length - 1])};
  out.build = performance.getEntriesByName("ts-flip-build").map((e) => Math.round(e.duration));
  out.slow = localStorage.getItem("ts.flip.slow");
  out.mover = root.dataset.turn;
  out.violations = violations.slice();
  reply("turns", out);
  const back = await turn("ArrowLeft", "mid.2", 1500);
  reply("back", {before: back.before, after: back.after, stageGone: back.stageGone});
  const slowF = await turn("ArrowRight", "slow.1", -1);
  await wait(500);
  const slowB = await turn("ArrowLeft", "slow.2", -1);
  reply("slow", {fwd: [slowF.before, slowF.after], back: [slowB.before, slowB.after]});
  const edge = {};
  const f0 = foot();
  document.querySelector(".ts-edge--next").click();
  await wait(900);
  edge.next = [f0, foot()];
  document.querySelector(".ts-edge--prev").click();
  await wait(900);
  edge.prev = foot();
  const rib = document.querySelector(".ts-ribbon");
  const pressed0 = rib.getAttribute("aria-pressed");
  rib.click();
  await wait(1500);
  edge.ribbon = [pressed0, rib.getAttribute("aria-pressed")];
  rib.click();
  await wait(1500);
  edge.ribbonAgain = rib.getAttribute("aria-pressed");
  edge.violations = violations.slice();
  reply("edges", edge);
  const modes = {};
  for (const m of ["slide", "none", "flip"]) {
    root.dataset.turn = m;
    const r = await turn("ArrowRight", "m." + m, 0);
    modes[m] = {before: r.before, after: r.after, sawStage: !!r.info};
  }
  reply("modes", modes);
  // The page change out of the reader: a boosted link in the navigation.
  const link = document.querySelector('a[href="/"]');
  let vt = 0;
  const orig = document.startViewTransition && document.startViewTransition.bind(document);
  if (orig) document.startViewTransition = (...a) => { vt++; return orig(...a); };
  const hrefBefore = location.href;
  link.click();
  await wait(2500);
  reply("leave", {hrefBefore, hrefAfter: location.href, vtCalls: vt, readerGone: !document.querySelector(".ts-readerpage"), vtName: getComputedStyle(document.getElementById("main")).viewTransitionName,
    mainOpacity: getComputedStyle(document.getElementById("main")).opacity, violations: violations.slice()});
  reply("done", true);
} catch (e) {
  reply("pages.error", String(e && e.stack || e));
  reply("done", true);
}
})()`
}

// navScript changes pages with boosted links on pages that are not the reader (the same swap the reader
// leaves by), to tell a crash of the swap itself from one that only the reader brings.
func navScript() string {
	return `(async () => { ` + post + `
if (!location.protocol.startsWith("http")) return;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const trail = [];
if (` + js(os.Getenv("TENDSHELF_PAGES_NOVT")) + ` === "1") document.startViewTransition = undefined;
try {
  for (const href of ["/me", "/", "/me", "/"]) {
    const link = document.querySelector('a[href="' + href + '"]');
    if (!link) { trail.push("no link " + href); continue; }
    reply("nav.next", {from: location.pathname, to: href, vtName: getComputedStyle(document.getElementById("main")).viewTransitionName});
    link.click();
    await wait(2000);
    trail.push(location.pathname);
    reply("nav.at", trail.slice());
  }
} catch (e) { trail.push(String(e)); }
reply("nav", trail);
reply("done", true);
})()`
}

// expScript holds a forward flight at 75 % (past the middle, where the back of the leaf shows) and tries
// style variants on it one at a time, each answered as "exp.<name>" for a screenshot: how a rendering
// problem in this engine is narrowed down. TENDSHELF_PAGES_VARIANTS is a JSON object of name to CSS. The
// styles go in through a constructed sheet (CSSOM), which the page's CSP allows.
func expScript() string {
	return `(async () => { ` + post + `
if (!location.protocol.startsWith("http")) return;
const wait = (ms) => new Promise((r) => setTimeout(r, ms));
const doc = (html) => new DOMParser().parseFromString(html, "text/html");
const step = sessionStorage.getItem("pgStep") || "start";
const variants = JSON.parse(` + js(os.Getenv("TENDSHELF_PAGES_VARIANTS")) + `);
try {
  if (step === "start") {
    const home = doc(await (await fetch("/", {credentials: "same-origin"})).text());
    const a = home.querySelector('a[href^="/books/"]');
    sessionStorage.setItem("pgStep", "read");
    location.href = a.getAttribute("href") + "/read?ch=2";
    return;
  }
  for (let i = 0; i < 60 && !document.getElementById("reader")?.classList.contains("ts-reader--pages"); i++) await wait(250);
  await wait(1500);
  window.dispatchEvent(new KeyboardEvent("keydown", {key: "ArrowRight", bubbles: true, cancelable: true}));
  for (let i = 0; i < 40 && !document.querySelector(".ts-flip__leaf"); i++) await new Promise((r) => requestAnimationFrame(r));
  await wait(40);
  const anims = document.getAnimations();
  for (const a of anims) { a.pause(); a.currentTime = a.effect.getComputedTiming().endTime * 0.75; }
  const sheet = new CSSStyleSheet();
  document.adoptedStyleSheets = [...document.adoptedStyleSheets, sheet];
  for (const [name, css] of Object.entries(variants)) {
    sheet.replaceSync(css);
    await new Promise((r) => requestAnimationFrame(r));
    await new Promise((r) => requestAnimationFrame(r));
    reply("exp." + name, {sides: [...document.querySelectorAll(".ts-flip__leaf>.ts-flip__side")].map((e) => getComputedStyle(e).transform)});
    await wait(1500);
  }
} catch (e) {
  reply("exp.error", String(e && e.stack || e));
}
reply("done", true);
})()`
}
