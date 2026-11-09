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
sheet. In scroll, the keys scroll a screen minus two lines.
The bar never hides unasked: a click with a mouse in the text does nothing to it, and the middle tap toggles it only for a
finger or pen (`pointerType`). A hidden bar always has a way back: a small handle at the top centre (`[data-show-bar]`,
44 by 44 px, "Show the bar", a real button) shows while `data-chrome="off"`. In the scroll flow a hidden bar takes no room
(`display: none`) and `reader.js` moves `scrollTop` by the change in the text's top, so the words stay put and no move is
recorded; the pages flow keeps the room, since its columns are laid out for it.

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

A part that does not load (pages flow, `fetchPart` in `reader-pages.js`) is tried once more after 1.5 s, quietly. If it
still fails there is one "This part did not load." toast with Try again, however often the person turns on; it goes when
a part loads, and when the reader is left (an htmx swap of the page, `pagehide`, or the flow changing). A failed result is
never kept, so Try again asks the server again. A redirect (the session ended) shows "Sign in" instead. The part's address
(no query) and the status or error go to `console.warn`.

Drop cap and verse (`reader_dropcap.go`): the chapter's first paragraph gets the big first letter only when the chapter is
not front matter ("Opening pages") and the block is prose: a paragraph of at least 140 characters, less than half of it
link. If that first paragraph is a dedication, an epigraph or a list of links, the chapter has no drop cap. Three or more
paragraphs in a row of at most 50 characters are verse: they carry `ts-reader__verse` and take no first-line indent.

## Comfort (G5)

Three choices under "More" in the Reading settings sheet, all off by default and kept in `reader.v1` (`keepAwake`, `warm`
`off`/`evening`/`always`, `autoHide`). They sit on `#reader` as `data-keep-awake`, `data-warm`, `data-auto-hide`; the script
`reader-comfort.js` is on the page only when one is on (the settings marker carries `data-comfort-src`, and
`reader-settings.js` imports it when a switch is turned on). Warm light sets `data-warm="on"` on `.ts-readerpage`
(`always`, or `evening` from 20:00 to 05:59 on the device, looked at again every 5 minutes); `pages-reader.css` draws a
multiplied 60 % wash of `--mark` over the page, never in the contrast theme (`TestComfortContrast` holds text at 4.5:1).
Keep awake takes a screen wake lock while the page is visible, lets go after 10 minutes without input and when the page
leaves; a refusal or a missing API is silent. Auto-hide sets `data-chrome="off"` 3 s after the last pointer or key unless
focus is in the bar or dock, a sheet or dialog is open, or text is selected. A bar it hid comes back when a mouse moves into
the top or bottom 64 px, or on any key except the ones that turn or scroll (arrows, PageUp and PageDown, Home, End, Space,
`[` and `]`), and by the handle. There is no brightness control: no web API sets
the screen's brightness, and a dimming overlay would only lower contrast.

## Read aloud (decision [0032](decisions/0032-read-aloud.md))

