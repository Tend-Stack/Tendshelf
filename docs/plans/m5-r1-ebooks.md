# M5 R1 Ebook ingestion: plan

Sources: `docs/read-along.md` §1 and §7, decision 0001, roadmap "M5 Read along" R1, handoff "Read-along
reader", boards `screens/reader/AddEbook*.png` and `ProtectedPhone.png`. PreparingPhone is R3 and not built here.
Exit (roadmap): every parser and refusal fixture passes; the same file gives a byte-identical document;
hostile-file tests pass; the Text page lists chapters and first lines.

## Packages and order

| Package | Runs | Shared files it touches |
| --- | --- | --- |
| E1 Data, document, core parsers | first, alone | `go.mod`/`go.sum`, `NOTICES.md`, `docs/architecture.md`, `AGENTS.md` (approved list), `internal/db/db.go`, roadmap |
| E2 PDF and MOBI/AZW3 | after E1, in parallel with E3 (and E4) | `go.mod`/`go.sum`, `NOTICES.md`, `docs/architecture.md`, `AGENTS.md`, roadmap |
| E3 Scanner, upload store, job | after E1, in parallel with E2 | `internal/media/formats.go`, `internal/scan/scan.go`, `cmd/tendshelf/main.go`, `cmd/tendshelf/hooks.go`, roadmap |
| E4 Text page | after E3 (may overlap E2) | `internal/web/server.go`, `book_views.go`, `routes_book.go`, `routes_jobs.go`, `static/css/bundle.css`, roadmap |

At most two run at once. File ownership inside `internal/ebook`: E1 owns everything except `pdf*.go`,
`mobi*.go`, `ebooktest/pdf.go`, `ebooktest/mobi.go` (E2) and `service.go`, `job.go`, `reconcile.go`,
`upload.go` and their tests (E3). Roadmap: each package appends its own status paragraph under R1; the
orchestrator resolves the merge. Test names: E1 `TestEbook*` and `TestMigration0016*`, E2 `TestPDF*`
and `TestMOBI*`, E3 `TestIngest*` and `TestScanEbook*`, E4 `TestTextTab*`.

## Invariants (all packages)

1. Library ebook files are opened read-only (`os.Open`) and never written, renamed or deleted. Uploaded
   bytes are written once to `<data>/ebooks/<sha256>.<ext>` (dir 0700, file 0600) and never changed.
2. Copy protection is detected before any content is parsed; a protected file is refused with
   `refused_reason='drm'`, never decrypted, and an upload's bytes are deleted on any refusal.
3. All parsing is bounded (Limits below), runs under a 60 s context, inside `recover()`; zip and EPUB
   entry names are resolved with `path` on zip names only, never joined to the file system.
4. Same bytes + same recipe give byte-identical JSON and identical IDs; no map iteration, clock, random
   value or file path reaches the document.
5. A document is immutable and content-addressed: `<data>/text/<doc_sha256>.json.gz`, `doc_sha256` =
   SHA-256 of the uncompressed JSON.
6. Every Text route goes through `share.Can`: reading needs `share.ReadText`, adding or clearing needs
   `share.EditBook` (owner and admin only). Anything refused answers 404 like a missing book.
7. An ebook never becomes a book: ebooks live only in `ebook_files`/`editions`; scan grouping and
   probing never see them (no ffprobe on ebooks).
8. A job result is written only if the file row still has the quick hash the job read, inside a
   `t.Fence` transaction.

## Limits (`internal/ebook/limits.go`, `DefaultLimits`)

MaxFileBytes 200 MiB; MaxEntries 10,000; MaxTotalUncompressed 1 GiB (counted from bytes actually read,
not headers); MaxEntryBytes 64 MiB; MaxRatio 100 for entries whose uncompressed size is over 1 MiB (the
floor stops small, highly compressible XML from tripping it); ParseTimeout 60 s; MaxWords 2,000,000;
MaxXMLDepth 256; MaxBlocks 500,000. Over a limit: `refused` with reason `damaged` (codes `zip_entries`,
`zip_total`, `zip_bomb`, `entry_too_big`, `xml_depth`, `timeout`, `parser_panic`) or `too_large`
(`file_too_big`, `too_many_words`).

## Migration `internal/db/migrations/0016_ebooks.sql` (E1)

