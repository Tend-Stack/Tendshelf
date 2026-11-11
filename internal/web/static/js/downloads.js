// Tendshelf download manager (decision 0012, docs/plans/m3-offline.md "Download manager").
// Loaded by offline.js when a page has a download control or the Downloads list, or when a book is
// still on its way. It saves each audio file into the person's cache in chunks, keeps the index in
// localStorage["ts.offline"], draws the Downloads page and the book page's Download control, and
// exposes window.tsDownloads for the tests and the other scripts. The text of a book (decision 0019)
// is stored by downloads-text.js, loaded when a book may have text.
const root = document.documentElement;
const person = root.dataset.tsPerson || "";
const CHUNK = Number(root.dataset.tsChunk) || 4 << 20;
const INDEX_KEY = "ts.offline";
const PREFS_KEY = "ts.offline.prefs";
const CHECKED_KEY = "ts.offline.checkedAt";
const MSGS_KEY = "ts.offline.msgs";
const MiB = 1 << 20;
const DAY = 86400000;
const supported = !!person && isSecureContext && "caches" in window;
const controllers = new Map(); // bookId -> AbortController of the download that is running
const debug = { failNextPut: "" };
let running = false;
let quiet = false; // a "Download all" batch is running: its own line speaks, not one toast per book
let rate = 0; // bytes per second, a moving average over the last chunks

// ---- The index of downloads ----

function readJSON(key, fallback) {
  try {
    return JSON.parse(localStorage.getItem(key)) || fallback;
  } catch {
    return fallback;
  }
}

function writeJSON(key, value) {
  try {
    localStorage.setItem(key, JSON.stringify(value));
  } catch {
    // storage is optional
  }
}

function readIndex() {
  const index = readJSON(INDEX_KEY, { v: 1, books: {} });
  index.books = index.books || {};
  return index;
}

function emit(bookId) {
  document.dispatchEvent(new CustomEvent("ts:downloads:change", { detail: { bookId } }));
}

// Changes one book's record on a fresh copy of the index (other tabs write too). Returns the new
// record, or null when the book is no longer in the index.
function patch(id, change) {
  const index = readIndex();
  const record = index.books[id];
  if (!record) return null;
  change(record);
  writeJSON(INDEX_KEY, index);
  emit(id);
  return record;
}

function list() {
  const books = readIndex().books;
  return Object.keys(books)
    .map((id) => ({ id, ...books[id] }))
    .sort((a, b) => a.addedAt - b.addedAt);
}

// ---- Words ----

// The sentences of the Downloads page, as the server wrote them (data-ts-msg-* on its root). The
// book page does not carry them, so they are kept in localStorage the first time the Downloads page
// is seen, or fetched once.
function messagesFrom(element) {
  const out = {};
  for (const attr of element.attributes) {
    if (attr.name.startsWith("data-ts-msg-")) out[attr.name.slice(12)] = attr.value;
  }
  writeJSON(MSGS_KEY, out);
  return out;
}

let msgs = readJSON(MSGS_KEY, {});

function loadMessages() {
  const page = document.querySelector("[data-ts-downloads]");
  if (page) msgs = messagesFrom(page);
}

function fill(text, vars) {
  return (text || "").replace(/\{(\w+)\}/g, (_, name) => (name in vars ? vars[name] : ""));
}

// Decimal units: "291 MB" below 1000 MB, "1.6 GB" above.
function size(bytes) {
  return bytes < 1e9 ? Math.max(1, Math.round(bytes / 1e6)) + " MB" : (bytes / 1e9).toFixed(1) + " GB";
}

function words(key, vars) {
  return fill(msgs[key], vars);
}

async function say(kind, key, record) {
  if (quiet) return;
  loadMessages();
  if (!msgs[key]) {
    try {
      const html = await (await fetch("/me/downloads")).text();
      msgs = messagesFrom(new DOMParser().parseFromString(html, "text/html").querySelector("[data-ts-downloads]"));
    } catch {
      return;
    }
  }
  if (!window.tsPlayer) return;
  // It leaves by itself and a tap dismisses it: player.js times every toast it makes for us.
  window.tsPlayer.toast(kind, words(key, { title: record.title }));
}

// ---- The person's cache ----

function openCache() {
  return caches.open("ts-person-" + person);
}

// downloads-text.js, loaded once when a book may have text (the banner carries its address).
let textLoad = null;

