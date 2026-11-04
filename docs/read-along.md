# Read-along

Status: design, 2026-10-31. Not started. Roadmap phase **R** (after P2), milestones R1-R6 in
[roadmap.md](roadmap.md). Rule changes it needs are in [decision 0001](decisions/0001-read-along.md).

Read-along is part of the book, not a second app. An ebook is a **text edition** of a book that already
has audio. The player, the position, bookmarks, notes, sharing and offline downloads are the existing
ones, extended. There is no standalone ebook library: an ebook that matches no audiobook waits in the
admin inbox until it is paired.

## Invariants

1. Ebook files are the user's, like audio files: library ebooks are never written, renamed or deleted;
   an uploaded ebook is kept byte-identical as Tendshelf's own copy. Every derived artefact (canonical
   text, timings, exports) is a separate file that can be rebuilt from the original.
2. Copy-protected ebooks are refused with a plain message and never decrypted, converted or parsed
   past the protection check.
3. One position per user and book: milliseconds on the global timeline (architecture invariant 6). A
   text location is stored beside it only to keep the exact reading place; it never forks the position.
4. A timing is measured from the audio or marked as a gap. Tendshelf never interpolates a word or
   sentence time it did not measure, and never times text that is absent from the recording.
5. An alignment is reproducible: it is keyed by recipe, model file hashes, edition text hash and the
   ordered audio quick hashes. Changing any of them makes a new alignment; old ones stay readable
   until replaced.
6. Models are optional runtime components the owner installs explicitly; none ships in the binary or
   the repository. The server stays pure Go; the model runs in a separate, owned, loopback-only process.
7. Reading an ebook needs its own permission (`read_text`), separate from listening. Highlights and
   notes are private to the user who made them.
8. The reader is server-rendered (templ + HTMX); the only new script is a small ES module that maps
   the player's time to the text.

## 1. Ebook ingestion (`internal/ebook`)

**Where ebooks come from.** (a) The scanner finds ebook files inside a book's folder (same rules as audio:
read-only, quick hash, move detection, missing kept). (b) An upload on the book's **Text** page
(`/books/{id}/text`), stored as `<data>/ebooks/<sha256>.<ext>`, mode 0600, 200 MB cap. (c) An ebook
found in a library folder with no audio becomes an **unpaired edition** in `/admin/ebooks`.

**Formats** (one parser per format, all to the same canonical document):

| Format | Reader | Notes |
| --- | --- | --- |
| EPUB 2 and 3 | `archive/zip`, OPF/NCX/nav via `encoding/xml`, content via `x/net/html` | Spine order, nav titles, images (raster only; SVG shown as its alt text). EPUB3 Media Overlays (SMIL) are read too, see section 6. |
| PDF | text layer only | Reflow: join lines, undo end-of-line hyphenation, drop repeated headers/footers and page numbers, detect headings by size. If the reflow check fails (two columns, mostly short lines, tables), the edition is `layout='page'`: **Read** opens the original PDF in the browser's own viewer; read-along still shows the extracted text plainly. No text layer: refused as `no_text` ("This PDF is pictures of pages, so Tendshelf cannot read its words"). No OCR. |
| Plain text, Markdown | own small parser | Blank-line paragraphs; Markdown headings (`#`), emphasis and lists; tables and raw HTML become plain text. Chapters by headings or by lines like `Chapter 12`. |
| DOCX | `archive/zip` + `encoding/xml` | `word/document.xml`; chapters from `Heading1`/outline level 0; footnotes kept as notes. |
| HTML | `x/net/html` | One file = one document; chapters by `h1`/`h2`. Scripts, styles and remote resources dropped. |
| FB2 (and `.fb2.zip`) | `encoding/xml` | `<section>` = chapter, embedded `<binary>` images. |
| MOBI, AZW3 (KF8) | own PalmDB reader (PalmDOC LZ77; HUFF/CDIC later) | DRM-free only, converted at import to the canonical document. KFX, AZW (old), AZW4, PRC with DRM: refused. |

**Copy-protection check (before any parsing of content):** EPUB with `META-INF/rights.xml` (Adobe
ADEPT), `sinf.xml` (Apple), `license.lcpl` (Readium LCP), or an `encryption.xml` that encrypts anything
with an algorithm other than font obfuscation (`http://www.idpf.org/2008/embedding`,
`http://ns.adobe.com/pdf/enc#RC`); PDF with an `/Encrypt` dictionary; MOBI header encryption type 1 or
2; any KFX; DOCX that is an OLE compound file (password-protected Office). Result:
`refused_reason='drm'`, message "This ebook is copy-protected, so Tendshelf will not open it."