```sql
-- 0016_ebooks: read-along R1 (docs/read-along.md §1). ebook_files is every ebook file Tendshelf knows:
-- found in a library folder (origin 'library', read-only, path set) or uploaded on a book's Text page
-- (origin 'upload', bytes at <data>/ebooks/<sha256>.<ext>, path NULL). book_id is the pairing (NULL =
-- unpaired, waits for the R2 inbox). editions is one canonical document read from one file's exact
-- bytes by one recipe; the document is <data>/text/<doc_sha256>.json.gz. edition_chapters lists chapters.
CREATE TABLE ebook_files (
  id             TEXT PRIMARY KEY,
  origin         TEXT NOT NULL CHECK (origin IN ('library','upload')),
  library_id     TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_id      TEXT REFERENCES library_folders(id) ON DELETE CASCADE,
  path           TEXT UNIQUE,
  book_id        TEXT REFERENCES books(id) ON DELETE SET NULL,
  paired_by      TEXT CHECK (paired_by IS NULL OR paired_by IN ('folder','upload','isbn','title','person')),
  ext            TEXT NOT NULL CHECK (ext IN ('epub','pdf','txt','md','markdown','html','htm','xhtml','docx','fb2','fb2.zip','mobi','azw3','azw','kfx','kfx-zip')),
  size           INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns       INTEGER NOT NULL DEFAULT 0,
  quick_hash     TEXT NOT NULL CHECK (length(quick_hash) = 64),
  sha256         TEXT CHECK (sha256 IS NULL OR length(sha256) = 64),
  format         TEXT CHECK (format IS NULL OR format IN ('epub','pdf','txt','md','html','docx','fb2','mobi','azw3')),
  state          TEXT NOT NULL CHECK (state IN ('pending','ready','refused','failed')),
  refused_reason TEXT CHECK (refused_reason IS NULL OR refused_reason IN ('drm','unsupported','damaged','too_large','no_text')),
  error_code     TEXT,
  job_id         TEXT,
  original_name  TEXT NOT NULL DEFAULT '',
  uploaded_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
  missing_since  INTEGER,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  CHECK ((origin = 'library') = (path IS NOT NULL)),
  CHECK ((path IS NULL) = (folder_id IS NULL)),
  CHECK (origin = 'library' OR sha256 IS NOT NULL),
  CHECK ((state = 'refused') = (refused_reason IS NOT NULL))
) STRICT;
CREATE INDEX ebook_files_book ON ebook_files(book_id) WHERE book_id IS NOT NULL;
CREATE INDEX ebook_files_folder ON ebook_files(folder_id) WHERE folder_id IS NOT NULL;
CREATE INDEX ebook_files_quick_hash ON ebook_files(quick_hash);
CREATE INDEX ebook_files_pending ON ebook_files(state) WHERE state = 'pending';
CREATE UNIQUE INDEX ebook_files_upload_once ON ebook_files(book_id, sha256) WHERE origin = 'upload' AND book_id IS NOT NULL;

CREATE TABLE editions (
  id            TEXT PRIMARY KEY,
  ebook_file_id TEXT NOT NULL REFERENCES ebook_files(id) ON DELETE CASCADE,
  source_sha256 TEXT NOT NULL CHECK (length(source_sha256) = 64),
  recipe        TEXT NOT NULL CHECK (length(recipe) > 0),
  doc_sha256    TEXT NOT NULL CHECK (length(doc_sha256) = 64),
  doc_bytes     INTEGER NOT NULL CHECK (doc_bytes > 0),
  format        TEXT NOT NULL CHECK (format IN ('epub','pdf','txt','md','html','docx','fb2','mobi','azw3')),
  layout        TEXT NOT NULL DEFAULT 'flow' CHECK (layout IN ('flow','page')),
  title         TEXT NOT NULL DEFAULT '',
  subtitle      TEXT NOT NULL DEFAULT '',
  authors_json  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(authors_json) AND json_type(authors_json) = 'array'),
  language      TEXT NOT NULL DEFAULT '',
  isbn          TEXT NOT NULL DEFAULT '',
  asin          TEXT NOT NULL DEFAULT '',
  chapter_count INTEGER NOT NULL CHECK (chapter_count >= 0),
  word_count    INTEGER NOT NULL CHECK (word_count >= 0),
  char_count    INTEGER NOT NULL CHECK (char_count >= 0),
  page_count    INTEGER NOT NULL DEFAULT 0 CHECK (page_count >= 0),
  created_at    INTEGER NOT NULL,
  UNIQUE (ebook_file_id, source_sha256, recipe)
) STRICT;
CREATE INDEX editions_doc ON editions(doc_sha256);

CREATE TABLE edition_chapters (
  edition_id     TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  idx            INTEGER NOT NULL CHECK (idx >= 1),
  title          TEXT NOT NULL DEFAULT '',
  front          INTEGER NOT NULL DEFAULT 0 CHECK (front IN (0,1)),
  first_line     TEXT NOT NULL DEFAULT '',
  first_block_id TEXT NOT NULL DEFAULT '',
  block_count    INTEGER NOT NULL CHECK (block_count >= 0),
  word_count     INTEGER NOT NULL CHECK (word_count >= 0),
  char_count     INTEGER NOT NULL CHECK (char_count >= 0),
  src            TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (edition_id, idx)
) STRICT, WITHOUT ROWID;
```
Pairing lives on the file (survives "read again" into a new edition). The current edition of a file is
the one with `source_sha256 = ebook_files.sha256 AND recipe = ebook.Recipe`. `paired_by` lists R2's
values now because SQLite cannot widen a CHECK later. No CHECK ties `paired_by` to `book_id`: book
deletion sets `book_id` NULL and must not fail. `char_count` is in UTF-16 units.

