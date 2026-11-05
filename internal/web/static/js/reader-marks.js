// The reader's marks (M5 RD3): the selection toolbar with four highlight colours, Note and Copy; the note
// dialog; the bookmark button; footnote links; the "Back to where I was" chip; and the search hit. Every
// word on screen is server-rendered (the toolbar, #reader-note-dialog and data-copy), none lives here.
// Offsets sent to the server are UTF-16 offsets in the block text: the sentence's data-o plus the
// characters before the point inside the sentence. Mounted by shell.js on #reader-marks.
export function mount(menu) {
  const reader = document.getElementById("reader");
  const form = document.getElementById("reader-hl-form");
  const dialog = document.getElementById("reader-note-dialog");
  if (!reader || !form || !dialog) return;
  const abort = new AbortController();
  const opts = { signal: abort.signal };
  const copy = JSON.parse(reader.dataset.copy || "{}");
  const backKey = "ts.back." + reader.dataset.book;
  let picked = null; // the last good selection: both ends and the words
  let colour = "yellow";

  const alive = () => menu.isConnected || (abort.abort(), false);
  const hide = () => (menu.hidden = true);

  // Characters of sentence text before a point.
  function before(sentence, node, offset) {
    const range = new Range();
    range.selectNodeContents(sentence);
    range.setEnd(node, offset);
    return range.toString().length;
  }

  // The sentences a range touches, as the two ends the server wants.
  function ends(range) {
    const root = range.commonAncestorContainer;
    const home = (root.nodeType === 1 ? root : root.parentElement).closest(".ts-s");
    const all = home ? [home] : [...(root.nodeType === 1 ? root : root.parentElement).querySelectorAll(".ts-s")].filter((s) => range.intersectsNode(s));
    if (!all.length || !reader.querySelector(".ts-reader__text").contains(all[0])) return null;
    const first = all[0];
    const last = all[all.length - 1];
    const from = first.contains(range.startContainer) ? before(first, range.startContainer, range.startOffset) : 0;
    const to = last.contains(range.endContainer) ? before(last, range.endContainer, range.endOffset) : last.textContent.length;
    return { start: first, startOff: Number(first.dataset.o) + from, end: last, endOff: Number(last.dataset.o) + to };
  }

  document.addEventListener(
    "selectionchange",
    () => {
      if (!alive()) return;
      const selection = getSelection();
      if (selection.isCollapsed || !selection.rangeCount) {
        if (!menu.contains(document.activeElement)) hide();
        return;
      }
      const range = selection.getRangeAt(0);
      const found = ends(range);
      if (!found) return hide();
      picked = { ...found, text: selection.toString() };
      menu.hidden = false;
      const box = range.getBoundingClientRect();
      const left = Math.min(Math.max(8, box.left + box.width / 2 - menu.offsetWidth / 2), innerWidth - menu.offsetWidth - 8);
      const top = box.top - menu.offsetHeight - 8;
      menu.style.setProperty("left", left + "px");
      menu.style.setProperty("top", Math.max(8, top < 8 ? box.bottom + 8 : top) + "px");
    },
    opts,
  );

  menu.addEventListener("pointerdown", (event) => event.preventDefault(), opts); // keeps the selection
  document.addEventListener("keydown", (event) => event.key === "Escape" && hide(), opts);

  // ---- Saving ----

  // Fills the hidden form with the marked words and sends it (with the note, when there is one).
  function save(marked, note) {
    form.elements.start_sid.value = marked.start.dataset.sid;
    form.elements.start_off.value = marked.startOff;
    form.elements.end_sid.value = marked.end.dataset.sid;
    form.elements.end_off.value = marked.endOff;
    form.elements.client_id.value = crypto.randomUUID();
    form.elements.colour.value = colour;
    form.elements.note.value = note || "";
    form.requestSubmit();
    hide();
    getSelection().removeAllRanges();
  }

  // The note dialog is in the page already: open it for the marked words.
  function ask(marked) {
    const field = dialog.querySelector("textarea");
    field.value = "";
    dialog.onclose = () => {
      if (dialog.returnValue === "save") save(marked, field.value);
      dialog.returnValue = "";
    };
    dialog.showModal();
    field.focus();
  }

  // The first sentence on screen: the place a bookmark keeps.
  function firstShown() {
    const top = document.querySelector(".ts-readerbar").getBoundingClientRect().bottom;
    return [...reader.querySelectorAll(".ts-s")].find((sentence) => {
      const rects = sentence.getClientRects();
      const last = rects[rects.length - 1];
      return last && last.bottom > top && last.right > 0 && last.left < innerWidth;
    });
  }

  // ---- Buttons ----

  function act(kind, marked) {
    if (kind === "note") ask(marked);
    else if (kind === "copy") navigator.clipboard.writeText(marked.text);
    else save(marked);
    hide();
  }

  menu.addEventListener(
    "click",
    (event) => {
      const button = event.target.closest("[data-ts-mark]");
      if (!button || !picked) return;
      colour = button.dataset.colour || colour;
      act(button.dataset.tsMark, picked);
    },
    opts,
  );

  // Bookmark this page: the first sentence on screen goes to the hidden form.
  document.addEventListener(
    "click",
    (event) => {
      const bookmark = event.target.closest('[data-ts-mark="bookmark"]');
      const sentence = bookmark && alive() && firstShown();
      if (!sentence) return;
      const bm = document.getElementById("reader-bm-form");
      bm.elements.sid.value = sentence.dataset.sid;
      bm.elements.client_id.value = crypto.randomUUID();
      bm.requestSubmit();
    },
    opts,
  );

  // ---- Links ----

  // Where a jump left from: the sentence that held the link. The next page shows the way back.
  const remember = (link) => {
    const sentence = link.closest(".ts-s");
    if (sentence) sessionStorage.setItem(backKey, JSON.stringify([sentence.dataset.sid, sentence.dataset.o]));
  };

  reader.addEventListener(
    "click",
    async (event) => {
      // A note dot opens the marks sheet, where the note can be read and changed.
      if (event.target.closest("[data-hl-note]")) return document.querySelector('.ts-readerbar a[href*="sheet=marks"]').click();
      const note = event.target.closest("a[data-note-href]");
      if (note) {
        event.preventDefault();
        event.stopPropagation(); // htmx's boost sits on the link itself and would follow it
        try {
          (await import(reader.dataset.notesSrc)).open(note, () => remember(note));
        } catch {
          location.href = note.href; // the popup did not come: the plain link still goes there
        }
        return;
      }
      const jump = event.target.closest("a.ts-reader__link:not([target])");
      if (jump) remember(jump);
    },
    { ...opts, capture: true }, // before the link's own boost handler
  );

  // ---- On arrival ----

  const back = JSON.parse(sessionStorage.getItem(backKey) || "null");
  sessionStorage.removeItem(backKey);
  if (back && copy.back) {
    const chip = document.createElement("a");
    chip.className = "ts-btn ts-reader__back";
    chip.textContent = copy.back;
    chip.href = `/books/${reader.dataset.book}/read?edition=${reader.dataset.edition}&at=${encodeURIComponent(back[0])}&off=${back[1]}`;
    reader.closest(".ts-readerpage").append(chip);
  }

  // A search result opens with its words marked: a text range in the page's highlight registry.
  if (location.search.includes("hit=") && window.Highlight) import(reader.dataset.notesSrc).then((notes) => notes.hit(reader));
}