function textModule() {
  const banner = document.getElementById("ts-offline");
  const src = banner && banner.dataset.tsDownloadsTextSrc;
  if (!src) return Promise.reject(new Error("no text module"));
  textLoad ||= import(src).catch((e) => {
    textLoad = null;
    throw e;
  });
  return textLoad;
}

// Bytes of a record that are stored: its whole chunks and the text's share by items stored.
function doneOf(r) {
  const items = r.text ? r.text.parts.length + r.text.images.length : 0;
  const text = items ? Math.round((r.text.bytes * r.text.have) / items) : 0;
  return r.files.reduce((sum, f) => sum + haveBytes(f), 0) + text;
}

function chunkKey(file, n) {
  return file.key + (file.key.includes("?") ? "&" : "?") + "chunk=" + n;
}

// Bytes of a file that are stored, from its count of whole chunks.
function haveBytes(file) {
  return file.have >= file.chunks ? file.size : file.have * CHUNK;
}

// How many chunks from 0 are stored (a pause, a reload or a crash leaves some).
async function countChunks(cache, file) {
  let n = 0;
  while (n < file.chunks && (await cache.keys(chunkKey(file, n))).length) n++;
  return n;
}

// Deletes the file's chunks. The completion entry goes first, so it never stands without its chunks.
async function dropFile(cache, file) {
  await cache.delete(file.key);
  tellWorker();
  for (let n = 0; n < file.chunks; n++) await cache.delete(chunkKey(file, n));
  file.have = 0;
  file.etag = "";
}

// The worker answers /stream/ only for files it knows are stored, so it is told when that changes.
function tellWorker() {
  if (navigator.serviceWorker) {
    navigator.serviceWorker.ready.then((r) => r.active && r.active.postMessage({ type: "stored" })).catch(() => {});
  }
}

// Everything a book keeps in the cache: files, the player fragment, the cover and the peaks.
async function purge(record) {
  const cache = await openCache();
  if (record.kind === EPISODE) {
    // An episode keeps its file, the player fragment, the show's picture and its chapters, and no peaks.
    for (const file of record.files) await dropFile(cache, file);
    await cache.delete("/hx/player/episode/" + record.id);
    await cache.delete("/podcasts/chapters/" + record.id);
    if (record.cover) await cache.delete(record.cover);
    tellWorker();
    return;
  }
  for (const file of record.files) {
    await dropFile(cache, file);
    await cache.delete("/peaks/" + file.id);
  }
  await cache.delete("/hx/player/" + record.id);
  if (record.cover) await cache.delete(record.cover);
  if (record.text) await (await textModule()).purge(cache, record);
  tellWorker();
}

async function put(cache, key, response) {
  if (debug.failNextPut) {
    debug.failNextPut = "";
    throw new DOMException("The quota has been exceeded.", "QuotaExceededError");
  }
  await cache.put(key, response);
}

// ---- Starting, pausing and removing ----

// Raised inside a download to end it with a state.
class Halt extends Error {
  constructor(state, error) {
    super(error);
    this.state = state;
    this.error = error;
  }
}

// What the text module may use of this file: the queue's own helpers, so there is one put and one index.
const kit = { openCache, put, patch, Halt, sleep, done: doneOf };

// A podcast episode is a record of its own kind in the same index: one file under /podcasts/audio/, whose size is
// learned from the first answer because a feed's announced length is often wrong.
const EPISODE = "episode";