## Canonical document `tendshelf-doc-v1` (E1, `internal/ebook/doc.go`)

`Recipe = "tendshelf-ebook-v1"`, `Segmenter = "tendshelf-segment-v1"`. Go structs, encoded with
`json.Encoder`, `SetEscapeHTML(false)`, no indent, trailing newline removed. Field order is the struct order:
```
Doc{V int "v"; Recipe; Format; SourceSHA256 "sourceSha256"; Layout "layout" ("flow"|"page");
    Meta{Title, Subtitle, Authors []string, Language, Identifiers{ISBN "isbn,omitempty", ASIN "asin,omitempty"}};
    PageCount "pageCount"; Chapters []Chapter}
Chapter{Idx (1-based); Title; Front bool; Src; Blocks []Block}
Block{ID "c{idx}.p{n}" (n 1-based); Kind: p|h|quote|li|img|note|pre|hr; Level int omitempty (h: 1-6);
      Text; Img string omitempty (zip path); Runs [][start,end,style] (style em|strong|code, omitempty);
      Sentences [][start,end]; Words [][start,end]; Src *SrcRef omitempty{Part, Start, End, Map [][canon,byte]}}
```
Offsets are UTF-16 units in `Text`, half-open. Sentence IDs `c{idx}.p{n}.s{k}` (k 1-based) are derived,
not stored; a word's ID is its 0-based index inside its sentence. Gzip: `gzip.NewWriterLevel(w,
gzip.BestCompression)` with a zero `Header` (no name, no ModTime). Written to a temp file in the same
directory, fsync, rename; an existing file of that name is kept as is.

Normalisation before segmenting (`normalize.go`): NFC (`golang.org/x/text/unicode/norm`); drop U+00AD,
U+200B, U+FEFF; collapse runs of ASCII whitespace, tab and newline into one space (not in `pre`); trim.
Segmenter (`segment.go`): a sentence ends at `.`, `!`, `?`, `…` or a run of them, plus any closing `” ’ " ' ) ] »`,
when followed by whitespace and then an uppercase letter, digit, opening quote or bracket,
`¿ ¡ — –`, or the end of the block. It never ends after a listed abbreviation (case-insensitive: mr mrs
ms dr st sr sra srta jr prof vs etc e.g i.e no vol ch fig p pp approx ud uds dña) or a single capital
initial. Headings, `img`, `hr` and blocks with no ending are one sentence. Words are maximal runs of
letters, marks and digits joined by an inner `'`, `’`, `-` or `‐`.

Front matter (`front.go`): `front=true` when (a) the source marks the chapter as cover, titlepage,
halftitlepage, copyright-page, imprint, toc, dedication or frontmatter, or (b) its normalised title is one
of: contents, table of contents, copyright, dedication, title page, índice, contenido, dedicatoria,
derechos, or (c) it comes before the first chapter with at least 300 words and has fewer than 120 words.
Empty blocks are dropped except `img` and `hr`; empty chapters are dropped; no chapter left gives a
`no_text` refusal with code `empty`. `pageCount` is the EPUB page-list length when one exists, PDF pages for
PDF, otherwise ceil(total chars / 1800).

## E1 Data, document, core parsers (about 250 calls)

Decisions: in-memory bounded zip (`safezip.go`), not `internal/upload/zip.go`: that one extracts to
disk under quota rules for book uploads; the ebook reader must never touch disk. Parsers share one
`builder` so IDs, offsets and front rules exist once. `x/net/html` Tokenizer (not `Parse`) so byte offsets
for `src.map` are known. FB2 and HTML charsets via `golang.org/x/text/encoding/htmlindex` and
`golang.org/x/net/html/charset`: same approved modules, needed for windows-1251 FB2.

Steps:
1. `internal/db/migrations/0016_ebooks.sql`: the SQL above. `internal/db/db.go`: `SchemaVersion = 16`.
2. `internal/db/migration_0016_test.go`: `TestMigration0016Ebooks` (open v15 via `ms[:15]`, `seed7`,
   reopen; books row kept; insert a library ebook_files row for the seeded book, an edition and a chapter;
   delete the book and check the file row stays with `book_id` NULL) and `TestMigration0016FromEmpty`
   (tables exist; an `origin='upload'` row with NULL sha256 is rejected; `refused` without reason is rejected).
3. `go get golang.org/x/net@v0.61.0 golang.org/x/text@latest` (direct); `NOTICES.md` rows (BSD-3-Clause,
   purpose); `docs/architecture.md` rows marked in use; `AGENTS.md` "Approved and in use" list extended.
4. `internal/ebook/limits.go`, `errors.go`: `Limits`, `DefaultLimits()`, `type Refusal struct{Reason, Code
   string}` (implements `error`), reasons `ReasonDRM="drm"`, `ReasonUnsupported`, `ReasonDamaged`,
   `ReasonTooLarge`, `ReasonNoText`.