**Hostile-file bounds:** 200 MB file, 1 GiB total uncompressed, 10,000 zip entries, compression ratio
over 100 refused, no extraction to disk (entries are read in memory), paths never joined to the file
system, XML without DTD entity expansion (`encoding/xml` has none; `Strict=false` with `xml.HTMLEntity`
only), 60 s parse deadline per file, `recover()` around each parser, 2 M words per edition.

**Canonical document** `tendshelf-doc-v1`, JSON gzip at `<data>/text/<doc_sha256>.json.gz`, immutable:

```
{ "v":1, "recipe":"tendshelf-ebook-v1", "format":"epub", "sourceSha256":"…",
  "meta":{"title","subtitle","authors":[…],"language":"en","identifiers":{"isbn","asin"}},
  "chapters":[ { "idx":3, "title":"Chapter Three", "front":false, "src":"OEBPS/ch03.xhtml",
    "blocks":[ { "id":"c3.p12", "kind":"p", "text":"…",
      "runs":[[0,5,"em"]], "sentences":[[0,48],[49,120]], "words":[[0,3],[4,9]],
      "src":{"part":"OEBPS/ch03.xhtml","start":10422,"end":10790,"map":[[0,10422],[37,10466]]} } ] } ] }
```

- Hierarchy chapters → blocks (paragraph, heading, quote, list item, image, note, preformatted, rule) →
  sentences → words. IDs: block `c{chapter}.p{block}`, sentence `c{chapter}.p{block}.s{n}`, word = index
  in the sentence. Offsets are UTF-16 code units in the block text (what the browser counts); `src.map`
  maps canonical offsets back to byte offsets in the original part (entities and collapsed space).
- Sentence splitting `tendshelf-segment-v1` (own rules, per language): never after a listed
  abbreviation (Mr., Dr., St., e.g., Sr., Sra., …) or an initial, keeps closing quotes and brackets with
  the sentence, handles `…`, `?!`, Spanish `¿¡`. Words: Unicode letters and digits with inner apostrophes
  and hyphens.
- `front=true` for title page, copyright, contents, dedication-like short pages, by nav type
  (`epub:type`), title words and size. Front matter is shown but excluded from coverage numbers.
- Same file and recipe give the same bytes and IDs (golden test). A new parser recipe never rewrites
  an existing document; "Read the ebook again" makes a new one and re-anchors highlights by quote.

**Pairing** (`internal/ebook/pair.go`): an ebook in a book's folder pairs to that book; an upload pairs to
the book it was uploaded to; otherwise score candidates: equal ISBN or ASIN in any book's details pairs
automatically; normalised title (subtitle, series marks and "Unabridged" removed) plus the author
surname set equal to exactly one book gives a suggestion with score 0.9 that is applied automatically
only when it is the single candidate; anything else waits in the inbox for a person. Manual pairing
and unpairing are always possible. A book may have several editions; one is the book's default and
each user may choose their own (`text_choices`). Audiobook and ebook ISBNs usually differ, so title
plus author is the common path (guess, measure on the fixture library).

## 2. Alignment pipeline (`internal/align`)

One background job per (edition, audio set). Stages:

1. **Normalise** (`tendshelf-normalize-v1`, Go): NFC, case fold, apostrophes and dashes unified,
   punctuation removed, numbers to words for English and Spanish (port of TendVox
   `english-number-forms-v1` / `fidelity-normalize-v1`; Spanish new), listed abbreviations expanded.
   One source word may become several spoken tokens; each token keeps its source word index. A word
   with characters the model's vocabulary lacks, or an ambiguous reading (`St.`, a bare 4-digit
   number in English), becomes a **wildcard** token instead of being dropped.
2. **Emissions** (model, the only model step): ffmpeg decodes each audio range to 16 kHz mono s16le
   through a pipe; Go sends 30 s windows with 1 s overlap each side to the worker, which returns
   wav2vec2 CTC log-probabilities (20 ms frames). Go keeps the central 30 s of each window, quantises
   to uint8 (0.1 nat steps, clamp at -25.5) and writes `<data>/cache/align/<audio quick_hash>/<model
   sha>/<window>.em` atomically. This is the resume point; a restart continues at the first missing
   window. The cache is reconstructible, bounded (default 2 GiB, least recently used removed) and
   excluded from backups and exports.
