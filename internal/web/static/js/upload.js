// Adding books by upload (decision 0015). The shell's [data-ts-module] loader imports this file on the
// Add books page only. The server sends the words (data-ts-msg-* on the root) and the row markup (the
// <template data-ts-row>); this file picks the files, sends them in pieces and moves the rows.
// Exactly one request is in flight at any time: pieces of one file after another, books one by one.

const PROTECTED = ["aax", "aaxc", "aa", "m4p", "kfx", "kfx-zip"];
const MAX_WAIT_MS = 30000;
const AVERAGE_MS = 20000;

class Stop extends Error {}
const PAUSED = new Stop("paused");

function csrf() {
  try {
    return JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] || "";
  } catch {
    return "";
  }
}

function sizeText(n) {
  const units = ["B", "kB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1000 && i < units.length - 1) {
    n /= 1000;
    i++;
  }
  return (i > 0 && n < 9.95 ? n.toFixed(1) : String(Math.round(n))) + " " + units[i];
}

function extension(path) {
  const dot = path.lastIndexOf(".");
  return dot < 0 ? "" : path.slice(dot + 1).toLowerCase();
}

// The hex SHA-256 of one piece. The browser cannot hash a whole multi-GB file without reading it
// into memory (crypto.subtle has no incremental form), so the manifest carries no file checksum.
async function pieceSum(blob) {
  const digest = await crypto.subtle.digest("SHA-256", await blob.arrayBuffer());
  return [...new Uint8Array(digest)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// A JSON request to the upload routes. A refusal throws Stop with the server's own words.
async function call(method, url, body) {
  const headers = { "X-CSRF-Token": csrf() };
  if (body) headers["Content-Type"] = "application/json";
  const res = await fetch(url, { method, headers, body: body && JSON.stringify(body), credentials: "same-origin" });
  const data = res.status === 204 ? {} : await res.json().catch(() => ({}));
  if (!res.ok) throw Object.assign(new Stop((data.error && data.error.message) || ""), { code: data.error && data.error.code });
  return data;
}

export function mount(root) {
  const list = root.querySelector("[data-ts-list]");
  const queue = root.querySelector("[data-ts-queue]");
  const template = root.querySelector("template[data-ts-row]");
  const uploads = new Map(); // row element -> the upload it shows
  let turn = Promise.resolve(); // books are sent one after another

  function say(key, vars) {
    const text = root.getAttribute("data-ts-msg-" + key) || "";
    return text.replace(/\{(\w+)\}/g, (_, name) => vars[name]);
  }

  function duration(ms) {
    const s = Math.max(1, Math.round(ms / 1000));
    if (s < 90) return say("sec", { n: s });
    return s < 5400 ? say("min", { n: Math.round(s / 60) }) : say("hour", { n: Math.round(s / 3600) });
  }

  // ---- rows ----

  function newRow(title, meta) {
    const row = template.content.firstElementChild.cloneNode(true);
    row.querySelector("[data-ts-title]").textContent = title;
    row.querySelector("[data-ts-meta]").textContent = meta;
    queue.hidden = false;
    list.prepend(row);
    return row;
  }

  // The state picks the row's look, its icon and the buttons that apply to it.
  function setState(up, state, line) {
    const row = up.row;
    row.className = "ts-upload__row ts-upload__row--" + state;
    row.querySelector("[data-ts-icons]").className = "ts-upload__icon ts-upload__icon--" + state;
    const icon = state === "failed" ? "warn" : state === "paused" || state === "refused" ? state : "sending";
    for (const svg of row.querySelectorAll("[data-ts-icon]")) svg.hidden = svg.dataset.tsIcon !== icon;
    for (const button of row.querySelectorAll("[data-ts-for]")) {
      button.hidden = !button.dataset.tsFor.split(" ").includes(state) || (button.dataset.tsAct === "cancel" && !up.id);
    }
    if (line !== undefined) row.querySelector("[data-ts-line]").textContent = line;
  }

  function refuse(up, title, line) {
    up.row.querySelector("[data-ts-title]").textContent = say("refused-title", { title });
    setState(up, "refused", line);
    up.row.querySelector("[data-ts-bar]").remove();
  }

  // Progress text changes at most once a second; the time left comes from a 20 second average.
  function progress(up, force) {
    const now = Date.now();
    up.sent = up.files.reduce((sum, f) => sum + f.offset, 0);
    up.samples.push({ at: now, sent: up.sent });
    while (up.samples.length > 1 && now - up.samples[0].at > AVERAGE_MS) up.samples.shift();
    if (!force && now - up.shownAt < 1000) return;
    up.shownAt = now;
    const first = up.samples[0];
    const rate = (up.sent - first.sent) / Math.max(1, now - first.at);
    const vars = { sent: sizeText(up.sent), total: sizeText(up.total) };
    up.row.querySelector("[data-ts-bar]").value = Math.floor((up.sent * 100) / up.total);
    up.row.querySelector("[data-ts-line]").textContent =
      rate > 0 ? say("progress", { ...vars, time: duration((up.total - up.sent) / rate) }) : say("progress-plain", vars);
  }

  // ---- sending ----

  // Wait before a retry; Pause cuts the wait short.
  function wait(up, ms) {
    return new Promise((resolve) => {
      up.wake = resolve;
      setTimeout(resolve, ms);
    });
  }

  // Send the next piece of a file. A dropped connection or a server hiccup is retried after 1, 2,
  // 4 ... 30 seconds; a refusal from the server stops the book with its words.
  async function sendPiece(up, file) {
    let delay = 1000;
    for (;;) {
      if (up.paused) throw PAUSED;
      const piece = file.blob.slice(file.offset, Math.min(file.offset + up.chunk, file.size));
      up.controller = new AbortController();
      try {
        const headers = { "Content-Type": "application/octet-stream", "X-CSRF-Token": csrf() };
        if (crypto.subtle) headers["X-Chunk-SHA256"] = await pieceSum(piece);
        const res = await fetch("/uploads/" + up.id + "/files/" + file.id + "?offset=" + file.offset, {
          method: "PUT", headers, body: piece, signal: up.controller.signal, credentials: "same-origin",
        });
        const data = await res.json().catch(() => ({}));
        if (res.ok) {
          file.offset = data.offset;
          return;
        }
        const code = data.error && data.error.code;
        if (code === "wrong_offset") {
          file.offset = data.offset; // the server says where it really is
          return;
        }
        if (res.status < 500 && code !== "checksum") throw new Stop(data.error && data.error.message);
      } catch (error) {
        if (error instanceof Stop) throw error;
        if (up.paused) throw PAUSED; // Pause aborted the request
      }
      up.row.querySelector("[data-ts-line]").textContent = say("retry", { n: delay / 1000 });
      await wait(up, delay);
      delay = Math.min(delay * 2, MAX_WAIT_MS);
    }
  }

  async function run(up) {
    up.paused = false;
    setState(up, "sending");
    up.samples = [];
    up.shownAt = 0;
    try {
      for (const file of up.files) {
        while (file.offset < file.size) {
          await sendPiece(up, file);
          progress(up, false);
        }
      }
      progress(up, true);
      setState(up, "checking", say("checking"));
      await finish(up);
    } catch (error) {
      if (error === PAUSED) return;
      setState(up, "failed", error instanceof Stop && error.message ? error.message : say("failed"));
    }
  }

  // Tell the server every byte is in, then let the server's row take over (it polls until done).
  async function finish(up) {
    await call("POST", "/uploads/" + up.id + "/finish");
    htmx.ajax("GET", "/hx/uploads/" + up.id, { target: up.row, swap: "outerHTML" });
  }

  function send(up) {
    turn = turn.then(() => run(up));
  }

  function pause(up) {
    up.paused = true;
    if (up.controller) up.controller.abort();
    if (up.wake) up.wake();
    progress(up, true);
    setState(up, "paused", say("paused", { sent: sizeText(up.sent), total: sizeText(up.total) }));
  }

  // ---- choosing ----

  function pathOf(file) {
    return file.tsPath || file.webkitRelativePath || file.name;
  }

  // Split what was picked or dropped into books: a zip next to nothing else, or at the top of the
  // picked folder when it holds several books, is a book; so is each folder (each subfolder when it
  // holds several books); the loose files left over make one book. A book's paths start at its folder.
  function split(files) {
    const several = root.querySelector("[data-ts-several]").checked;
    const depth = several ? 2 : 1;
    const books = new Map();
    for (const file of files) {
      const parts = pathOf(file).split("/");
      const zip = extension(file.name) === "zip" && parts.length <= depth;
      if (several && parts.length > 1 && parts.length <= depth && !zip) continue; // loose in the folder: left out
      const key = zip ? "z:" + parts.join("/") : parts.length > depth ? parts.slice(0, depth).join("/") : "";
      if (!books.has(key)) books.set(key, []);
      books.get(key).push(file);
    }
    for (const [key, group] of books) {
      const label = key && !key.startsWith("z:") ? key.split("/").pop() : "";
      addBook(group, label, several && label ? 1 : 0);
    }
  }

  function addBook(files, label, cut) {
    const name = (f) => pathOf(f).split("/").slice(cut).join("/");
    const title = label || files[0].name.replace(/\.[^.]*$/, "");
    const up = { id: "", files: [], samples: [], shownAt: 0, row: newRow(title, say(files.length === 1 ? "files-one" : "files-many", { n: files.length })) };
    uploads.set(up.row, up);
    if (list.children.length > 1) root.querySelector("#queue-h").textContent = say("books", { n: list.children.length });
    const copyProtected = files.find((f) => PROTECTED.includes(extension(f.name)));
    if (copyProtected) {
      refuse(up, title, say("refused-drm", { file: copyProtected.name }));
      return;
    }
    setState(up, "checking", say("checking"));
    // Books are created one at a time, as they are sent: the server limits unfinished uploads.
    turn = turn.then(async () => {
      try {
        const manifest = files.map((f) => ({ path: name(f), size: f.size, modified_ms: f.lastModified }));
        const created = await call("POST", "/libraries/" + root.dataset.tsLibrary + "/uploads", { label, files: manifest });
        const byPath = new Map(files.map((f) => [name(f), f]));
        adopt(up, created, created.files.map((f) => byPath.get(f.path)));
      } catch (error) {
        if (error.code === "no_audio") refuse(up, title, say("refused-none"));
        else if (error.code === "copy_protected") refuse(up, title, error.message);
        else setState(up, "failed", error.message || say("failed"));
        return;
      }
      await run(up);
    });
  }

  // Pair what the server expects with the files the person picked (one blob for each expected file).
  function adopt(up, view, blobs) {
    up.id = view.id;
    up.chunk = view.chunk_size;
    up.files = view.files.map((f, i) => ({ id: f.id, blob: blobs[i], size: f.size, offset: f.offset }));
    up.total = up.files.reduce((sum, f) => sum + f.size, 0) || 1;
    if (view.label) up.row.querySelector("[data-ts-title]").textContent = view.label;
  }

  // "Choose files again" after a reload: the same files carry on from the server's offsets. A file
  // is the same when its size matches and its path or its name does.
  async function chooseAgain(old, picked) {
    const view = await call("GET", "/uploads/" + old.dataset.tsUpload);
    const same = (f, p) => p.size === f.size && (pathOf(p) === f.path || p.name === f.path.split("/").pop());
    const blobs = view.files.map((f) => picked.find((p) => same(f, p)));
    if (blobs.includes(undefined)) {
      old.querySelector("[data-ts-line]").textContent = say("mismatch");
      return;
    }
    const up = { id: "", files: [], samples: [], shownAt: 0, row: newRow(view.label, old.querySelector("[data-ts-meta]").textContent) };
    old.replaceWith(up.row);
    uploads.set(up.row, up);
    adopt(up, view, blobs);
    send(up);
  }

  root.addEventListener("change", (event) => {
    const input = event.target;
    if (!input.matches("input[type=file]")) return;
    const files = [...input.files];
    const again = input.closest("[data-ts-upload]");
    input.value = "";
    if (again) chooseAgain(again, files).catch(() => {});
    else split(files);
  });

  // Dragging files or folders onto the page: the entries are read before the first await, then
  // every folder is read to its end (a reader returns its entries in batches).
  async function walk(entry) {
    if (entry.isFile) return new Promise((ok) => entry.file((f) => ok([Object.assign(f, { tsPath: entry.fullPath.slice(1) })]), () => ok([])));
    const reader = entry.createReader();
    const found = [];
    for (;;) {
      const batch = await new Promise((ok) => reader.readEntries(ok, () => ok([])));
      if (!batch.length) return found;
      for (const child of batch) found.push(...(await walk(child)));
    }
  }

  root.addEventListener("dragover", (event) => event.preventDefault());
  root.addEventListener("drop", async (event) => {
    event.preventDefault();
    const entries = [...event.dataTransfer.items].map((item) => item.webkitGetAsEntry()).filter(Boolean);
    split((await Promise.all(entries.map(walk))).flat());
  });

  root.addEventListener("click", (event) => {
    const button = event.target.closest("[data-ts-act]");
    if (!button) return;
    const row = button.closest("li");
    const up = uploads.get(row);
    const id = up ? up.id : row.dataset.tsUpload;
    switch (button.dataset.tsAct) {
      case "pause":
        pause(up);
        break;
      case "resume":
        send(up);
        break;
      case "cancel":
        if (up) up.paused = true;
        if (up && up.controller) up.controller.abort();
        call("POST", "/uploads/" + id + "/cancel").then(() => row.remove()).catch(() => {});
        break;
      case "retry":
        call("POST", "/uploads/" + id + "/finish")
          .then(() => htmx.ajax("GET", "/hx/uploads/" + id, { target: row, swap: "outerHTML" }))
          .catch(() => {});
        break;
    }
  });
}