5. `internal/ebook/ext.go`: `ExtOf(name) string` (lower case; recognises `fb2.zip` and `kfx-zip`; returns
   "" for anything else in the migration's ext list), `FormatLabel(format) string` (EPUB, PDF, Word,
   FB2, MOBI, AZW3, Plain text, Markdown, HTML).
6. `internal/ebook/doc.go`, `normalize.go`, `segment.go`, `front.go`, `builder.go`: as specified. Builder
   API: `newBuilder(format string, lim Limits)`, `Chapter(title, src string, kinds []string)`,
   `Block(kind string, level int, segs []seg, src *SrcRef)` with `seg{Text, Style string; ByteOff int}`,
   `Image(zipPath, alt string, src *SrcRef)`, `Finish(meta Meta, pageCount int) (*Doc, error)`;
   `Encode(*Doc) ([]byte, error)`; `WriteDoc(dir string, json []byte) (sha string, err error)`.
7. `internal/ebook/safezip.go`: `openZip(ra io.ReaderAt, size int64, lim Limits) (*zipBox, error)` (checks
   entry count, header total, ratio, encrypted flag bit 0 → `drm`/`zip_encrypted`); `(*zipBox).Read(name)`
   (exact name, else a single case-insensitive match; actual-bytes budget; per-entry cap).
8. `internal/ebook/xmlsafe.go`: `newXMLDecoder(r)`: `Strict=false`, `Entity=xml.HTMLEntity`,
   `CharsetReader` via htmlindex, depth counter giving `xml_depth`.
9. `internal/ebook/sniff.go`, `drm.go`: magic first (`PK\x03\x04` zip: `mimetype`/`container.xml` → epub,
   `word/document.xml` → docx, single `.fb2` entry → fb2; `%PDF-` → pdf; `BOOKMOBI` at offset 60 → mobi/azw3;
   `D0 CF 11 E0 A1 B1 1A E1` → `drm`/`ole_office` when ext is docx, else `unsupported`; `CONT` or
   `\xeaDRMION\xee` → `drm`/`kfx`); ext `kfx`/`kfx-zip` → `drm`/`kfx`; otherwise by ext (txt, md, html, fb2).
   EPUB DRM: `META-INF/rights.xml` (`adept`), any `sinf.xml` (`apple`), `META-INF/license.lcpl` (`lcp`),
   `META-INF/encryption.xml` with any algorithm other than `http://www.idpf.org/2008/embedding` and
   `http://ns.adobe.com/pdf/enc#RC` (`epub_encryption`).
10. `internal/ebook/htmlblocks.go`: tokenizer walk shared by EPUB, HTML and (E2) MOBI: p, div with text,
    h1-h6, blockquote, li, pre, hr, img (alt as text; svg → its title/alt), aside or section with
    `epub:type` footnote/endnote/rearnote → note; em/i → em, strong/b → strong, code → code; script,
    style, head, link and remote `src` dropped. Records `src.part`, element byte start/end, map entries
    at each text token.
11. `internal/ebook/epub.go`: container.xml → OPF (first rootfile with `application/oebps-package+xml`);
    meta: first dc:title, creators without role or role `aut` in order, dc:language, identifiers (ISBN-10/13
    with valid check digit after stripping `urn:isbn:`; ASIN `^B0[0-9A-Z]{8}$`); linear spine items are
    chapters in order; title from EPUB3 nav (`nav[epub:type=toc]`) or NCX by href without fragment,
    else first h1-h3; landmarks give the front kinds; page-list length gives pageCount. A href that
    resolves outside the zip root (`..`, leading `/`) is skipped. Codes `no_container`, `no_spine`.
12. `internal/ebook/html.go` (one file; title from `<title>`, author from `meta[name=author]`; chapters
    by h1 when there are two or more, else h2, else one), `text.go` (txt and md: blank-line paragraphs;
    md `#` headings, `*`/`_` em, `**` strong, `-`/`*`/`1.` items, `>` quote, fenced pre, `---` hr; tables and
    raw HTML as plain text; chapters by md headings or a lone line ≤ 60 chars matching
    `(?i)^(chapter|capítulo|capitulo|part|parte|book|libro)\s+(\d+|[ivxlcdm]+|\pL+)\b`; invalid UTF-8
    decoded as Windows-1252, UTF-16 with a BOM decoded), `docx.go` (styles.xml map; chapter at
    Heading1/Title/outline level 0, Heading2-6 → h; w:i/w:b runs; w:tab and w:br → space; footnotes from
    `word/footnotes.xml` appended as note blocks to the chapter that first cites them; title and creator
    from `docProps/core.xml`), `fb2.go` (title-info book-title, authors, lang, publish-info isbn; top
    sections of the first body without `name` are chapters, nested section titles → h; p, emphasis,
    strong, cite/epigraph → quote, v → p, image → img; `body name="notes"` → one last chapter "Notes" of note
    blocks; `.fb2.zip` holds exactly one `.fb2`).