3. **Coarse transcript** (Go): greedy CTC decoding of the emissions gives the heard words with frame
   times. This is the ASR transcript; wav2vec2 trained on read audiobooks (LibriSpeech) is a fair fit
   for narration (guess, measured in R3). It is stored only inside the emissions cache, never shown.
4. **Map** (Go): cut the audio timeline into blocks of about 10 minutes at the nearest pause (blank run
   of 300 ms or more). For each block, look up its 5-grams among the edition's 5-grams that occur once
   in the whole edition; the densest text window wins. Blocks with fewer than 3 hits are **audio
   without text** (publisher intro, credits, music). Each block maps independently, so chapter order
   differences, front matter that is not read and missing chapters need no special case. Within a
   mapped region, anchors are the longest increasing run of unique 5-gram hits; between anchors a
   word-level Needleman-Wunsch (exact = match, edit distance at most 1 per 4 letters = near match),
   bounded to 4 M cells per gap; a larger gap first re-anchors with 3-grams unique in the window.
5. **Fine alignment** (Go): CTC forced alignment (Viterbi over the 2L+1 blank-interleaved token
   sequence) of the ebook's own tokens over the cached emissions, one segment per pair of anchors at
   most 30 s apart, padded 200 ms. Wildcards use a star state whose emission is the best non-blank
   log-probability, so the neighbours stay right; a wildcard word gets the span between its
   neighbours and the mark `between`. Gaps longer than 30 s, or a rate outside 0.5-6 words per second,
   are **not aligned** (abridged or skipped text) rather than forced.
6. **Confidence**: a word is timed when its duration-weighted mean token probability is at least 0.30
   (TendVox policy; a model-dependent threshold, not a calibrated probability); otherwise `not-found`.
   A sentence's time is its first timed word's start to its last timed word's end; its confidence
   (0-1) combines the mean word score, the share of words timed and the share the coarse transcript
   agreed with. Levels: high at 0.75, low under 0.4 (recipe constants, calibrated in R3). A sentence
   with no timed word is a gap with a reason: `not-in-recording`, `not-found`, `unsupported`.

Why this shape: one model (wav2vec2 CTC) serves both the coarse transcript and the fine alignment, the
model process only computes emissions, and everything that decides (mapping, alignment, thresholds) is
deterministic Go with golden tests. Re-aligning a changed chapter or another edition of the same
recording calls no model when the emissions are cached. Chosen not to use a second ASR model
(whisper.cpp, Parakeet): a second runtime and model to install, and whisper can invent text over
music or silence. Reconsider only if the R3 accuracy target fails on real narration.

**Reuse from TendVox** (same author; NOTICES row when ported): the wav2vec2 models and their
Apache-2.0 licences, the 0.30 timed-word threshold and "no interpolation" policy, the owned-server
lifetime of `docs/alignment-worker.md` "Owned word timing server" (first-demand launch, loopback
ephemeral port, 32-byte token in the environment only, ready line plus authenticated `/healthz` whose
identity must match, one request at a time, stop after 5 idle minutes and on exit, `Pdeathsig`), and
its definite/uncertain failure split. Different: the worker returns emissions instead of word spans
(protocol `tendshelf-emissions-v1`), and a local re-run is free, so a failed window is retried
automatically up to 2 times before the job is held.

**Worker** (`runtimes/align/worker.py`, derived from TendVox `runtimes/alignment/worker.py`; Python
3.12, PyTorch CPU, transformers, safetensors, numpy; GPU optional with a CUDA/ROCm torch build and
`--device`). Launched by `internal/align/runtime` with a fixed argument list, `--threads` (default
min(4, CPUs/2)), lowered priority, `Pdeathsig` on Linux. Endpoints: `GET /healthz` → `{protocol,
model, modelSha256, vocab, frameMs, language, revision}`; `POST /v1/emissions` with headers
`X-Request-Id`, `X-Audio-Sha256`, body raw PCM (`audio/L16;rate=16000;channels=1`, at most 34 s) →
`{requestId, audioSha256, frames, vocabSize, frameMs, logprobs: base64 float16}`. Go checks every
identity and hash and refuses a response that does not match.

