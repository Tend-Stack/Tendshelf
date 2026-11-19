// Offline reading (decision 0019): the place queue and the stored reader shell. A reading place is written
// to localStorage before any send, because a page-hide beacon gives no answer. offline.js sends the queue.
const QUEUE = "ts.offline.reading";

function load(key, fallback) {
  try {
    return JSON.parse(localStorage.getItem(key)) || fallback;
  } catch {
    return fallback;
  }
}

// The Android app stores the text itself (A9), where ts.offline does not exist, so every book counts as held.
const inApp = document.documentElement.hasAttribute("data-shell");

function held(id) {
  if (inApp) return true;
  return !!load("ts.offline", {}).books?.[id]?.text;
}

function strip(form) {
  const fields = new URLSearchParams(form);
  for (const name of ["_csrf", "chars_read", "active_ms"]) fields.delete(name);
  return fields.toString();
}

// A record whose place was answered and which has nothing left to count is finished.
function settle(queue, id, form) {
  const record = queue[id];
  if (!record || record.form !== form || record.chars || record.active) return;
  if (held(id)) record.sent = true;
  else delete queue[id];
}

// What reader.js says about a save: "place" before any send, then "saved" or "unsent".
function note(name, id, detail) {
  const queue = load(QUEUE, {});
  const record = queue[id] || { chars: 0, active: 0 };
  const fields = new URLSearchParams(detail);
  if (name === "place") {
    queue[id] = { form: strip(detail), chars: record.chars, active: record.active, sent: false };
  } else if (name === "unsent" && queue[id]) {
    record.chars += Number(fields.get("chars_read")) || 0;
    record.active += Number(fields.get("active_ms")) || 0;
  } else if (name === "saved") {
    settle(queue, id, strip(detail));
  }
  localStorage.setItem(QUEUE, JSON.stringify(queue));
}

// The iPhone app's stored reader (tsread://app) answers a place itself and keeps it on the phone until the server's own page
// takes it through the app (native-downloads.js, "rd"). These are those places, in the order they were made: for each book the
// newer form wins, the reading counts add up, and the queue then sends them like any other (offline.js). Returns the highest
// order number seen, even for one taken before, so the app can forget it; 0 when there was none.
const SEQ = "ts.offline.reading.seq";

export function adopt(items) {
  const taken = Number(localStorage.getItem(SEQ)) || 0;
  const queue = load(QUEUE, {});
  const deviceId = localStorage.getItem("ts.deviceId");
  const valid = (Array.isArray(items) ? items : []).filter(
    (item) => item && /^[A-Za-z0-9_-]{1,64}$/.test(item.bookId) && Number.isFinite(item.seq) && typeof item.form === "string",
  );
  valid.sort((a, b) => a.seq - b.seq);
  let top = 0;
  let adopted = 0;
  for (const item of valid) {
    top = Math.max(top, item.seq);
    if (item.seq <= taken) continue;
    const fields = new URLSearchParams(item.form);
    const record = queue[item.bookId] || { form: "", chars: 0, active: 0 };
    const mine = Number(new URLSearchParams(record.form).get("client_updated_at")) || 0;
    const newer = (Number(fields.get("client_updated_at")) || 0) > mine;
    const form = new URLSearchParams(newer || !record.form ? strip(item.form) : record.form);
    if (deviceId) form.set("device_id", deviceId);
    queue[item.bookId] = {
      form: form.toString(),
      chars: record.chars + (Number(fields.get("chars_read")) || 0),
      active: record.active + (Number(fields.get("active_ms")) || 0),
      sent: false,
    };
    adopted = Math.max(adopted, item.seq);
  }
  if (adopted) {
    localStorage.setItem(QUEUE, JSON.stringify(queue));
    localStorage.setItem(SEQ, String(adopted));
  }
  return top;
}