13. `internal/ebook/pdf.go`, `mobi.go`: stubs returning `&Refusal{ReasonUnsupported, "not_yet"}` (E2
    replaces both files whole).
14. `internal/ebook/read.go`: `Read(ctx, path, ext string, lim Limits) (*Doc, error)`: stat size cap, open
    read-only, stream SHA-256, 60 s context, `recover()` → `damaged`/`parser_panic`, sniff, DRM check, parse,
    `Finish`. Test hook `var parseHook func(format string)` called before parsing.
15. `internal/ebook/store.go`: types `File`, `Edition`, `ChapterRow` (fields = columns), and
    `NewStore(*db.Store)`, `FilesForBook(ctx, bookID)`, `File(ctx, id)`, `CurrentEdition(ctx, fileID)
    (Edition, bool, error)`, `Chapters(ctx, editionID)`, `ReadyForBook(ctx, bookID) (bool, error)`.
16. `internal/ebook/ebooktest/`: `zip.go` (copy of the `internal/upload/zip_test.go:26-46` builder,
    fixed Modified time, raw-header option for lying sizes, encrypted flag), `epub.go` (`EPUB(t,
    EPUBOptions)`: Version 2|3, Title, Authors, Lang, ISBN, Chapters{Title, EpubType, Paras}, PageList,
    Rights, Apple, LCP, EncryptAlg, FontObfuscation, Extra entries), `docx.go` (`DOCX`, `OLE`), `fb2.go`
    (`FB2` with Encoding, `FB2Zip`), `hostile.go` (`ZipBomb`, `ManyEntries`, `TraversalEPUB`,
    `EntityEPUB` with a billion-laughs DOCTYPE, `LyingSizeEPUB`). All text is invented in the code.

Tests (`internal/ebook/*_test.go`): `TestEbookParsers` (table: EPUB2, EPUB3, EPUB3 page-list, TXT, MD,
HTML, DOCX, FB2 utf-8, FB2 windows-1251, FB2 zip; checks title, authors, chapter titles, block kinds,
first block text), `TestEbookRefusals` (rights.xml, sinf.xml, license.lcpl, encryption.xml AES, OLE docx,
KFX by ext and by magic, encrypted zip entry → drm; font obfuscation only → accepted), `TestEbookHostile`
(zip bomb, 10,001 entries, traversal names not read, entity file finishes under 2 s without expansion,
lying size, oversize file via `Limits`, parser panic via `parseHook`, deadline via a 1 ms `ParseTimeout`),
`TestEbookDeterministic` (read each fixture twice: equal JSON and SHA), `TestEbookGolden` (EPUB3 and MD
fixtures against `internal/ebook/testdata/golden/*.json`, `-update` flag rewrites), `TestEbookSegmenter`
(EN/ES table: abbreviations, initials, `…`, `?!`, `¿¡`, closing quotes, UTF-16 offsets with emoji),
`TestEbookWords`, `TestEbookFront`, `TestEbookNormalize`, `TestEbookStore`.
Gates: `go test -race ./internal/ebook/... ./internal/db/...`, then `make check` and `make build`.
Out of scope: PDF, MOBI (E2); scan, upload, job (E3); any route (E4); SMIL overlays (R6); images served.

## E2 PDF and MOBI/AZW3 (about 200 calls)

Decision, PDF reader: **recommend `github.com/ledongthuc/pdf`** (pure Go, BSD-3-Clause; gives per-glyph
font size and position, which the reflow check needs). An own reader means xref streams, object
streams, filters, encodings and ToUnicode CMaps: weeks, not a package. Choose on fixtures: step 2 runs
the module on the four spike fixtures; if any fails, commit what is green and report
`PLAN GAP: ledongthuc fails <fixture>` (the operator then decides on an own reader). The module takes no
context: the parser checks `ctx` between pages; a single hostile page can outlive the deadline in its
goroutine (accepted limit, bounded by the 200 MB cap). MOBI/AZW3: own reader, DRM-free only.

Steps:
1. `go get github.com/ledongthuc/pdf@<latest>`; read its LICENSE; `NOTICES.md` row; architecture row and
   `AGENTS.md` list updated.
2. `internal/ebook/ebooktest/pdf.go`: `PDF(t, PDFOptions)`: own tiny writer (PDF 1.4, Helvetica
   WinAnsi, Flate content streams). Options: Pages [][]Line{Text, X, Y, Size}, Type0ToUnicode (Identity-H
   with a ToUnicode CMap), XRefStream (object streams), Incremental (an appended update), TwoColumns,
   ImageOnly, Encrypt (standard handler dict in the trailer), Header, Footer, PageNumbers. Spike fixtures:
   plain, Type0ToUnicode, XRefStream, Incremental.