async function startEpisode(id) {
  if (!supported) return;
  const known = readIndex().books[id];
  if (known && known.state !== "failed") return;
  if (known) await remove(id);
  let doc = null;
  try {
    const res = await fetch("/api/v1/podcasts/episodes/" + encodeURIComponent(id), { headers: { Accept: "application/json" } });
    if (res.ok) doc = await res.json();
  } catch {
    // nothing to download without the episode's details
  }
  if (!doc) return;
  const index = readIndex();
  index.books[id] = {
    kind: EPISODE,
    title: doc.title,
    author: doc.show,
    cover: "",
    bytes: doc.bytes || 0,
    done: 0,
    state: "waiting",
    error: "",
    addedAt: Date.now(),
    readyAt: 0,
    finishedAt: 0,
    files: [{ id, key: "/podcasts/audio/" + encodeURIComponent(id), mime: doc.mime, size: 0, chunks: 1, have: 0, etag: "", open: true }],
  };
  writeJSON(INDEX_KEY, index);
  emit(id);
  if (navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
  pump();
}

async function start(bookId) {
  if (bookId.startsWith("pe_")) return startEpisode(bookId);
  if (!supported) return;
  const known = readIndex().books[bookId];
  if (known && known.state !== "failed") return;
  if (known) await remove(bookId);
  // The text half: null for a book with no text, or when this person may not download it.
  const text = await textModule()
    .then((m) => m.plan(bookId))
    .catch(() => null);
  let detail = null;
  try {
    const res = await fetch("/api/v1/books/" + encodeURIComponent(bookId), { headers: { Accept: "application/json" } });
    if (res.ok) detail = await res.json();
  } catch {
    // a text book needs no detail; an audio book stops below
  }
  const probe = document.createElement("audio");
  const files =
    detail && detail.permissions && detail.permissions.download
      ? detail.files
          .filter((f) => f.playable)
          .map((f) => ({
            id: f.id,
            // A file this browser cannot play as it is takes the compatible version.
            key: "/stream/" + f.id + (f.needsCompat && probe.canPlayType(f.mime) === "" ? "?compat=1" : ""),
            mime: f.mime,
            size: f.size,
            chunks: Math.max(1, Math.ceil(f.size / CHUNK)),
            have: 0,
            etag: "",
          }))
      : [];
  if (!files.length && !text) return;
  const index = readIndex();
  index.books[bookId] = {
    title: detail ? detail.book.title : text.title,
    author: detail ? (detail.book.authors || []).map((p) => p.name).join(", ") : text.author,
    cover: files.length ? "" : text.cover,
    bytes: files.reduce((sum, f) => sum + f.size, 0) + (text ? text.bytes : 0),
    done: 0,
    state: "waiting",
    error: "",
    addedAt: Date.now(),
    readyAt: 0,
    finishedAt: 0,
    files,
  };
  if (text) {
    index.books[bookId].text = {
      editionId: text.editionId,
      revision: text.revision,
      parts: text.parts,
      images: text.images,
      shell: "/books/" + bookId + "/read",
      bytes: text.bytes,
      have: 0,
    };
  }
  writeJSON(INDEX_KEY, index);
  emit(bookId);
  if (navigator.storage && navigator.storage.persist) navigator.storage.persist().catch(() => {});
  pump();
}

function pause(bookId) {
  const controller = controllers.get(bookId);
  if (controller) controller.abort();
  patch(bookId, (r) => {
    if (r.state !== "ready") r.state = "paused";
    r.net = false;
  });
}

function resume(bookId) {
  const record = patch(bookId, (r) => {
    if (r.state === "ready") return;
    r.state = "waiting";
    r.error = "";
    r.net = false;
  });
  if (record) pump();
}

// Cancel and remove are the same: stop, delete the files, then the record.
async function remove(bookId) {
  const controller = controllers.get(bookId);
  if (controller) controller.abort();
  const index = readIndex();
  const record = index.books[bookId];
  if (!record) return;
  record.id = bookId;
  delete index.books[bookId];
  writeJSON(INDEX_KEY, index);
  emit(bookId);
  // Wait for the download to let go before its files are deleted, so no chunk lands afterwards.
  if (controller) await controller.done;
  try {
    await purge(record);
  } catch {
    // the cache is optional
  }
  reseed();
}
const cancel = remove;

// ---- The queue ----

function nextBook() {
  return list().find((r) => r.state === "waiting" || r.state === "downloading");
}

// One book at a time, in the order they were added, behind a lock so two tabs do not both download.
function pump() {
  if (running || !supported) return;
  running = true;
  const work = async () => {
    for (let record = nextBook(); record; record = nextBook()) await runBook(record.id);
    running = false;
  };
  if (navigator.locks) navigator.locks.request("ts-downloads", work);
  else work();
}

function resumeAll(onlyNetwork) {
  let any = false;
  const index = readIndex();
  for (const [id, record] of Object.entries(index.books)) {
    const waiting = record.state === "waiting" || record.state === "downloading";
    if (controllers.has(id)) continue;
    if (waiting || (record.state === "paused" && (record.net || !onlyNetwork))) {
      record.state = "waiting";
      any = true;
    }
  }
  if (any) {
    writeJSON(INDEX_KEY, index);
    emit("");
    pump();
  }
}

function sleep(ms, signal) {
  return new Promise((done, fail) => {
    const timer = setTimeout(done, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        fail(new DOMException("aborted", "AbortError"));
      },
      { once: true },
    );
  });
}