**Install** (R3, admin page `/admin/runtimes`): Tendshelf does not download runtimes. The page shows the
pinned requirements, the official model page and revision, sizes and the licence, plus a **Check** button
that hashes every model file, runs `--describe` and records a `runtime_components` row. Only checked
components can be used. One-click installation is out of scope (it would need a second outbound
download path; see decision 0001).

**Estimates** (guesses until R3 measures them on the fixture and a public-domain recording):

| Item | Estimate | Basis |
| --- | --- | --- |
| CPU time, English base model, 4 threads, desktop x86 | 2-4 min per hour of audio | TendVox measured 0.055 s per audio second for its server, alignment included |
| CPU time, Spanish large XLSR model | 8-15 min per hour | about 3-4x the base model (guess) |
| ARM mini PC / NAS (4 cores) | 3-5x the desktop time | guess |
| Go mapping and alignment | under 10 s per hour | O(frames x tokens) per 30 s segment |
| Worker memory | 1-2 GiB resident | TendVox measured 1.08-1.98 GiB |
| Storage per 10-hour, 100k-word book | text 0.4-0.8 MB, timings 0.3-0.6 MB, emissions cache about 58 MB (5.8 MB per hour, reconstructible) | 50 frames x 32 tokens x 1 byte per second |
| Runtime, once | Python env with CPU torch 1.5-2 GB; English model about 380 MB, Spanish about 1.3 GB | TendVox environment and model cards |

**Storage of results.** Per chapter, `tendshelf-timing-v1` JSON gzip at `<data>/align/<chapter_key>.json.gz`,
content-addressed by `chapter_key` = SHA-256 of (recipe, model sha, chapter normalised text sha,
audio quick hashes of the mapped range). Times are global-timeline milliseconds. Re-running with the
same key reuses the file; editing one chapter's text or replacing one audio file changes only the keys
it touches. Timing payload served to the browser (flat arrays, -1 for no time):

```
{ "v":1, "alignment":"al_…", "chapter":3, "sid":["c3.p12.s0",…], "ss":[…], "se":[…], "sc":[…],
  "gap":[null,"not-found",…], "ws":[sentence index…], "wo0":[…], "wo1":[…], "w0":[…], "w1":[…] }
```

**Job and states.** Kind `align.book`, concurrency 1, lease 60 s. `alignments.state`: `queued` →
`emitting` → `aligning` → `done` (body coverage at least 90 %) | `partial` (5-90 %) | `failed` |
`cancelled` | `held`. `alignment_chapters.state`: `pending`, `aligned`, `unmatched`, `failed`. Restart:
`emitting` and `aligning` return to `queued` and resume from the cache and finished chapters. `held`
needs **Resume** or **Discard** from a person: a window failed 3 times, the runtime identity changed,
or an audio file's quick hash changed mid-job. Progress JSON: stage, chapters done/total, audio seconds
done/total; an estimate of time left appears only after 10 measured windows of this job.

**Failure modes** (code: what the person sees):

| Code | When | Shown |
| --- | --- | --- |
| `runtime_missing` | no checked component for the edition's language | "Read-along for Spanish needs word timing for Spanish. An admin can set it up." |
| `no_match` | under 5 % of body sentences timed | "This ebook does not seem to match the recording." Suggest unpairing. |
| `partial` | 5-90 % timed | "Read-along works for 64 % of the book." Chapter list marks the rest. |
| `abridged` (label on partial) | at least 85 % of speech mapped but under 70 % of text | "The recording seems shorter than the ebook (abridged?). Parts not read are shown in grey." |
| `different_edition` (label) | near-match words over 8 % inside anchored regions | "This looks like a different edition. It still works; small differences are not highlighted." |
| `audio_changed` | audio quick hashes differ from the alignment's | Alignment marked stale; "Prepare read-along again". |
| `worker_failed` | window failed 3 times | Job held; admin log has details. |

Thresholds are recipe constants; changing one is a new recipe version.

## 3. Reading with the player (`internal/web`, `static/js/readalong.js`)