3. `internal/ebook/pdf.go` (replaces the stub): `/Encrypt` in the trailer, or the module's password error
   → `drm`/`pdf_encrypt` before reading text. Lines from glyph runs per page; drop a line that recurs
   (digits normalised to `#`) in the top or bottom 8 % of at least half the pages (minimum 3 pages), and
   lone page numbers (digits or roman); join lines into paragraphs; a line ending in letter + `-` followed by
   a lowercase start is joined without the hyphen; body size = the size with most characters; a line
   at ≥ 1.3× body is `h`, and at ≥ 1.5× or matching the chapter regex of E1 it starts a chapter. Reflow check
   fails → `layout='page'` when over 30 % of lines start beyond 45 % of page width, or over 50 % of lines are
   shorter than 40 % of the median line width. No text: fewer than 200 letters total or under 20 per
   page on average → `no_text`/`image_only`. `src.part = "page:{n}"`, no map. pageCount = pages.
4. `internal/ebook/ebooktest/mobi.go`: `MOBI(t, MOBIOptions)`: PalmDB header, record 0 (PalmDOC
   compression 1 or 2, encryption 0/1/2, MOBI header length 232, encoding 65001, version 6 or 8, EXTH
   100/503/104/113, extra-data flags with trailing entries), text records via a test LZ77 encoder that
   emits literals, `0x01-0x08` runs, back references and space+char bytes; `AZW3` = version 8 with
   KF8 markup; `Combined` = MOBI6 part + BOUNDARY + KF8 part (EXTH 121).
5. `internal/ebook/mobi.go` (replaces the stub): PalmDB record table (bounds-checked offsets); encryption
   type 1 or 2 → `drm`/`mobi_encrypted`; compression 17480 (HUFF/CDIC) → `unsupported`/`huffcdic`;
   strip trailing entries per extra flags (bits 1-15: backward varint sizes, then bit 0: `(last&3)+1`);
   PalmDOC LZ77 decode with output capped at text length; KF8 part when EXTH 121 is present; decode
   cp1252 or UTF-8 by encoding field; rawML through E1's `htmlblocks` with `mbp:pagebreak` and h1/h2
   starting chapters; meta from EXTH. No SKEL/FRAG rebuild (text order of rawML is used).
Tests: `TestPDFSpike` (four fixtures give the expected text), `TestPDFParse` (reflow, hyphen join,
headers/footers dropped, headings, chapters), `TestPDFLayoutPage` (two columns, short lines),
`TestPDFRefusals` (Encrypt → drm, image-only → no_text), `TestPDFDeterministic`, `TestMOBIParse`
(uncompressed, LZ77, AZW3, combined), `TestMOBIRefusals` (encryption 1 and 2 → drm, HUFF/CDIC →
unsupported), `TestMOBIHostile` (record offsets past the end, LZ77 back reference before start, text length
lie: all `damaged`, no panic), `TestMOBIDeterministic`. Manual check only: the operator's AZW3/EPUB in
`/home/rubirosa/Books` through `Read` from a scratch `go test -run` with an env path; nothing copied or
committed, no text in any capture.
Gates: `go test -race ./internal/ebook/...`, `make check`, `make build`.
Out of scope: OCR, HUFF/CDIC, opening the PDF in the browser's viewer (R4), AZW4, page images.

## E3 Scanner, upload store, job (about 220 calls)

Decisions: the scanner hands ebooks to a sink after its transaction, so `internal/scan` never imports
`internal/ebook` and grouping is unchanged. The scanner records only unambiguous ebook extensions
(epub, mobi, azw3, azw, fb2, kfx, kfx-zip); pdf, docx, txt, md and html in audiobook folders are usually
supplements or notes, so those come only by upload (operator default, see below). Book uploads
(decision 0015) keep skipping ebook files: `upload.kept` already returns false for them, so
`internal/upload` is unchanged. Uploads on the Text page are one plain multipart POST (≤ 200 MB), not
the resumable upload machinery: a plain form works without JavaScript.

Steps:
1. `internal/media/formats.go`: `KindEbook` (comment: recorded as a text edition, never probed); kinds
   for the seven scanner extensions. Every other `Classify` caller compares explicitly and is unchanged.
2. `internal/scan/ebooks.go`: `type EbookFound struct{Path, RelDir, Name string; Size, MtimeNs int64}`,
   `type EbookFolder struct{FolderID, Path string; Files []EbookFound}`, `type EbookScan struct{LibraryID,
   Scope string; Skip []string; Folders []EbookFolder}`, `type EbookSink func(context.Context, EbookScan)`,
   `(*Scanner).OnEbooks(EbookSink)` (before Register).
3. `internal/scan/scan.go` `scanScoped`: right after `walk`, move `KindEbook` entries out of `files`
   (before the "suddenly empty" check, so its behaviour is unchanged); keep them per walked folder in
   `plan.ebooks`; after `UpdateGuarded` succeeds, call each sink before `enqueueProbes`. Skipped folders
   are not passed.