async function runBook(id) {
  const controller = new AbortController();
  let release;
  controller.done = new Promise((done) => (release = done));
  controllers.set(id, controller);
  try {
    const record = patch(id, (r) => {
      r.state = "downloading";
      r.error = "";
      r.net = false;
    });
    if (!record) return;
    record.id = id;
    const room = navigator.storage && navigator.storage.estimate ? await navigator.storage.estimate() : null;
    if (room && room.quota - room.usage < record.bytes - record.done + 16 * MiB) throw new Halt("failed", "space");
    if (record.kind === EPISODE) await keepEpisodePage(record);
    else if (record.files.length) await keepPage(record);
    tellWorker();
    for (let i = 0; i < record.files.length; i++) await runFile(id, i, controller.signal);
    if (record.text) await (await textModule()).run(id, record, controller.signal, kit);
    patch(id, (r) => {
      r.state = "ready";
      r.error = "";
      r.done = r.bytes;
      r.readyAt = Date.now();
    });
    say("hint", "toast-ready", record);
    reseed();
  } catch (e) {
    await settle(id, e, controller.signal);
  } finally {
    controllers.delete(id);
    release();
  }
}

// How a download that stopped early is recorded: pause and cancel already set their own state.
async function settle(id, e, signal) {
  if (signal.aborted || (e && e.name === "AbortError")) return;
  let state = "failed";
  let error = "failed";
  let net = false;
  if (e instanceof Halt) {
    state = e.state;
    error = e.error;
  } else if (e && e.name === "QuotaExceededError") {
    error = "space";
  } else if (e instanceof TypeError) {
    // The network went away: try again when it is back.
    state = "paused";
    error = "";
    net = true;
  }
  const record = readIndex().books[id];
  if (!record) return;
  record.id = id;
  if (state === "failed" && error === "space") await purge(record).catch(() => {});
  patch(id, (r) => {
    r.state = state;
    r.error = error;
    r.net = net;
    if (state === "failed" && error === "space") {
      r.done = 0;
      if (r.text) r.text.have = 0;
      for (const file of r.files) {
        file.have = 0;
        file.etag = "";
      }
    }
  });
  if (state === "failed") say("err", "error-" + error, record);
}

// The fragment the player opens offline, the cover and the peaks. The peaks are best effort.
async function keepPage(record) {
  const cache = await openCache();
  const res = await fetch("/hx/player/" + record.id, { cache: "no-store" });
  if (!res.ok || res.redirected) throw new Halt("failed", "gone");
  const html = await res.text();
  await put(cache, "/hx/player/" + record.id, new Response(html, { headers: { "Content-Type": "text/html" } }));
  const cover = new DOMParser().parseFromString(html, "text/html").getElementById("player-data").dataset.coverUrl;
  if (cover) {
    try {
      const image = await fetch(cover);
      if (image.ok) await put(cache, cover, image);
    } catch {
      // the cover is optional
    }
    patch(record.id, (r) => {
      r.cover = cover;
    });
  }
  for (const file of record.files) {
    try {
      const peaks = await fetch("/peaks/" + file.id);
      if (peaks.ok) await cache.put("/peaks/" + file.id, peaks);
    } catch {
      // the peaks are optional
    }
  }
}

// The episode's player fragment, the show's picture and the chapters file when the fragment names one. The
// picture and the chapters are best effort.
async function keepEpisodePage(record) {
  const cache = await openCache();
  const page = "/hx/player/episode/" + encodeURIComponent(record.id);
  const res = await fetch(page, { cache: "no-store" });
  if (!res.ok || res.redirected) throw new Halt("failed", "gone");
  const html = await res.text();
  await put(cache, page, new Response(html, { headers: { "Content-Type": "text/html" } }));
  const data = new DOMParser().parseFromString(html, "text/html").getElementById("player-data").dataset;
  if (data.coverUrl) {
    try {
      const image = await fetch(data.coverUrl);
      if (image.ok) await put(cache, data.coverUrl, image);
      patch(record.id, (r) => {
        r.cover = data.coverUrl;
      });
    } catch {
      // the picture is optional
    }
  }
  if (data.chaptersHref) {
    try {
      const chapters = await fetch(data.chaptersHref);
      if (chapters.ok) await put(cache, data.chaptersHref, chapters);
    } catch {
      // the chapters are optional
    }
  }
}

