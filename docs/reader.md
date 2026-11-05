# The reader

Tendshelf reads DRM-free ebooks itself ([decision 0018](decisions/0018-book-reader.md), which retired read-along and
its alignment worker; plan in [plans/m5-book-reader.md](plans/m5-book-reader.md)). The only external programs are
`ffmpeg` and `ffprobe`. Phase history is in [roadmap.md](roadmap.md).

## Rules

1. Ebook files are the user's: library ebooks are never written, renamed or deleted; an uploaded ebook is kept
   byte-identical as Tendshelf's own copy. Everything derived (the canonical document) can be rebuilt from the original.
2. Copy-protected ebooks are refused with a plain message and never decrypted, converted or parsed past the check.
3. Reading needs its own permission (`read_text`), separate from listening. Highlights, notes and bookmarks are private
   to the person who made them.
4. The reader is server-rendered (templ and HTMX). The publisher's HTML, CSS, fonts and scripts never reach the browser.

## Formats (`internal/ebook`)

One parser per format, all to the same canonical document: EPUB 2 and 3, PDF (text layer only, reflowed), plain text and
Markdown, DOCX, HTML, FB2 (and `.fb2.zip`), MOBI and AZW3 (DRM-free only). Files come from the scanner (an ebook inside
a book's folder), an upload on the book's Text tab (`<data>/ebooks/<sha256>.<ext>`, 200 MB cap) or the Add books page.

## Refusals

Checked before any content is parsed, with the message "This ebook is copy-protected, so Tendshelf will not open it."
(`refused_reason='drm'`): EPUB with `META-INF/rights.xml` (Adobe ADEPT), `sinf.xml` (Apple), `license.lcpl` (Readium
LCP) or an `encryption.xml` that encrypts anything but font obfuscation; PDF with an `/Encrypt` dictionary; encrypted
MOBI; any KFX; password-protected Office files.

Hostile-file bounds: 200 MB file, 1 GiB uncompressed, 10,000 zip entries, compression ratio over 100, no extraction to
disk, no DTD entity expansion, a 60 s parse deadline and `recover()` around each parser, 2 M words per edition.

## The canonical document

`tendshelf-doc-v1`, gzip JSON at `<data>/text/<doc_sha256>.json.gz`, immutable. Chapters hold blocks (paragraph,
heading, quote, list item, image, note, preformatted, rule); a block holds its text, style runs, links, sentences and
words. Ids: block `c{chapter}.p{block}`, sentence `c{chapter}.p{block}.s{n}`. Offsets are UTF-16 code units of the block
text, what the browser counts. The same file and recipe give the same bytes and ids.

Recipe `tendshelf-ebook-v2` adds, all omitted when empty so a v1 document still loads and hashes: `Block.links` (`start`,
`end`, `to` a block id or `url`, `note`), `imgW` and `imgH`, and `meta` description, publisher, series, series index,
year and cover part. A link keeps only `http`, `https` and `mailto` outside the book; a link to an id the book does not have
is dropped and its text stays. An inline span or link never ends its paragraph.

Recipe `tendshelf-ebook-v3` adds `pageList`, omitted when empty: the book's own print pages as `{label, block}` marks in
reading order (EPUB 3 `page-list` nav, EPUB 2 NCX `pageList`, and a PDF's page numbers). A target is a `part#id`, resolved
through the same block anchors links use, so a page that starts inside a paragraph shows at the paragraph's start; one that
lands on no block, or has an empty or over-16-character label, is dropped; of several on one block the first wins; at most
20,000. `pageCount` is the largest numeric label, else the number of marks; without a list it is as before
(ceil(characters / 1800)), and `data-pg` is the standard-page arithmetic. `ebook.PageAt(doc, chapter, block)` is the last
mark at or before a block, blank before the first. A label is shown ("p. xii of 13") and never stored in a place.

A new recipe never rewrites a document in place. At start-up and after every scan, a ready file with no edition for its
present bytes and recipe goes back to pending; while it waits, the reader keeps serving its newest edition. When the new
edition lands, in one transaction the person's highlights, bookmarks and reading positions move through `ebook.Reanchor`
(exact text at the old place, else the only match in that chapter, else the only match in the book, the context choosing
between several), the old edition is deleted and its document file with it when no edition shares it. A mark that cannot be
placed keeps its row with no edition: it stays in the marks list with its quote and has no jump; a position falls back to
its percent.

## The reader page (`internal/web`)

`GET /books/{id}/read` takes `ch`, `part`, `at` (a sentence id), `off` and `sheet` (`contents`, `marks`, `reading`); an old
`mode=` is ignored. A chapter is served in parts of about 40,000 UTF-16 units cut at block edges
(`/hx/books/{id}/read/chapters/{idx}?part=`); the section carries `data-part`, `data-before`, `data-prev-href` and
`data-next-href`, every block `data-c` (its units before it in the chapter), and the root `data-at`, `data-off`, `data-cpm`,
`data-learned`, `data-total-chars`, `data-before`, `data-chapter-chars`, `data-save` and `data-flow`. Where the page opens:
the address (`at`, or `ch` and `part`), else the person's saved place on this edition, else that place found again by its
quote, else its percent, else the first chapter that is not front matter. An EPUB picture is
`/books/{id}/read/images/{blockID}`: JPEG, PNG, GIF or WebP by sniffing, nothing else, with `default-src 'none'; sandbox`.
`GET` and `POST /reading/{bookID}` (the `/progress` rules, a form body for `sendBeacon`) read and save the reading place;
the server works out the percent. `POST /reading/{bookID}/finished` (`finished` 1 or 0) marks the book read or back to the
start; it writes the reading place only. The bar's speaker plays the audiobook from its own saved place and never moves the
reading place. Highlights, notes and bookmarks are stored as a sentence id and an offset plus the quote. Reading settings
(font, size, line spacing, margins, justify, hyphens, page or scroll, tap sides; the theme is the app's) are `reader.v1` in `user_settings`.

## The client (`reader.js`, `reader-pages.js`)

`reader.js` mounts from `data-ts-module` on `#reader`. The flow is the person's setting: `auto` shows pages below 1280 px
and scroll above, `pages` and `scroll` force one. Pages (`reader-pages.js`, loaded only for pages) lay one chapter part out
in CSS columns from `--ts-cols` (1 below 1024 px, 2 when both fit at 45ch), `--ts-col`, `--ts-gap` and `--ts-page-h`, set
through the CSSOM; a turn moves the part's `scrollLeft` by one page, and the next or previous part is fetched from
`data-next-href` and `data-prev-href` and replaces the section. A page number is only an index in the present layout.

The place is the first text on screen: a binary search over the sentences by where they end, then over the characters of
that sentence (about twenty layout reads). It is saved as the sentence id, the offset in its block, the next 64 characters,
the chapter and the character position, 3 s after a turn, and by `sendBeacon` when the page is hidden or the person
leaves; a failed save waits in `sessionStorage` until the connection returns. Opening a book writes nothing. A change of
size, font, margin, setting or window takes the saved place, lays out again and shows it, so the place survives all of
them. When the person comes back to the tab and another device holds a newer place, a toast offers "Go there" or "Stay
here"; the reader never jumps by itself.

Input: the outer quarters of the page turn it (mirrored by "Swap tap sides"), the middle shows or hides the bar and the
dock, and a tap on a link, a highlight, a button or with a selection does nothing else; swipe 48 px with touch or pen; keys
ArrowRight, PageDown and Space go forward, ArrowLeft, PageUp and Shift+Space back, `[` and `]` change chapter, `t` opens
the contents, `/` search (RD3). Keys are caught on `window` before `player.js`, and left alone in a field, a dialog or an open
sheet. In scroll, taps in the middle toggle the chrome and the keys scroll a screen minus two lines.

Marks (`reader-marks.js`, RD3): selecting text shows a toolbar with four colours (yellow, green, blue, pink), Note and Copy.
A highlight is posted with `data-o` plus the characters before the point inside the sentence, so the server gets offsets
in the block text. The note dialog (`#reader-note-dialog`) and all labels are rendered by the server. The bookmark button
in the bar keeps the first sentence on screen (`POST /books/{id}/bookmarks` with `edition_id` and `sid`); text bookmarks
show on the marks sheet, never on the audio Bookmarks tab. A footnote link (`data-note-href`) loads `reader-notes.js` and
opens `GET /hx/books/{id}/read/note/{blockID}` as a dialog (the target block and the note blocks after it until another
note's target, at most ten); Escape, the backdrop and Close shut it and focus returns to the link. Any other in-book link
jumps and the next page offers "Back to where I was". Search is `?sheet=search&q=` (or the htmx fragment
`/hx/books/{id}/read/search`): the whole phrase, folded for case, accents and white space, at least two characters, 200 hits
at most, each with its chapter, an 80-character snippet and a link with `at`, `off` and `hit`, which marks the words with
`::highlight(ts-hit)`. `GET /books/{id}/read/marks.md`, `.txt` and `.json` download the person's own highlights, notes and
text bookmarks. A document that is missing, damaged or too large gives "Tendshelf cannot open this book right now." and, when the person may download, a link to the original file (`/books/{id}/text/{fileID}/file`), so the book can be opened in another app.

Progress: the ruler and `p. n of N` come from the block's `data-pg`, the time left from the person's own speed
(`data-cpm`, characters per minute) and the characters remaining in the chapter and the book, in steps of five minutes, with
"About" until three sessions have taught the speed. The last part carries "That's the end of the book." and a button
that marks the book finished; the reader never marks it itself.

## Reading stats (`/me/reading`)

"Your reading" is private to the signed-in person: time, days read, words a minute, books finished, a Time chart, a yearly goal
and the covers finished in the period. Time comes from `reading_sessions.active_ms`, counted on the local day a session ended;
speed is characters over five, shown as "About n" to the nearest 10; finished books come from `reading_positions.finished_at`.
The goal is `{"goalBooks": n}` under `reading.v1` in `user_settings` (0 to 365, 0 is no goal). Covers go through `CardsByID` with
the person's scope, so a revoked book's title never shows; its time and count stay. There is no family view. Listening and
reading never write each other.

## Checks (RD4)

The reader is in the accessibility audit: `reader-pages` and `reader-scroll` are pages (the owner reads in pages, an admin in
scroll), and the four sheets, the footnote popup, the selection toolbar and the note dialog are states, each at four widths in
four themes with 0 findings. Behind an open sheet the reader page is `inert`; its shell navigation is hidden while the reader
is up; the book title is the page's h1 and a chapter's title its h2. One allowlist line stands: in Pages the part is a column
box, so the text of later columns lies outside it until a turn. `TestBRContrast` holds `--read-ink` on every highlight colour
and the selection wash in every theme (decision 0016); `TestBRVisual` captures the states beside the boards;
`TestBRLayoutTime` lays out a 40,000-unit part with the CPU throttled 4x (300 ms) and times turns (16 ms at p95).

## Series and cards across formats (L)

Where a person stands in a book is the place of the format used last (`standIn`, `card_progress.go`): the newer client time of the
listening place and the reading place; a book finished in either format is finished. Home's "Your series in progress" row, the
series tiles on the book page and the thin line at the foot of a started book's cover (library grid and list, series page, shelves)
all read it. A row keeps one control: Read when reading was last, Play otherwise. Nothing here writes a place.
## Offline (G6, decision [0019](decisions/0019-offline-reading.md))

The book page's Download keeps the audio and the text of the edition the person reads (both rights: `ReadText` and
`DownloadBook`). `downloads-text.js` reads `GET /offline/books/{id}/manifest`, stores every chapter part and picture under the
address the reader asks for, then the contents shell and the reader shell (`/offline/books/{id}/read`) last, so a half-stored book
never opens. Offline, the worker answers those addresses and `reader-offline.js` fills the shell. The daily check compares the
manifest `revision` (edition, the person's highlights, text bookmarks): a new one stores the text again, a 404 removes it, a failed
request changes nothing. Highlights, notes, bookmarks, search and settings need the server. `/offline?text=` lists held books.

## Skim (G4)

The dock's ruler stays the picture; a transparent range input (`#reader-skim`, 0 to 10000, 44 px tall) lies over it and
`reader.js` shows it once mounted and keeps its value at the place. The first pointer press, key or focus on it imports
`reader-skim.js`. While it moves, the tip (`#reader-skim-tip`) and the input's `aria-valuetext` read "{chapter title} · {n} %",
from `data-skim` on `#reader` (`[[characters before the chapter, title], ...]`, titles cut to 60 characters, at most 300
entries). Letting go (or Enter) opens `/books/{id}/read?ch=N&bp=P`; the arrow keys move one percent, Home and End are the
browser's, Escape puts the thumb back. A skim writes no place: it only stores `ts.back.<book>` (if not already set), so the
"Back to where I was" chip of `reader-marks.js` appears on arrival and goes after three forward turns; reading on is what saves.
On the server `bp` (an integer 0 to 10000, else ignored, and ignored beside `part`) wins over `ch` and opens the part holding
the block at that share of the book's text, at its first sentence (`rsPlaceAtChars`); `at` still wins over `bp`. Offline the shell
cannot place `bp` and `reader-offline.js` reads only `ch`: the chapter opens at its start, and the chip still leads back.