4. `internal/ebook/service.go`: `New(st *db.Store, lib *library.Repo, runner *jobs.Runner, dataDir
   string, log *slog.Logger) *Service`, `Register(*jobs.Runner)`, `ScanSink() scan.EbookSink`,
   `Finisher() scan.Finisher` (calls `EnqueuePending`), `Store() *Store`.
5. `internal/ebook/reconcile.go`: per folder, rows `WHERE folder_id=?` (and under `Scope` when set). Seen
   path: same size and mtime → clear `missing_since`; else quick hash; changed hash → `state='pending'`,
   `sha256` NULL, enqueue. New path: a row of the same library with equal quick hash and size that is
   missing or not seen in this walk is moved (path, folder); else insert `pending`. Unseen rows not under
   a `Skip` prefix get `missing_since=now`. Folder pairing for rows with `book_id` NULL: live books of the
   library whose `root_path` equals the ebook's directory, or whose `root_path` is an audio file in that
   directory; exactly one → `paired_by='folder'`; none → try the parent directory once; several → leave
   unpaired. Never unpairs, never overrides a non-folder `paired_by`. Errors are logged; the scan goes on.
6. `internal/ebook/job.go`: `KindIngest = "ebook.ingest"`, payload `{"file_id"}`, Concurrency 1,
   MaxAttempts 2. Not pending → done. Path = row path or `<data>/ebooks/<sha256>.<ext>`; library file gone
   → `failed`/`missing`. `Read`; refusal → `refused`; for uploads delete the stored bytes unless another
   non-refused upload row has the same sha256. Success → `WriteDoc` into `<data>/text`, then one
   `WriteTx` with `t.Fence`: insert edition (`INSERT ... ON CONFLICT DO NOTHING`) and chapters
   (first_line = first p/quote/li text cut at a word boundary to 160 runes plus `…`), `UPDATE ebook_files
   SET state='ready', sha256, format ... WHERE id=? AND quick_hash=? AND state='pending'`; zero rows →
   discard (a newer job is queued). Other errors → `failed`/`read_error`, permanent `jobs.Error`.
   `EnqueuePending`: pending rows with no queued or running job, plus `refused` rows with code `not_yet`
   (reset to pending, so E2's parsers pick them up).
7. `internal/ebook/upload.go`: `AddUpload(ctx, book library.Book, userID, name string, body io.Reader)
   (File, error)`; `type UploadError struct{Status int; Code, Message string}`. Order: `ExtOf(name)` "" →
   422 `unsupported`; kfx/kfx-zip → 422 `copy_protected` (nothing read); free space of `<data>/ebooks` below
   200 MiB + 64 MiB → 507 `no_space` (`media.ErrNoSpaceInfo` passes); stream to
   `<data>/ebooks/.incoming/<id>.part` with SHA-256, cap 200 MiB + 1 → 413 `too_large`; 0 bytes → 422 `empty`;
   rename to `<sha>.<ext>` (or drop the temp when it exists); duplicate (unique index) → 409 `duplicate`;
   insert `origin='upload'`, `paired_by='upload'`, `original_name` = base name cut to 255 bytes; enqueue.
   `Dismiss(ctx, bookID, fileID)`: deletes an upload row in `refused` or `failed` of that book, and its bytes
   when unreferenced; else `ErrNotFound`. Temp files left by a crash are removed at `Register`.
8. `cmd/tendshelf/hooks.go`: `registerEbooks(d deps) (*ebook.Service, error)` (New, `OnEbooks`,
   `OnScanFinished(Finisher)`, Register); `cmd/tendshelf/main.go`: call it before `sc.Register`, then
   `EnqueuePending` once the runner starts.
Tests: `TestScanEbookSplit` (ebooks never reach groups or probes; a folder with only an epub makes no
book), `TestIngestReconcile` (new, unchanged, changed, moved, missing, skip prefix, scope), `TestIngestPairing`
(folder book, single-file book, parent dir, two candidates, no override of `upload`), `TestIngestJob`
(ready writes edition, chapters and gz doc; drm refusal; upload refusal deletes bytes; changed quick hash
discards the result; `not_yet` reset), `TestIngestUpload` (each error code and status; byte-identical
stored file, mode 0600; duplicate), `TestIngestDismiss`, `TestIngestReadOnly` (library fixture dir made
0500 and files 0400: ingest succeeds, mtimes unchanged).
Gates: `go test -race ./internal/ebook/... ./internal/scan/... ./internal/media/... ./cmd/...`, `make check`, `make build`.
Out of scope: R2 pairing rules (ISBN, title), the `/admin/ebooks` inbox, an orphaned-bytes sweep, re-reading
on a recipe change other than `not_yet`.

## E4 Text page (about 200 calls)

Decisions: the Text tab is a book tab (`GET /books/{id}/text` through the existing tab route), not a
standalone page, so the player stays and the board's content sits in `#book-panel`. Shown when the
person may `EditBook`, or may `ReadText` and the book has a ready edition. Readers see ready editions
only; editors also see pending, refused, failed and missing rows. The job poll reuses `GET /hx/jobs/{id}`.

Routes: `POST /books/{id}/text` (multipart field `file` plus the CSRF hidden field the other forms use;
htmx → 200 with the file row fragment that polls; plain → 303 to `/books/{id}/text`; an error →
the form fragment (htmx) or the full page (plain) with the code's status and message), `POST
/books/{id}/text/{fileID}/dismiss` (htmx → 200 empty body; plain → 303), `jobViews[ebook.KindIngest]`.
Each checks the right action on the book the file belongs to; any refusal or mismatch is 404.

Steps:
1. `internal/web/server.go`: `Options.Ebooks *ebook.Service`, field `ebooks`; `cmd/tendshelf/main.go` passes it.
2. `internal/web/ebook_copy.go`: strings below. `book_views.go`: `{"text", ebookTabLabel}` after
   chapters. `routes_book.go`: compute `canTextEdit`, `canTextRead`; delete the tab otherwise; `case "text"`.
3. `internal/web/routes_ebooks.go`: register the two POSTs (from `registerBook`), the job view, view
   builders. `internal/web/pages/book_text.templ` (`TextPanel`, `EbookEdition`, `EbookFileRow`,
   `EbookRefusal`, `EbookUploadForm`); first 12 chapters then "Show all" (`?all=1`), front chapters labelled.
4. CSS: `ts-ebook-*` classes in `bundle.css`, tokens only, four themes; report the gzip size.
5. `internal/web/routes_matrix_ebooks_test.go`: every new route by role.
Copy (handoff deck where it exists; new strings marked *): tab "Text"*; "Add an ebook"; "With the text and
the narration together, anyone you share this book with can read along, or just read."; "Found in your
folders"; "Or add a file"; "Choose an ebook file"* (deck: "Choose an EPUB file"); "EPUB, PDF, Word, FB2,
MOBI, AZW3, HTML, Markdown or plain text, up to 200 MB. No copy protection. Kept on your server."* (deck
says "next to the audio", untrue for uploads); "Choose"; "Add this ebook"*; "Found in {path}"; "Added from
this device"*; "{format} · {n} chapters · {n} words"*; "Front matter"*; "Show all {n} chapters"*; "Reading
{name}…"*; refusal card: "This ebook is copy-protected", "Tendshelf can only open ebooks without copy
protection, so it can't pair {name} with the audiobook. Nothing was changed.", "If the shop you bought it
from offers a copy without protection, add that one instead.", "Choose another file", "Why can't Tendshelf
open it?", "Copy protection locks a file to one shop's apps. Tendshelf never removes or works around it."*,
"{title} still plays as an audiobook."; other reasons*: "Tendshelf cannot read this kind of file.", "This
ebook is larger than Tendshelf reads.", "This file looks damaged, so Tendshelf did not open it.", "This
PDF is pictures of pages, so Tendshelf cannot read its words", "Tendshelf found no text in this file.",
"Tendshelf could not read this file. Try again later.", "This file is no longer in its folder.", "This
PDF keeps its page layout. Read along will show its words plainly.", "Clear"; upload errors: "Choose a file
first.", "That file is empty.", "This ebook is copy-protected, so Tendshelf will not open it.", "This file
is larger than 200 MB.", "This ebook is already on this book.", "The server's data folder is full."
Tests: `TestTextTabVisibility` (owner, admin, member and guest with and without `can_read`, with and
without a ready edition), `TestTextTabLists` (chapters and first lines; Show all; front label; refused
card; readers never see pending or refused rows), `TestTextTabUpload` (htmx and plain; each error code
and status; non-editor 404), `TestTextTabPoll`, `TestTextTabDismiss`, `TestTextTabNoJS` (form works
without htmx headers), matrix rows, and the opt-in capture `TestTextTabVisual` (390, 768, 1024, 1440; empty,
list, refused; dark and light; synthetic fixtures; seeded ids) beside AddEbook*/ProtectedPhone.
Gates: `make generate` (tree clean), the touched web tests, `make check`, `make build`, the CSS budget
test, `TENDSHELF_TEST_BROWSER=1` capture run.
Out of scope: Read and Read along modes, `/books/{id}/read`, serving images or the original file
(`text.download`), Pair / Not the same book (R2), Preparing read-along (R3), offline text (R5).

## Operator decisions (defaults apply if unanswered)

1. Scanner extensions: default epub, mobi, azw3, azw, fb2, kfx only; PDF, DOCX, TXT, MD and HTML only
   by upload (audiobook folders often hold supplement PDFs and notes).
2. PDF reader: default `github.com/ledongthuc/pdf`, kept only if the E2 spike fixtures pass.
3. Upload copy: default "Choose an ebook file" and the hint without "next to the audio".
