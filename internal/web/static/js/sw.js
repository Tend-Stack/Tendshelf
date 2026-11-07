// Tendshelf service worker (decision 0012). It answers only what is listed below; everything else
// goes to the network untouched. The server puts `self.TS_BUILD = {build, assets};` in front of
// this file, so the file itself stays the same for every build. Decision 0019 adds the reader: a
// downloaded book's chapter parts and pictures, and its reader shell for a navigation that cannot
// reach the server. The worker stores nothing itself; the download does.
const STATIC = "ts-static-" + self.TS_BUILD.build;
const PERSON_PREFIX = "ts-person-";
// The reader page of a book. Its shell is stored under this path (and with ?sheet=contents).
const READER = /^\/books\/[^/]+\/read$/;

// Paths the worker never touches: share links, invitations, sign-in links, the API, sign-in and sign-out, the
// health check and the worker itself.
const PASS = ["/s/", "/invite/", "/signin/", "/api/", "/login", "/setup", "/logout", "/healthz", "/sw.js"];

self.addEventListener("install", (event) => {
  // No skipWaiting: a new worker waits until the page says nothing is playing.
  event.waitUntil(caches.open(STATIC).then((cache) => cache.addAll(self.TS_BUILD.assets)));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      for (const name of await caches.keys()) {
        if (name.startsWith("ts-static-") && name !== STATIC) await caches.delete(name);
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener("message", (event) => {
  const type = event.data && event.data.type;
  if (type === "activate") self.skipWaiting();
  // The download manager and the sign-out say the person cache changed: look again.
  if (type === "stored") event.waitUntil(refreshStreams());
  // Tests and diagnosis: what the worker believes is stored, once it has looked.
  if (type === "streams" && event.ports[0]) {
    event.waitUntil(
      refreshStreams().then(() => event.ports[0].postMessage(streamsKnown ? [...held] : null)),
    );
  }
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  if (request.method !== "GET" && request.method !== "HEAD") return;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;
  if (url.searchParams.has("download")) return;
  if (PASS.some((prefix) => url.pathname.startsWith(prefix))) return;

  if (url.pathname.startsWith("/static/")) {
    event.respondWith(fromStatic(request, url));
  } else if (url.pathname.startsWith("/stream/")) {
    // respondWith is decided here, at once. A book that is not stored goes to the server directly:
    // answering it through fetch() here left cancelled Range requests open in Firefox until the
    // site hung. Until the set of stored books is known, the worker answers as before, so a stored
    // book is never refused.
    if (streamsKnown && !held.has(streamKey(url))) return;
    event.respondWith(fromChunks(request, url));
  } else if (url.pathname.startsWith("/covers/") || url.pathname.startsWith("/peaks/")) {
    // The same for what is on screen while a book plays: only a stored copy is the worker's to answer.
    if (streamsKnown && !held.has(url.pathname + url.search)) return;
    event.respondWith(cachedOrNetwork(request, url));
  } else if (/^\/hx\/player\/[^/]+$/.test(url.pathname)) {
    if (streamsKnown && !held.has(url.pathname)) return;
    event.respondWith(freshOrCached(request, url));
  } else if (/^\/hx\/books\/[^/]+\/read\/chapters\/\d+$/.test(url.pathname)) {
    event.respondWith(freshOrCached(request, url, url.pathname + url.search));
  } else if (/^\/books\/[^/]+\/read\/images\/[^/]+$/.test(url.pathname)) {
    event.respondWith(cachedOrNetwork(request, url));
  } else if (
    request.mode === "navigate" ||
    request.headers.get("HX-Boosted") === "true" ||
    (READER.test(url.pathname) && url.searchParams.get("sheet") === "contents")
  ) {
    event.respondWith(pageOrShell(request, url));
  }
});

// How long a page or boosted navigation waits for the network before the offline shell answers.
// With a VPN (Tailscale) up on a phone in airplane mode, requests hang instead of failing, so
// without a limit the app would never leave the splash screen. 4 s is longer than a slow mobile
// round trip yet short enough to feel like a failure, not a freeze.
const PAGE_DEADLINE_MS = 4000;
// The player fragment and a stored chapter part are small and the stored copy is as good, so they give up sooner.
const PLAYER_DEADLINE_MS = 3000;

// fetch() that gives up after `ms`. A timeout rejects like a network error, so callers fall back
// exactly as they already do when the network is gone.
async function timedFetch(request, ms) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), ms);
  try {
    return await fetch(request, { signal: controller.signal });
  } finally {
    clearTimeout(timer);
  }
}