Page `/books/{id}/read?mode=&edition=&at=` inside the normal layout, so the player bar keeps playing.
Mode labels are Listen, Read along, Read, Focus line (the wording of
`docs/design/claude-design-prompt-reader.md`). The mode switch, chapter list, settings and highlight menus are HTMX; chapter text is a server-rendered
fragment with one `<span class="s" data-s="i">` per sentence (style runs split at sentence edges).

| Mode | What it does |
| --- | --- |
| **Listen** | The book page and player only (today's behaviour). |
| **Read along** | Text with the current sentence (and word) highlighted and gentle auto-scroll. Tap or Enter on a sentence plays from its start. Gap sentences are shown in grey and are not playable. |
| **Read** | Text only. The reading position (first sentence fully in view, after 2 s still) becomes the position, so **Listen** resumes at the sentence being read. |
| **Focus line** | One sentence at a time, large type, centred; follows the audio when playing, otherwise next/previous buttons, arrow keys and swipe. |
| **Car** | The P4 car screen (big controls, no text). Hidden until P4 ships. |

**Highlight script** (`readalong.js`, ES module, about 300 lines, no framework): subscribes to the player
module's time (`player.onTime`), loads the chapter's timing JSON into typed arrays, and on each
`requestAnimationFrame` while playing binary-searches the sentence start array, then the word array of
that sentence; it touches the DOM only when an index changes. Sentence highlight is a class; word
highlight uses the CSS Custom Highlight API with a `Range` over the sentence's text nodes (no per-word
spans); browsers without it get sentence highlight only, and the setting says so. Seeking, speed
changes and file boundaries need no special case because the player reports global time. Next
chapter text loads when the voice reaches the last 10 % of the current one. `prefers-reduced-motion`
turns smooth scroll into a jump. The current sentence is also underlined, so colour is not the only
signal; the screen-reader announcement is off by default (an option announces each new sentence).

**Auto-scroll and "lost the place?"**: by default (steady) the current line stays about one third from
the top; gentle scrolls only when it leaves the middle band of the screen. A
user scroll or touch pauses auto-scroll and shows the **Back to the voice** button; tapping it (or 8 s
without scrolling, if "return automatically" is on) jumps back to the current sentence.

**Reader settings** (`user_settings` key `reader.v1`, also cached locally for offline): font (Fraunces,
Fira Sans, OpenDyslexic: vendored, self-hosted; [decision 0002](decisions/0002-design-handoff.md)), size 80-250 %, line
spacing 1.2-2.2, margins narrow/normal/wide, theme light/sepia/night/high contrast (each checked at
4.5:1 including both highlight colours), highlight style word/sentence/both/none, auto-scroll on/off,
follow style steady (default)/gentle, return automatically on/off. Dyslexia fonts are a preference, not a claim:
research on their benefit is mixed, while spacing and size matter more.

**Bookmarks, highlights, notes in both directions.** A bookmark made while listening has a time; in
the text it shows at the sentence aligned to that time. A bookmark made in the text stores the sentence
and its start time. A **highlight** is a text range (start/end sentence ID and offset, plus a quote
with 32 characters of context on each side) **and** a time range taken from the alignment when it was
made (`alignment_id` recorded); "play this highlight" plays exactly that range. A note can be attached
to a highlight or to a time, as today. A highlight in a gap has no time; it is still kept.

## 4. Sharing and privacy (`internal/share`)

- New action `read_text` (chapter text, timing, images, reader page) and `download_text` (original file,
  overlay export; requires `can_download` too). `grants.can_read` and `share_links.can_read` default 0:
  sharing a book shares listening only unless the owner ticks **Also let them read along**. Owner and admin
  implicitly have both. A user without `read_text` gets `not_found` for every text route, and the
  reader link is not rendered.
- Upload, pairing, alignment, export: owner and admin only.
- Highlights, notes and reader settings are visible only to their user, through the UI and the API,
  admins included (the owner can of course read the database file; the UI says so on `/me`).
- Share-link guests with `can_read` read along with progress in local storage only, like listening.

## 5. Offline and desktop

"Download" (P4) also stores, when the user has `read_text` and the edition is aligned: every chapter
fragment, its timing JSON and images, keyed by alignment ID, in Cache Storage. Highlights made offline
queue in IndexedDB with bookmarks and notes. Wails desktop uses the same pages; personal mode runs the
alignment job locally when a runtime is checked.

## 6. EPUB3 Media Overlays