A "Read aloud" control opens a strip in the dock and speaks the book with the device's own voice, one sentence at a time, from the
sentence at the top of the view. The control is in the header beside Aa from 768 px and at the end of the dock row below it; the
offline reader shell has it too. It stays hidden while the device has no voice of its own: only browser voices with `localService`
true are listed or used, and every utterance names its voice, so book text never leaves the device.
`reader.js` shows the control (about ten lines) and imports `reader-aloud.js` on the first press. The module marks the spoken
sentence with `.ts-s.is-now` (a wash and an underline), follows it through `kit.follow` (`reader.js` `show()` then `moved()`, so
the saved place is the top of the view, counted as reading), turns the page in pages, and goes on into the next part and chapter
(scroll: the `ts-reader-next` sentinel; pages: `turn`). When the person moves the view away, following stops and "Back to the voice"
brings it back. Pause cancels and remembers the sentence; Play speaks it again. Starting pauses a playing audiobook and the audiobook
starting stops the voice; leaving the reader (a boosted link out, `pagehide`) cancels speech.
Voice and speed are the device's (`localStorage` `ts.aloud.voice`, `ts.aloud.rate`; speeds 0.8 to 2); voices of the book's language
come first. Keys, only while the strip is open: `j` back one sentence, `k` forward one. The engine is behind a three-method seam
(`voices`, `speak`, `cancel`; `window.tsNativeSpeech` replaces the browser's in the Android app, R2). Limits: no word marking, no
starting from a tapped sentence, no reading backwards across parts, no speech with the screen off. Tests: `TestReaderAloudMarkup`,
`TestAloudBrowser*` (a fake `speechSynthesis` in `aloud_browser_test.go` only), the `reader-aloud` a11y states, `TestAloudVisual`.

## Series and shelves (G7)

The last part of a book that belongs to a series ends with a quiet link, "Next in {series}: {title}", under the finished
button (`reader_series.go`). It names the book after this one in the first series among the books the person can see, so a
hidden book is skipped and its title never shows; the book page picks listening or reading. The series page rows carry the place
and the person's state ("Book 2 · 42%"), listening and reading both counting. "To read" is an ordinary shelf made the first time
the person taps it in the Add to shelf sheet (shown first, ticked when held); they may rename, empty or delete it, and the next tap
makes a new one. Send a book by link is not part of this.
## Look-up and the word list (G1)

Select a word in the reader and choose **Look up** (online only; the offline shell has no Look up). `reader-marks.js` imports
`reader-lookup.js` on the first press, which asks `GET /hx/books/{id}/read/lookup?q=` for a dialog (`readerCtx`, so `share.Can(ReadText)`;
`q` is 1 to 64 runes with a letter or digit, else a 422 toast). The dialog shows the entries from `internal/dict` as escaped text, a plain
DuckDuckGo link (`https://duckduckgo.com/?q=` plus the selected word, `rel="noopener noreferrer"`, opens in a new tab only when tapped; nothing
else is in it) and **Save word**, which fills the hidden `#reader-word-form` and posts `POST /hx/books/{id}/words`. A selection longer than 64
runes or over two sentences looks up its first word. For a book whose edition language is English, a word that is not found is tried once
without `-s`, `-es`, `-ies`, `-ed`, `-ing` or `-ly`; no other language is stemmed. Looking up never writes a reading place.

**Adding a dictionary.** No dictionary is bundled and Tendshelf never downloads one. The owner puts files in `<data>/dictionaries/`; the folder
is read at the first look-up and again whenever a file in it changes (name, size or time), so no restart is needed. At most eight are read
(by file name); symbolic links and sub-folders are ignored; a damaged file is skipped and logged, never fatal. With none, the popup says "No
dictionary on this server yet." and tells an administrator where to put one. Two formats, read from their published descriptions:

- **StarDict**: `name.ifo` with `name.idx` or `name.idx.gz`, `name.dict` or the dictzip `name.dict.dz`, and an optional `name.syn`
  (other spellings). 32-bit and 64-bit offsets (`idxoffsetbits`), `sametypesequence` or per-entry type bytes. Fields `m`, `l` and `t` are text;
  `h`, `g` and `x` have their tags stripped (a script is dropped with its text); other types are skipped. `idxfilesize` in the `.ifo` must match.
- **Text**: `name.tsv`, UTF-8, one `word<TAB>definition` a line; `\n` inside a definition is a line break.

Words match folded on both sides (lower case, accents off). Bounds, since a dictionary file is untrusted: `.idx` 256 MiB after unpacking
(a gzip bomb stops at the limit), `.syn` 64 MiB, TSV 64 MiB and 1 MiB a line, 2,000,000 headwords, an entry read at most 256 KiB and shown as
at most 4,000 runes, three entries a dictionary, 30 seconds to read one index, a parser panic recovered. `.dict.dz` is read chunk by chunk
(each chunk capped to its stated size), never unpacked whole. The package opens no network and writes nothing (`TestDictNoNetwork`).

**The word list** (`/me/words`, linked from the profile's reading panel) is private: `word_list` (migration 0022) has one row per person, book
and folded word; `library.WordList` joins through `visible()` so a revoked book's words stay stored and are not listed. A person keeps at most
5,000 words ("Your word list is full."). Each row shows the word, the sentence, the book and "Go to the page" (when the edition and sentence
are still known). `/me/words.md` and `/me/words.json` download the person's own list. A merge moves a text book's words to the audiobook (a
word both held stays once, the audiobook's); a split sends the ebook edition's words back.