// One file, one chunk request at a time. The completion entry is written last: only then does the
// worker serve the file.
async function runFile(id, i, signal) {
  const cache = await openCache();
  const file = readIndex().books[id].files[i];
  if ((await cache.keys(file.key)).length) {
    file.have = file.chunks;
    return;
  }
  file.have = await countChunks(cache, file);
  let restarted = false;
  let type = file.mime;
  while (file.have < file.chunks) {
    const from = file.have * CHUNK;
    const res = await fetchChunk(id, file, from, from + CHUNK - 1, signal);
    if (!res) {
      // The file changed on the server: start it again once.
      if (restarted) throw new Halt("failed", "failed");
      restarted = true;
      await dropFile(cache, file);
      continue;
    }
    type = res.headers.get("Content-Type") || type;
    file.etag = file.etag || res.headers.get("ETag") || "";
    const body = await res.arrayBuffer();
    if (file.open && res.status === 200) {
      // The publisher ignored Range: this is the whole file, kept as one chunk.
      file.size = body.byteLength;
      file.chunks = 1;
    }
    await put(cache, chunkKey(file, file.have), new Response(body, { headers: { "Content-Type": type } }));
    file.have++;
    file.mime = type;
    const seconds = (performance.now() - (file.at || performance.now())) / 1000;
    if (seconds > 0) rate = rate ? rate * 0.7 + (body.byteLength / seconds) * 0.3 : body.byteLength / seconds;
    file.at = performance.now();
    save(id, i, file);
  }
  await put(
    cache,
    file.key,
    new Response(null, {
      headers: {
        "Content-Type": type,
        "X-Ts-Size": String(file.size),
        "X-Ts-Chunk": String(CHUNK),
        "X-Ts-Chunks": String(file.chunks),
        ETag: file.etag,
      },
    }),
  );
  tellWorker();
  save(id, i, file);
}

function save(id, i, file) {
  const kept = { ...file };
  delete kept.at;
  const record = patch(id, (r) => {
    r.files[i] = kept;
    r.note = "";
    r.bytes = r.files.reduce((sum, f) => sum + f.size, 0) + (r.text ? r.text.bytes : 0);
    r.done = doneOf(r);
  });
  if (!record) throw new DOMException("removed", "AbortError");
}

// One chunk, with the waits the plan sets. Returns the response to store, or null when the file
// changed (a whole body for a later chunk, 416, or another total).
async function fetchChunk(id, file, from, to, signal) {
  const started = Date.now();
  let failures = 0;
  for (;;) {
    const headers = { Range: "bytes=" + from + "-" + to };
    if (file.etag && from > 0) headers["If-Range"] = file.etag;
    const res = await fetch(file.key + (file.key.includes("?") ? "&" : "?") + "download=1", { headers, cache: "no-store", signal });
    if (res.status === 206) {
      const total = Number((res.headers.get("Content-Range") || "").split("/")[1]);
      if (file.open && from === 0 && total > 0) {
        // An episode's size is known only from its first answer.
        file.size = total;
        file.chunks = Math.max(1, Math.ceil(total / CHUNK));
        return res;
      }
      if (file.key.includes("compat") && from === 0) {
        // A compatible copy has its own size, known only from its first answer.
        file.size = total;
        file.chunks = Math.max(1, Math.ceil(total / CHUNK));
        return res;
      }
      return total === file.size ? res : null;
    }
    if (res.status === 200) return from === 0 && (file.open || file.size <= CHUNK) ? res : null;
    if (res.status === 416) return null;
    if (res.status === 403 || res.status === 404) throw new Halt("failed", "gone");
    if (res.status === 503) {
      // A compatible version is being made.
      if (Date.now() - started > 600000) throw new Halt("failed", "failed");
      patch(id, (r) => {
        r.note = "preparing";
      });
      await sleep((Number(res.headers.get("Retry-After")) || 3) * 1000, signal);
      continue;
    }
    if (res.status >= 500 && failures < 3) {
      await sleep([5000, 15000, 60000][failures++], signal);
      continue;
    }
    throw new Halt(res.status >= 500 ? "paused" : "failed", "failed");
  }
}

// ---- The shell and finished books ----