- **Import** (R6): an EPUB with `media-overlay` manifest items is read with its SMIL `<par>` pairs. They
  are used directly (alignment `source='overlay'`, no model) when the overlay's audio matches the paired
  book's audio: equal SHA-256, or the same number of files with each duration within 0.5 s in order.
  Otherwise the overlays are ignored and Tendshelf aligns itself. Overlay text fragments are mapped to
  sentence IDs by element ID; a fragment that spans several sentences times them as one group.
  An EPUB whose overlays point at audio inside the EPUB and that has no separate audiobook ("an ebook
  with its own narration") is listed in the inbox as not yet supported.
- **Export** (R6): job `align.export_overlays` writes a new EPUB 3.3 to `<data>/exports/` built from the
  canonical document (not by editing the original XHTML: sentences can cross elements), with one SMIL
  per chapter at sentence level, `media:duration` metadata and the book's audio stream-copied when it
  is MP3 or AAC, otherwise AAC 128 kbit/s via ffmpeg. Needs `download_text`; removed after 7 days.
  The publisher's own styling is not kept (named limitation).

## 7. Tests (synthetic, no TTS, no real books)

- **Parsers**, table-driven on fixtures built by the tests: EPUB 2, EPUB 3, EPUB 3 with overlays, each
  DRM marker (refused) and font obfuscation only (accepted), PDF with text layer, PDF with `/Encrypt`,
  image-only PDF, TXT, Markdown, DOCX, password-protected DOCX, HTML, FB2, MOBI uncompressed and LZ77,
  MOBI with encryption flag, AZW3, KFX. Hostile files: zip bomb, path traversal names, entity
  expansion, oversized entries, parser panic.
- **Golden**: canonical document bytes and IDs stable; segmenter and normaliser tables (EN/ES numbers,
  abbreviations, quotes, `¿¡`).
- **Pairing**: folder, upload, ISBN, title + author, two candidates (suggestion only), manual override.
- **Aligner on synthetic emissions**: a generator turns (text, known per-word start/end, pauses, rate
  changes) into CTC emission matrices with peaked tokens plus noise. Cases: clean; publisher intro and
  credits; front matter not read; two chapters swapped; 20 % of paragraphs removed (abridged); 5 % of
  words substituted plus inserted sentences (other edition); unsupported characters; 3 minutes of
  music. **Metric**: share of narrated sentences whose start is within ±250 ms of truth. Targets:
  clean at least 99 %, each perturbed case at least 95 %, and zero timed sentences that are absent from
  the audio, zero out-of-order times. uint8 against float emissions: boundaries equal within one frame.
- **Job**: kill at every stage and resume without recomputing finished windows; cancel; runtime
  identity change holds the job; editing one chapter re-aligns only it with zero worker calls.
- **Worker lifetime** with a fake worker (Go test binary): ready line, identity mismatch, crash during
  a request, idle stop, parent death.
- **Handlers** (`httptest`), every text route by role, including a guest without `can_read` (404 for
  page, fragment, timing, image, original) and user B never seeing user A's highlights.
- **Position**: a text-origin update stores the sentence start; a gap sentence keeps the text location
  and resumes audio at the previous timed sentence's end; newest wins between devices.
- **Overlays**: import from a fixture whose audio matches (used) and does not (ignored); export then
  re-import gives the same times within 1 ms. Opt-in `TENDSHELF_TEST_EPUBCHECK` runs epubcheck.
- **Real model, opt-in** (`TENDSHELF_TEST_ALIGN_RUNTIME`): a public-domain LibriVox recording with its
  Project Gutenberg text and 10 minutes of hand-checked sentence starts, prepared under
  `.local/tests/align-accuracy/<run-id>/`, never committed. Records the ±250 ms share and CPU time per
  audio hour. Target (guess): at least 95 %.
- **Browser**: the highlight script is checked with the Chromium capture helper (P0b) and a recorded
  manual check at 390, 768, 1024 and 1440 px.

## Not handled (and why)

Standalone ebook library and comics (the product is listening; text serves it). OCR of scanned PDFs (a
large model for a rare case). One-click runtime download (needs a second outbound path; decision 0001).
EPUBs with their own built-in narration (needs archive-backed audio in the scanner and streamer).
Translation or bilingual side-by-side reading. Search inside the text. Shared or public highlights.
Keeping the publisher's styling in the overlay export. Fixed-layout EPUB read-along (shown as pages).
