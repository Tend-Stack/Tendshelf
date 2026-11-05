// Offline reading (decision 0019): the place queue and the stored reader shell. A place is queued before
// any send, because a beacon gives no answer.
const QUEUE = "ts.offline.reading";

function load(key, fallback) {
  try {
    return JSON.parse(localStorage.getItem(key)) || fallback;
  } catch {
    return fallback;
  }
}

function held(id) {
  return !!load("ts.offline", {}).books?.[id]?.text;
}

function strip(form) {
  const fields = new URLSearchParams(form);
  for (const name of ["_csrf", "chars_read", "active_ms"]) fields.delete(name);
  return fields.toString();
}

// An answered record with nothing left to count is finished.
function settle(queue, id, form) {
  const record = queue[id];
  if (!record || record.form !== form || record.chars || record.active) return;
  if (held(id)) record.sent = true;
  else delete queue[id];
}

// reader.js reports a save: "place" before any send, then "saved" or "unsent".
function note(name, id, detail) {
  const queue = load(QUEUE, {});
  const record = queue[id];
  if (name === "place") {
    queue[id] = { form: strip(detail), chars: record?.chars || 0, active: record?.active || 0, sent: false };
  } else if (name === "saved") {
    settle(queue, id, strip(detail));
  } else if (record) {
    const fields = new URLSearchParams(detail);
    record.chars += Number(fields.get("chars_read")) || 0;
    record.active += Number(fields.get("active_ms")) || 0;
  }
  localStorage.setItem(QUEUE, JSON.stringify(queue));
}

// Sends unsent records in slices within the per-save caps. False means try again later.
export async function flushReading(csrf) {
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
      // Answered, even if refused for good: take the slice off the record as it is now.
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

// The place the shell opens at: the address, else the queue when newer than the shell, else the shell's own.
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
  text.innerHTML = await res.text();
  window.htmx?.process(text);
  // A fragment has no link to the part before it; the page has one.
  const section = text.firstElementChild;
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
  // A reader request with no answer says so; stops once the marker has left.
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
  else if (navigator.onLine && held(id)) refreshShell(id).catch(() => {});
}