// Asks for the offline shell with the ready books in it and keeps it for the worker.
async function reseed() {
  if (!supported || !navigator.onLine) return;
  try {
    const all = list().filter((r) => r.state === "ready");
    const ready = all.filter((r) => r.kind !== EPISODE).slice(0, 200);
    const episodes = all.filter((r) => r.kind === EPISODE).slice(0, 200);
    const ids = (records) => records.map((r) => encodeURIComponent(r.id)).join(",");
    const res = await fetch("/offline?books=" + ids(ready) + "&text=" + ids(ready.filter((r) => r.text)) + "&episodes=" + ids(episodes));
    if (res.ok && !res.redirected) await (await openCache()).put("/offline", res);
  } catch {
    // the shell is optional
  }
}

function prefs() {
  return readJSON(PREFS_KEY, { removeFinished: false });
}

// Notes when each ready book was finished and, when the setting is on, removes the ones finished
// more than 7 days ago. At most once a day unless asked.
async function checkFinished(force) {
  if (!supported || !navigator.onLine) return;
  if (!force && Date.now() - Number(localStorage.getItem(CHECKED_KEY)) < DAY) return;
  localStorage.setItem(CHECKED_KEY, String(Date.now()));
  for (const record of list().filter((r) => r.state === "ready")) {
    try {
      if (record.kind === EPISODE) {
        const res = await fetch("/api/v1/podcasts/episodes/" + encodeURIComponent(record.id), { headers: { Accept: "application/json" } });
        if (!res.ok) continue;
        const state = (await res.json()).state;
        const playedAt = state && state.played ? state.updatedAt : 0;
        patch(record.id, (r) => {
          r.finishedAt = playedAt;
        });
        if (prefs().removeFinished && playedAt && playedAt < Date.now() - 7 * DAY) await remove(record.id);
        continue;
      }
      let finishedAt = -1;
      const text = await textModule()
        .then((m) => m.refresh(record.id, record, kit))
        .catch(() => "keep");
      if (text === "gone") {
        await dropText(record);
        if (!record.files.length) continue;
      } else if (!record.files.length) {
        // A book with only text takes its finished date from the manifest.
        if (!text || text === "keep") continue;
        finishedAt = text.finishedAt;
      }
      if (finishedAt < 0) {
        const res = await fetch("/api/v1/progress/" + encodeURIComponent(record.id), { headers: { Accept: "application/json" } });
        if (!res.ok) continue;
        const p = (await res.json()).progress;
        finishedAt = p && p.finished ? p.finishedAt : 0;
      }
      patch(record.id, (r) => {
        r.finishedAt = finishedAt;
      });
      if (prefs().removeFinished && finishedAt && finishedAt < Date.now() - 7 * DAY) await remove(record.id);
    } catch {
      return;
    }
  }
}

// The right to the text is gone: its copy goes, and with it the record of a book that had only text.
async function dropText(record) {
  const textual = await textModule().catch(() => null);
  if (textual) await textual.purge(await openCache(), record);
  if (!record.files.length) return remove(record.id);
  patch(record.id, (r) => {
    delete r.text;
    r.bytes = r.files.reduce((sum, f) => sum + f.size, 0);
    r.done = r.bytes;
  });
}

// ---- The book page's Download control ----

function renderBookControls() {
  const books = readIndex().books;
  for (const button of document.querySelectorAll("[data-ts-download]")) {
    const record = books[button.dataset.tsDownload];
    const link = button.parentElement.querySelector("[data-ts-download-status]");
    const label = button.querySelector(".ts-book__label");
    button.dataset.tsLabel = button.dataset.tsLabel || label.textContent;
    const busy = record && (record.state === "waiting" || record.state === "downloading");
    const settled = record && !busy;
    button.hidden = !supported || !!settled;
    button.disabled = !!busy;
    label.textContent = busy
      ? fill(record.state === "waiting" ? button.dataset.tsMsgWaiting : button.dataset.tsMsgDownloading, { pct: percent(record) })
      : button.dataset.tsLabel;
    if (link) {
      link.hidden = !settled;
      if (settled) link.querySelector("[data-ts-download-label]").textContent = link.dataset["tsMsg" + record.state[0].toUpperCase() + record.state.slice(1)] || "";
    }
  }
}

function percent(record) {
  return record.bytes ? Math.min(100, Math.floor((record.done / record.bytes) * 100)) : 0;
}

// ---- The Downloads page ----

let installPrompt = null;
let storageTimer = 0;

function setBar(bar, value) {
  bar.querySelector(".ts-bar__fill").style.setProperty("--ts-value", String(Math.max(0, Math.min(100, value))));
}

