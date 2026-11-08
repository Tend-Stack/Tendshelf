// "Download all" on a series or shelf page (docs/plans/next-stage.md, L10). One module for the browser
// (downloads.js) and the Android app (native-downloads.js): each passes a driver that knows how its own
// queue works, and this file draws the button, the confirm dialog, the progress line and Stop. It adds no
// queue of its own: a book is handed to the driver one at a time, and the driver's existing queue downloads
// them one after another. The sentences come from the server on the wrapper's data-ts-msg-* attributes.
//
// A driver is {
//   usable()                  whether this device can download at all
//   have(ids)                 Promise of the Set of ids already saved or queued on this device
//   room(bytes)               Promise of {ok, free} for that many bytes
//   add(id)                   Promise; hands one book to the queue
//   stop(ids)                 cancels those books and keeps the rest
//   states(ids)               Map id -> "ready" | "failed" | "pending" | "none"
//   onChange(callback)        calls back whenever a book's state moves
//   quiet(on)                 mutes the per-book toasts while a batch runs
// }

const batches = new Map(); // list address -> the batch of that page
let driver = null;

export function mount(next) {
  if (driver) return;
  driver = next;
  document.addEventListener("click", onClick);
  document.addEventListener("htmx:after:swap", render);
  driver.onChange(render);
  render();
}

function boxes() {
  return document.querySelectorAll("[data-ts-download-all]");
}

function words(box, key, vars = {}) {
  const text = box.getAttribute("data-ts-msg-" + key) || "";
  return text.replace(/\{(\w+)\}/g, (_, name) => (name in vars ? vars[name] : ""));
}

// Decimal units, as the Downloads page writes them: "291 MB" below 1000 MB, "1.6 GB" above.
function size(bytes) {
  return bytes < 1e9 ? Math.max(1, Math.round(bytes / 1e6)) + " MB" : (bytes / 1e9).toFixed(1) + " GB";
}

function part(box, name) {
  return box.querySelector("[data-ts-dlall-" + name + "]");
}

// ---- Drawing ----

// A batch is over when every book it handed over has settled; a book that never arrived counts as failed.
function tally(batch) {
  const states = driver.states(batch.ids);
  let ready = 0;
  let failed = 0;
  let pending = 0;
  for (const id of batch.ids) {
    const state = states.get(id) || "none";
    if (state === "ready") ready++;
    else if (state === "failed" || (state === "none" && batch.added.has(id))) failed++;
    else pending++;
  }
  return { ready, failed, pending };
}

function finish(batch, text) {
  batch.running = false;
  batch.text = text;
  driver.quiet(false);
}

function render() {
  for (const box of boxes()) {
    box.hidden = !driver.usable();
    part(box, "label").textContent = words(box, "all-label");
    part(box, "stop").textContent = words(box, "all-stop");
    const batch = batches.get(box.dataset.tsDownloadAll);
    let progress = "";
    if (batch && batch.running) {
      const counts = tally(batch);
      const total = batch.ids.length;
      if (!batch.adding && counts.pending === 0) {
        const key = total === 1 ? "all-finished-one" : "all-finished";
        finish(batch, counts.failed ? words(box, "all-finished-some", { n: counts.failed, total }) : words(box, key, { total }));
      } else {
        progress = words(box, "all-progress", { done: Math.min(total, counts.ready + counts.failed + 1), total });
      }
    }
    const status = part(box, "status");
    status.textContent = progress || (batch ? batch.text : "");
    status.hidden = !status.textContent;
    const running = !!batch && batch.running;
    part(box, "stop").hidden = !running;
    part(box, "start").hidden = running;
  }
}

// ---- The confirm dialog ----

function ask(box, { title, body, go, close }) {
  const dialog = part(box, "dialog");
  part(box, "title").textContent = title;
  part(box, "body").textContent = body;
  part(box, "cancel").textContent = close;
  const button = part(box, "go");
  button.hidden = !go;
  button.textContent = go || "";
  if (!dialog.open) dialog.showModal();
}

// Reads the list, takes away what the device already holds, and says what would happen before anything starts.
async function open(box) {
  if (box.dataset.tsBusy) return;
  box.dataset.tsBusy = "1";
  const close = words(box, "all-close");
  try {
    let list;
    try {
      const res = await fetch(box.dataset.tsDownloadAll, { headers: { Accept: "application/json" }, credentials: "same-origin" });
      if (!res.ok) throw new Error("list " + res.status);
      list = await res.json();
    } catch {
      ask(box, { title: words(box, "all-error-title"), body: words(box, "all-error-body"), close });
      return;
    }
    const have = await driver.have(list.books.map((b) => b.id));
    const todo = list.books.filter((b) => !have.has(b.id));
    const bytes = todo.reduce((sum, b) => sum + b.bytes, 0);
    box.tsTodo = todo.map((b) => b.id);
    if (!todo.length) {
      ask(box, { title: words(box, "all-have-title"), body: words(box, "all-have-body"), close });
      return;
    }
    const room = await driver.room(bytes);
    if (!room.ok) {
      const body = words(box, "all-room-body", { size: size(bytes), free: size(room.free) });
      ask(box, { title: words(box, "all-room-title"), body, close });
      return;
    }
    ask(box, {
      title: todo.length === 1 ? words(box, "all-confirm-one") : words(box, "all-confirm-many", { count: todo.length }),
      body: words(box, "all-confirm-body", { size: size(bytes) }),
      go: words(box, "all-go"),
      close: words(box, "all-not-now"),
    });
  } finally {
    delete box.dataset.tsBusy;
  }
}

// ---- A batch ----

async function begin(box) {
  const ids = box.tsTodo || [];
  box.tsTodo = null;
  if (!ids.length) return;
  const batch = { ids, added: new Set(), running: true, adding: true, text: "" };
  batches.set(box.dataset.tsDownloadAll, batch);
  driver.quiet(true);
  render();
  // One at a time, in the page's order: the driver's queue downloads them in the order they were added.
  for (const id of ids) {
    if (!batch.running) break;
    await driver.add(id);
    batch.added.add(id);
    // Stop was pressed while this book was being handed over: it goes too.
    if (!batch.running) driver.stop([id]);
    render();
  }
  batch.adding = false;
  render();
}

// Stop cancels what is still queued from this batch and keeps every book that finished.
function stop(box) {
  const batch = batches.get(box.dataset.tsDownloadAll);
  if (!batch || !batch.running) return;
  const states = driver.states(batch.ids);
  const done = batch.ids.filter((id) => states.get(id) === "ready").length;
  driver.stop(batch.ids.filter((id) => states.get(id) === "pending"));
  finish(batch, words(box, "all-stopped", { done, total: batch.ids.length }));
  render();
}

function onClick(event) {
  const target = event.target.closest("[data-ts-dlall-start], [data-ts-dlall-stop], [data-ts-dlall-cancel], [data-ts-dlall-go]");
  const box = target && target.closest("[data-ts-download-all]");
  if (!box) return;
  if (target.matches("[data-ts-dlall-start]")) {
    open(box);
  } else if (target.matches("[data-ts-dlall-stop]")) {
    stop(box);
  } else {
    part(box, "dialog").close();
    if (target.matches("[data-ts-dlall-go]")) begin(box);
  }
}