// The signed-in person's cache: the one ts-person-* cache there is.
async function personCache() {
  for (const name of await caches.keys()) {
    if (name.startsWith(PERSON_PREFIX)) return caches.open(name);
  }
  return null;
}

// What the person cache holds for a URL, or undefined.
async function stored(url) {
  const cache = await personCache();
  return cache ? cache.match(url) : undefined;
}

// ---- /static/: cache-first ----

// The file name without its content hash: app.0123456789.css becomes app.css.
function unhashed(path) {
  return path.replace(/\.[0-9a-f]{10}\./, ".");
}

async function fromStatic(request, url) {
  const cache = await caches.open(STATIC);
  const hit = await cache.match(url.pathname);
  if (hit) return hit;
  try {
    return await fetch(request);
  } catch (error) {
    // An older page asks for an asset of an older build: the current asset of the same name will do.
    const wanted = unhashed(url.pathname);
    for (const key of await cache.keys()) {
      const path = new URL(key.url).pathname;
      if (unhashed(path) === wanted) return cache.match(path);
    }
    throw error;
  }
}

// ---- Covers and peaks: only what a download stored ----

async function cachedOrNetwork(request, url) {
  const hit = await stored(url.pathname + url.search);
  return hit || fetch(request);
}

// ---- The book fragment: network-first once it has been stored ----

// `key` is where the copy is stored: the path, or for a chapter part the path with its query.
async function freshOrCached(request, url, key = url.pathname) {
  const cache = await personCache();
  const cached = cache ? await cache.match(key) : undefined;
  if (!cached) return fetch(request);
  // Known offline: do not wait for a network that cannot answer.
  if (self.navigator.onLine === false) return cached;
  try {
    const fresh = await timedFetch(request, PLAYER_DEADLINE_MS);
    if (fresh.status === 200 && !fresh.redirected) await cache.put(key, fresh.clone());
    return fresh;
  } catch {
    return cached;
  }
}

// ---- Pages: the network, and the offline shell when it cannot answer ----

async function pageOrShell(request, url) {
  // Known offline: do not wait for a network that cannot answer.
  if (self.navigator.onLine === false) return (await shell(url)) || Response.error();
  // The request is not given a timeout of its own: whether it may be given up on depends on whether
  // a shell is stored, and that is only known when the deadline passes.
  const controller = new AbortController();
  const network = fetch(request, { signal: controller.signal });
  let timer;
  const deadline = new Promise((resolve) => {
    timer = setTimeout(resolve, PAGE_DEADLINE_MS, null);
  });
  try {
    let response = await Promise.race([network, deadline]);
    if (response === null) {
      // Too slow. A stored shell answers and the request is dropped. With no shell the person has
      // nothing else to see, so a slow server is waited for; only a failed request is an error.
      const saved = await shell(url);
      if (saved) {
        controller.abort();
        network.catch(() => {});
        return saved;
      }
      response = await network;
    }
    if (response.status !== 502 && response.status !== 504) return response;
    return (await shell(url)) || response;
  } catch {
    return (await shell(url)) || Response.error();
  } finally {
    clearTimeout(timer);
  }
}

// A book's reader shell for a reader address (the contents sheet has its own copy), else /offline.
async function shell(url) {
  const reader = READER.test(url.pathname);
  const key = url.pathname + (url.searchParams.get("sheet") === "contents" ? "?sheet=contents" : "");
  const saved = (reader && (await stored(key))) || (await stored("/offline"));
  if (!saved) return null;
  const headers = new Headers(saved.headers);
  headers.set("X-Ts-Offline", "1");
  return new Response(saved.body, { status: 200, headers });
}