// The words under a row's title.
function metaFor(record) {
  const vars = { title: record.title, size: size(record.bytes), done: size(record.done), total: size(record.bytes) };
  const text = (key, extra) => words(key, { ...vars, ...extra });
  if (record.state === "ready") {
    const days = record.finishedAt ? Math.floor((Date.now() - record.finishedAt) / DAY) : -1;
    return text("ready") + (days >= 0 ? " · " + text("finished", { n: days }) : "");
  }
  if (record.state === "failed") return text("error-" + (record.error || "failed"));
  if (record.state === "paused") return text("paused-progress");
  if (record.state === "waiting") return text(!navigator.onLine || record.net ? "waiting-net" : "waiting-size");
  if (record.note === "preparing") return text("preparing");
  const minutes = rate ? Math.max(1, Math.ceil((record.bytes - record.done) / rate / 60)) : 0;
  return text("progress") + (minutes ? " · " + text("eta", { n: minutes }) : "");
}

function renderRow(record, template, listEl) {
  let row = listEl.querySelector('[data-id="' + record.id + '"]');
  if (!row) {
    row = template.content.firstElementChild.cloneNode(true);
    row.dataset.id = record.id;
    const cover = row.querySelector("[data-ts-cover]");
    if (record.cover) {
      const img = cover.appendChild(document.createElement("img"));
      img.className = "ts-cover__img";
      img.alt = "";
      img.src = record.cover;
      cover.classList.remove("ts-cover--blank");
    }
    row.querySelector("[data-ts-title]").textContent = record.title;
    const link = row.querySelector("[data-ts-link]");
    if (link) {
      link.href =
        record.kind === EPISODE
          ? "/podcasts/episodes/" + encodeURIComponent(record.id)
          : "/books/" + encodeURIComponent(record.id) + (record.files.length ? "" : "/read");
    }
  }
  row.querySelector("[data-ts-meta]").textContent = metaFor(record);
  const bar = row.querySelector("[data-ts-row-bar]");
  if (bar) {
    bar.setAttribute("aria-valuenow", String(percent(record)));
    bar.setAttribute("aria-label", words("downloading", { title: record.title }));
    setBar(bar, percent(record));
  }
  const showing = { pause: record.state === "downloading" || record.state === "waiting", resume: record.state === "paused" || record.state === "failed", cancel: record.state !== "ready", remove: true };
  for (const button of row.querySelectorAll("[data-ts-act]")) {
    const act = button.dataset.tsAct;
    button.hidden = !showing[act];
    const key = act === "resume" && record.state === "failed" ? "retry" : act;
    button.setAttribute("aria-label", words(key, { title: record.title }));
  }
  if (row.parentElement !== listEl) listEl.append(row);
}

function renderPage() {
  const page = document.querySelector("[data-ts-downloads]");
  if (!page) return;
  const show = (selector, on) => {
    const el = page.querySelector(selector);
    if (el) el.hidden = !on;
    return el;
  };
  loadMessages();
  const books = list();
  show("[data-ts-insecure]", !supported);
  const settings = show("[data-ts-settings]", supported);
  if (settings) settings.removeAttribute("data-ts-pending");
  show("[data-ts-empty]", !books.length);
  show("[data-ts-install]", !!installPrompt);
  const active = books.filter((r) => r.state !== "ready");
  const ready = books.filter((r) => r.state === "ready");
  for (const [name, group] of [["active", active], ["ready", ready]]) {
    const section = show("[data-ts-" + name + "]", group.length);
    const listEl = section.querySelector("ul");
    const wanted = new Set(group.map((r) => r.id));
    for (const row of listEl.children) if (!wanted.has(row.dataset.id)) row.remove();
    for (const record of group) renderRow(record, page.querySelector("template[data-ts-row-" + name + "]"), listEl);
    section.querySelector("[data-ts-" + name + "-count]").textContent = String(group.length);
  }
  page.querySelector("[data-ts-ready-size]").textContent = size(ready.reduce((sum, r) => sum + r.bytes, 0));
  show("[data-ts-keep-open]", active.some((r) => r.state === "downloading" || r.state === "waiting"));
  page.querySelector("[data-ts-pref]").setAttribute("aria-checked", String(!!prefs().removeFinished));
  clearTimeout(storageTimer);
  storageTimer = setTimeout(renderStorage, 500);
}

