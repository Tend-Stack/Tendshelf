# 0018 Tendshelf reads books: read-along dropped, ebooks are books

Date: 2026-11-04. Status: accepted (operator decisions of 2026-11-04). Plan: [m5-book-reader.md](../plans/m5-book-reader.md).
Input: [research](../research/ebook-reader-features.md).
Supersedes [0001](0001-read-along.md) and [0017](0017-alignment-worker-pipes.md). Amends [0015](0015-upload-into-library.md)
(what an upload may contain). Replaces the R2 rule "an unpaired ebook never appears as a book" (`docs/plans/m5-r2-pairing.md`).

## Context

Read-along needed a Python worker and a model the owner installs. Tendshelf must never ask a regular person to install
anything, and the sentence timing only ever served people who had both formats. What readers want instead is a very good
reader: their place on every device, comfortable text, marks they own.

## Decision

1. **Read-along is removed completely.** No sentence timing, no worker, no voice following, no `/admin/runtimes`. Git
   history keeps the R3 work. The only external programs are `ffmpeg` and `ffprobe` again.
2. **A future voice feature uses the device's own text-to-speech** (`speechSynthesis`). Out of scope now. Taken up by [0032](0032-read-aloud.md).
3. **An ebook is a book.** A ready ebook file with no audiobook gets its own `books` row (`kind = 'text'`), with a cover,
   shelf place and reading position. `kind` says who owns the row's life: the scanner (`audio`) or the ebook service (`text`).
4. **One book, two formats, stays the advantage, and it is automatic.** The existing matcher (`Match`, `Decide`) runs by
   itself. A confident match makes the ebook book and the audiobook one book; an unsure match stays a suggestion for a
   person. "Not the same book" splits them and is remembered. Merging and splitting change database rows only, never a
   file, and an automatic merge never widens who can reach anything.
5. **Two formats, two positions.** Listening stays `progress` (milliseconds). Reading is `reading_positions` (a text
   anchor plus percent, never a page number). Neither moves the other: without sentence timing any mapping would be a guess.
6. **The speaker in the reader resumes the audiobook at its own saved place, and the book icon in the player opens the
   reader at its own saved place.** Chapter-level starts are not offered (reasons in the plan).
7. **One resume button per book** now reads: the primary button continues the format last used; the other format is a
   secondary button with a plain verb ("Read", "Listen"), never "Continue" or "Resume", and carries no place.
8. **Publisher HTML, CSS, fonts and scripts never reach the browser.** The reader renders only our markup from the
   canonical document. This is the sanitiser: scripted EPUB content has nothing to run in.
9. **Pages use CSS columns, one chapter part at a time**, turned through `scrollLeft`; scroll mode shares the same anchors.
10. **Add books accepts ebooks** into the library's upload folder (amends 0015 points 5 and 8 and its "Ebook uploads: R1
    owns them"). All other 0015 invariants hold. The Text tab's upload to `<data>/ebooks/` stays for adding a file to an
    existing book.

## Consequences

- `AGENTS.md`, `docs/architecture.md`, `docs/vision.md` and `docs/feature-map.md` change with the packages that make each
  statement true. `docs/read-along.md` is retired into a short `docs/reader.md`.
- The boards `screens/reader/ReadAlong*`, `FocusPhone`, `EyesFreePhone` (in the reader), `PreparingPhone` and
  `AlignFailPhone` no longer apply. The mode switcher, the Highlight, Follow and View settings, and "Play at" leave the
  boards that remain. Pages, tap zones, footnote popups and search have no board yet.
- Publisher styling, tables as tables, and embedded fonts are not shown. Right-to-left, vertical text and fixed layout wait.
- `covers.source` keeps its CHECK: a cover read from an ebook is `embedded`, which is what it is.

## Not handled, and why

- Mapping a reading place to an audio time: a guess without timing, and it would overwrite a real listening place.
- Sentence-level anything. `Sentences` stay in the document because highlights anchor on them.
- Share links to ebook-only books: a link page has no reader; left to the "send a book" package.
