// The text half of a download (decision 0019, package G6b). downloads.js loads this module when a book
// may have text: it asks the manifest what to store, stores every chapter part and picture under the exact
// address the reader asks for, then the reader shell last, so a half-stored book never opens. It also
// removes that copy and keeps it honest against the manifest's revision.
const READING_KEY = "ts.offline.reading";
const WAITS = [5000, 15000, 60000];

function manifestUrl(id) {
  return "/offline/books/" + encodeURIComponent(id) + "/manifest";
}

// The two shells the worker serves: the reader, and the reader with its contents sheet. Each is fetched from
// /offline/books/{id}/read and kept under the address the person opens.
function shells(text) {
  return [text.shell + "?sheet=contents", text.shell];
}

// The address that renders a shell for this person.
function offlineAddress(shell) {
  return "/offline" + shell;
}

// What the manifest says, or null when the book has no text for this person (404) or the answer is a
// redirect to the sign-in page.
export async function plan(bookId) {
  const res = await fetch(manifestUrl(bookId), { cache: "no-store" });
  return res.ok && !res.redirected ? res.json() : null;
}

// One address. A redirect or 403 means the right is gone; 404 too, except for a picture, which is skipped
// (null). A 5xx waits and tries again three times, then the download pauses.
async function get(url, signal, kit, optional) {
  for (let failures = 0; ; ) {
    const res = await fetch(url, { cache: "no-store", signal });
    if (res.ok && !res.redirected) return res;
    if (res.redirected || res.status === 403) throw new kit.Halt("failed", "gone");
    if (res.status === 404) {
      if (optional) return null;
      throw new kit.Halt("failed", "gone");
    }
    if (res.status < 500) throw new kit.Halt("failed", "failed");
    if (failures === WAITS.length) throw new kit.Halt("paused", "failed");
    await kit.sleep(WAITS[failures++], signal);
  }
}

// Stores one address and returns its size. A copy already stored is kept unless the caller asks for fresh.
async function keep(cache, key, from, signal, kit, { optional = false, fresh = false } = {}) {
  if (!fresh) {
    const have = await cache.match(key);
    if (have) return (await have.blob()).size;
  }
  const res = await get(from, signal, kit, optional);
  if (!res) return 0;
  const copy = res.clone();
  await kit.put(cache, key, res);
  return (await copy.blob()).size;
}

// Stores the text of a book: parts, pictures, then the contents shell and the reader shell. With fresh the
// stored copies are fetched again and the index is left to the caller (a background refresh is silent).
export async function run(id, record, signal, kit, fresh = false) {
  const cache = await kit.openCache();
  const text = record.text;
  const items = [...text.parts.map((u) => [u, false]), ...text.images.map((u) => [u, true])];
  let bytes = 0;
  let have = 0;
  for (const [url, optional] of items) {
    bytes += await keep(cache, url, url, signal, kit, { optional, fresh });
    have++;
    if (fresh) continue;
    const share = (r) => {
      r.text.have = have;
      r.done = kit.done(r);
    };
    if (!kit.patch(id, share)) throw new DOMException("removed", "AbortError");
  }
  if (!record.files.length && record.cover && !fresh) {
    // A book with no audio has no player fragment to name its cover, so the cover is kept here.
    try {
      const image = await fetch(record.cover, { signal });
      if (image.ok) await kit.put(cache, record.cover, image);
    } catch {
      // the cover is optional
    }
  }
  for (const shell of shells(text)) bytes += await keep(cache, shell, offlineAddress(shell), signal, kit, { fresh: true });
  text.bytes = bytes;
  text.have = items.length;
  if (fresh) return;
  kit.patch(id, (r) => {
    r.bytes += bytes - r.text.bytes;
    r.text.bytes = bytes;
  });
}

// Deletes the text of a book: the shell first, so a half-removed book never opens, then the contents sheet,
// the parts and the pictures, and its unsent reading record.
export async function purge(cache, record) {
  const text = record.text;
  if (!text) return;
  for (const key of [...shells(text).reverse(), ...text.parts, ...text.images]) await cache.delete(key);
  try {
    const queue = JSON.parse(localStorage.getItem(READING_KEY)) || {};
    delete queue[record.id];
    localStorage.setItem(READING_KEY, JSON.stringify(queue));
  } catch {
    // the queue is optional
  }
}

// The daily check: "gone" when the manifest says there is no text for this person any more, "keep" when
// nothing could be learned (a redirect, a failed request) or nothing changed, else {finishedAt}. A new
// revision stores everything again under the new addresses with the shell last, then drops the addresses
// the new list no longer has; the book stays ready throughout.
export async function refresh(id, record, kit) {
  let m;
  try {
    const res = await fetch(manifestUrl(id), { cache: "no-store" });
    if (res.redirected) return "keep";
    if (res.status === 404) return "gone";
    if (!res.ok) return "keep";
    m = await res.json();
  } catch {
    return "keep";
  }
  const old = record.text;
  if (old && old.revision === m.revision && old.editionId === m.editionId) return { finishedAt: m.finishedAt };
  const text = { editionId: m.editionId, revision: m.revision, parts: m.parts, images: m.images, shell: "/books/" + id + "/read", bytes: m.bytes, have: 0 };
  try {
    await run(id, { ...record, text }, new AbortController().signal, kit, true);
  } catch {
    return "keep";
  }
  if (old) {
    const keepers = new Set([...text.parts, ...text.images]);
    const cache = await kit.openCache();
    for (const key of [...old.parts, ...old.images]) if (!keepers.has(key)) await cache.delete(key);
  }
  text.have = text.parts.length + text.images.length;
  kit.patch(id, (r) => {
    r.bytes += text.bytes - (old ? old.bytes : 0);
    r.done = r.bytes;
    r.text = text;
  });
  return { finishedAt: m.finishedAt };
}