// Storage on this phone: Tendshelf's share and what is free, from the browser's own estimate.
async function renderStorage() {
  const page = document.querySelector("[data-ts-downloads]");
  const section = page && page.querySelector("[data-ts-storage]");
  if (!section) return;
  section.removeAttribute("data-ts-pending");
  if (!supported || !navigator.storage || !navigator.storage.estimate) {
    section.hidden = true;
    return;
  }
  const { usage = 0, quota = 0 } = await navigator.storage.estimate();
  const vars = { used: size(usage), free: size(Math.max(0, quota - usage)) };
  section.hidden = false;
  section.querySelector("[data-ts-storage-free]").textContent = words("free", { size: vars.free });
  section.querySelector("[data-ts-storage-used]").textContent = words("used", { size: vars.used });
  const bar = section.querySelector("[data-ts-storage-bar]");
  bar.setAttribute("aria-label", words("bar", vars));
  setBar(bar, quota ? (usage / quota) * 100 : 0);
  const kept = navigator.storage.persisted ? await navigator.storage.persisted() : true;
  section.querySelector("[data-ts-persist-note]").hidden = kept;
  const ios = /iP(hone|ad|od)/.test(navigator.userAgent) && !navigator.standalone && !matchMedia("(display-mode: standalone)").matches;
  section.querySelector("[data-ts-safari-note]").hidden = !ios;
}

// "Download all" (downloads-all.js, loaded when a page has the control): this browser's driver is the queue above.
let allLoad = null;

function renderAll() {
  const banner = document.getElementById("ts-offline");
  const src = banner && banner.dataset.tsDownloadsAllSrc;
  if (!supported || !src || allLoad || !document.querySelector("[data-ts-download-all]")) return;
  allLoad = import(src).then((m) => m.mount(allDriver));
}

function stateOf(record) {
  if (!record) return "none";
  return record.state === "ready" || record.state === "failed" ? record.state : "pending";
}

const allDriver = {
  usable: () => supported,
  // A book that failed is not held: asking again retries it.
  have: async (ids) => new Set(ids.filter((id) => stateOf(readIndex().books[id]) !== "none" && stateOf(readIndex().books[id]) !== "failed")),
  async room(bytes) {
    const e = navigator.storage && navigator.storage.estimate ? await navigator.storage.estimate() : null;
    const free = e ? Math.max(0, e.quota - e.usage) : Infinity;
    return { ok: free >= bytes + 16 * MiB, free };
  },
  add: (id) => start(id),
  stop: (ids) => ids.forEach((id) => remove(id)),
  states: (ids) => new Map(ids.map((id) => [id, stateOf(readIndex().books[id])])),
  onChange: (callback) => document.addEventListener("ts:downloads:change", callback),
  quiet: (on) => {
    quiet = on;
  },
};

function render() {
  renderBookControls();
  renderPage();
  renderAll();
}

// ---- Wiring ----

document.addEventListener("click", (event) => {
  const target = event.target.closest("[data-ts-download], [data-ts-act], [data-ts-pref], [data-ts-install]");
  if (!target) return;
  if (target.matches("[data-ts-download]")) {
    start(target.dataset.tsDownload);
  } else if (target.matches("[data-ts-pref]")) {
    const next = { ...prefs(), removeFinished: !prefs().removeFinished };
    writeJSON(PREFS_KEY, next);
    render();
    if (next.removeFinished) checkFinished(true);
  } else if (target.matches("[data-ts-install]")) {
    if (installPrompt) installPrompt.prompt();
  } else {
    const id = target.closest("[data-id]").dataset.id;
    const act = target.dataset.tsAct;
    if (act === "pause") pause(id);
    else if (act === "resume") resume(id);
    else remove(id);
  }
});

window.addEventListener("beforeinstallprompt", (event) => {
  event.preventDefault();
  installPrompt = event;
  render();
});
window.addEventListener("online", () => resumeAll(true));
window.addEventListener("offline", render);
window.addEventListener("storage", (event) => {
  if (event.key === INDEX_KEY) render();
});
document.addEventListener("ts:downloads:change", render);
document.addEventListener("htmx:after:swap", render);
if (navigator.serviceWorker) navigator.serviceWorker.addEventListener("controllerchange", reseed);

window.tsDownloads = { start, pause, resume, cancel, remove, list, debug, check: checkFinished };

render();
if (supported) {
  resumeAll(true);
  if (list().some((r) => r.state === "ready")) setTimeout(reseed, 3000);
  checkFinished(false);
}