// ---- /stream/{id}: answered from stored chunks ----

// Range: bytes=a-b, a- or -n. Anything else (several ranges, none) is the whole file.
function parseRange(header, size) {
  const m = /^bytes=(\d*)-(\d*)$/.exec(header || "");
  if (!m || (m[1] === "" && m[2] === "")) return { start: 0, end: size - 1, whole: true };
  if (m[1] === "") return { start: Math.max(0, size - Number(m[2])), end: size - 1 };
  return { start: Number(m[1]), end: m[2] === "" ? size - 1 : Math.min(Number(m[2]), size - 1) };
}

// Where a file's completion entry is stored: the path, with ?compat=1 for the compatible copy.
function streamKey(url) {
  return url.pathname + (url.searchParams.get("compat") === "1" ? "?compat=1" : "");
}

// What the person cache holds that the worker may answer, as stream keys and as path plus query
// (stream chunks, with chunk=n, are not listed). Held in memory because the fetch event must decide synchronously. It is filled when the
// worker starts (a stopped worker loses it) and again whenever the page says the cache changed.
const held = new Set();
let streamsKnown = false;
let looking = null;
let lookAgain = false;

function refreshStreams() {
  if (looking) {
    lookAgain = true;
    return looking;
  }
  looking = (async () => {
    try {
      do {
        lookAgain = false;
        const cache = await personCache();
        const found = new Set();
        if (cache) {
          for (const req of await cache.keys()) {
            const u = new URL(req.url);
            if (u.searchParams.has("chunk")) continue;
            found.add(u.pathname.startsWith("/stream/") ? streamKey(u) : u.pathname + u.search);
          }
        }
        held.clear();
        for (const key of found) held.add(key);
        streamsKnown = true;
      } while (lookAgain);
    } catch {
      // Unreadable: answer every stream as before.
      streamsKnown = false;
    } finally {
      looking = null;
    }
  })();
  return looking;
}

refreshStreams();

async function fromChunks(request, url) {
  const key = streamKey(url);
  const cache = await personCache();
  const done = cache ? await cache.match(key) : undefined;
  if (!done) return fetch(request);

  const size = Number(done.headers.get("X-Ts-Size"));
  const chunk = Number(done.headers.get("X-Ts-Chunk"));
  const type = done.headers.get("Content-Type") || "application/octet-stream";
  const chunkKey = (n) => key + (key.includes("?") ? "&" : "?") + "chunk=" + n;

  let { start, end, whole } = parseRange(request.headers.get("Range"), size);
  if (start >= size) {
    return new Response(null, { status: 416, headers: { "Content-Range": "bytes */" + size } });
  }
  if (end < start) {
    ({ start, end, whole } = { start: 0, end: size - 1, whole: true });
  }

  const headers = {
    "Content-Type": type,
    "Content-Length": String(end - start + 1),
    "Accept-Ranges": "bytes",
    "Cache-Control": "no-store",
  };
  if (!whole) headers["Content-Range"] = "bytes " + start + "-" + end + "/" + size;
  const status = whole ? 200 : 206;
  if (request.method === "HEAD") return new Response(null, { status, headers });

  // One chunk is read per pull, so playing the start of a long file does not load all of it.
  let index = Math.floor(start / chunk);
  const last = Math.floor(end / chunk);
  const body = new ReadableStream({
    async pull(controller) {
      const saved = await cache.match(chunkKey(index));
      if (!saved) return controller.error(new Error("chunk " + index + " is missing"));
      const bytes = new Uint8Array(await saved.arrayBuffer());
      const from = index === Math.floor(start / chunk) ? start - index * chunk : 0;
      const to = index === last ? end - index * chunk + 1 : bytes.length;
      controller.enqueue(bytes.subarray(from, to));
      index += 1;
      if (index > last) controller.close();
    },
  });
  return new Response(body, { status, headers });
}