// Sends every unsent record, a slice at a time within the server's per-save caps (200,000 characters and
// 10 minutes). Returns false when sending should stop and be tried again.
export async function flushReading(csrf) {
  // At tsread://app the app already keeps every save; sending from here would count them twice.
  if (location.protocol === "tsread:") return true;
  for (const id of Object.keys(load(QUEUE, {}))) {
    let record = load(QUEUE, {})[id];
    while (record && !record.sent) {
      const chars = Math.min(record.chars, 200000);
      const active = Math.min(record.active, 600000);
      const form = new URLSearchParams(record.form);
      form.set("chars_read", chars);
      form.set("active_ms", active);
      let res;
      try {
        res = await fetch("/reading/" + encodeURIComponent(id), { method: "POST", body: form, headers: { "X-CSRF-Token": csrf } });
      } catch {
        return false;
      }
      if (res.redirected || res.status === 403 || res.status === 429 || res.status >= 500) return false;
      // Answered (200, or a refusal that will not change): take the slice off the record as it is now.
      const queue = load(QUEUE, {});
      const now = queue[id];
      if (!now) break;
      now.chars = Math.max(0, now.chars - chars);
      now.active = Math.max(0, now.active - active);
      settle(queue, id, record.form);
      localStorage.setItem(QUEUE, JSON.stringify(queue));
      record = queue[id];
    }
  }
  return true;
}

// The place the shell opens at: the address, else the queue when it is newer than the shell, else the
// shell's own. The chapter and part come from the sentence id and the part starts the server wrote.
function placeOf(marker, root) {
  const here = new URLSearchParams(location.search);
  const saved = new URLSearchParams(load(QUEUE, {})[root.dataset.book]?.form || "");
  const newer = Number(saved.get("client_updated_at")) > Number(marker.dataset.placed);
  let [sid, off] = [root.dataset.at, root.dataset.off];
  let [chapter, part] = [Number(marker.dataset.startCh), Number(marker.dataset.startPart)];
  if (here.get("at")) [sid, off] = [here.get("at"), here.get("off")];
  else if (here.get("ch")) [sid, chapter, part] = ["", Number(here.get("ch")), Number(here.get("part")) || 1];
  else if (newer && saved.get("sid")) [sid, off] = [saved.get("sid"), saved.get("off")];
  const found = /^c(\d+)\.p(\d+)/.exec(sid);
  if (found) {
    chapter = Number(found[1]);
    const starts = JSON.parse(marker.dataset.partStarts)[chapter] || [1];
    part = starts.filter((start) => start <= Number(found[2])).length;
  }
  return { chapter, part, sid: found ? sid : "", off: off || "0" };
}

async function open(marker, root, copy) {
  const place = placeOf(marker, root);
  const text = document.getElementById("reader-text");
  const res = await fetch(marker.dataset.frag.replace("{c}", place.chapter) + (place.part > 1 ? "&part=" + place.part : "")).catch(() => null);
  if (!res?.ok) {
    text.textContent = copy.failed;
    return;
  }
  // The part goes in front of the page foot, which the page already carries.
  const holder = document.createElement("template");
  holder.innerHTML = await res.text();
  const section = holder.content.querySelector("section[data-part]");
  text.prepend(section);
  window.htmx?.process(text);
  // A fragment has no link back to the part before it; the page has one.
  const before = /chapters\/(\d+)\?(?:.*&)?(?:part=(\d+))?/.exec(section.dataset.prevHref || "");
  if (before) {
    const link = document.createElement("a");
    link.className = "ts-btn ts-btn--quiet ts-reader__prev";
    link.href = "/books/" + root.dataset.book + "/read?ch=" + before[1] + (before[2] ? "&part=" + before[2] : "");
    link.textContent = copy.prev;
    section.prepend(link);
  }
  root.dataset.at = place.sid;
  root.dataset.off = place.off;
  (await import(marker.dataset.readerSrc)).mount(root);
}

async function refreshShell(id) {
  const res = await fetch("/offline/books/" + id + "/read");
  if (!res.ok || res.redirected) return;
  const cache = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  await cache.put("/books/" + id + "/read", res);
}

export function mount(marker) {
  const root = document.getElementById("reader");
  const id = root.dataset.book;
  const copy = JSON.parse(root.dataset.copy || "{}");
  for (const name of ["place", "unsent", "saved"]) {
    root.addEventListener("ts:reader:" + name, (event) => note(name, id, event.detail));
  }
  // A request from the reader got no answer: say so. Stops once the marker has left.
  const stop = new AbortController();
  document.addEventListener(
    "htmx:error",
    (event) => {
      if (!marker.isConnected) stop.abort();
      else if (event.target.closest?.(".ts-readerpage")) window.tsPlayer?.toast("hint", copy.needsNet);
    },
    { signal: stop.signal },
  );
  if (marker.hasAttribute("data-offline-shell")) open(marker, root, copy);
  else if (!inApp && navigator.onLine && held(id)) refreshShell(id).catch(() => {});
}
