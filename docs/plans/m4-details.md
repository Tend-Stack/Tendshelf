# M4 Details: implementation plan (2026-11-03)

Roadmap: `docs/roadmap.md` "## M4 Details". The operator's scope is binding: from the Extractor
(`~/Projects/TenVocal-Stuff/Ecxtrator`, "the Extractor" below) port only what pulls book details in:
sidecar parsers, online providers, matching and scoring, and their tests. Nothing about clips, voice,
measuring or categories (decision 0007). The tag writer is built from Tendshelf's own design and does not port
`extractor_tags.py`.

## Shared decisions (all packages)

| # | Decision | Reason |
| --- | --- | --- |
| S1 | Precedence, highest first: `user`, `json` (metadata.json), `opf`, `txt` (desc.txt, reader.txt), `nfo`, `tags`, `folder`, `online`. This keeps one `metadata_values` row per source. | It is the Extractor's and Audiobookshelf's order (names < tags < nfo < txt < opf < metadata.json) with the user on top and online at the bottom. It differs from today's list only by adding `json`. |
| S2 | When someone applies an online value, Tendshelf always writes it as an `online` row. If the field already has a different, non-empty value, it also writes a `user` row with the same value. | An online value fills a gap and gives way to a later sidecar. An online value the person chose over existing data wins, like an edit. This is the Extractor's `merge_details` rule ("fills empty unless chosen"). |
| S3 | A lookup searches Audnexus by default: the Audible catalogue to find ASINs, then Audnexus books. A "Search more places" box adds Google Books, Open Library and iTunes. | Audnexus has the audiobook fields (narrators, series, ASIN), and contacting fewer third parties is better for privacy. The Extractor's default is the same. |
| S4 | `online.enabled` is on by default (a missing key means true). Only the owner can turn it off. When it is off, the Look up controls are hidden, the routes answer 404, and the jobs fail with `online_off`. | Each request already needs an explicit click from an owner or admin, so the click is the consent. Scans never go online. |
| S5 | Matching folds accents with a fixed Latin table (M4-2). Unicode letters and digits are kept, `&` becomes `and`, and anything else is dropped. Folding is used only to compare; stored values keep their accents. | The Extractor's `[^a-z0-9 ]` rule turns "Brontë" into "bront" and turns non-Latin titles into "", so every candidate gets the same score. |
| S6 | Any change to details, covers, people or files needs `share.EditBook` on the book (owner and admin; members and guests are refused in `canByGrant`). Author lookup needs `share.ManageLibraries` on `TargetServer` (the existing `s.canManage`). The online switch needs `share.ManageServer` (owner only). A refusal is a 404, as elsewhere. | These actions already exist, so no new action is added. |
| S7 | Lookups run as the `metadata.online` job. The lookup job also fetches the cover pictures of the top 3 candidates, so the compare sheet only shows cached bytes and never starts a fetch on its own. | All outbound traffic happens in the job that the click started. |
| S8 | Write-details is built last, as its own packages (M4-5, M4-7, M4-9). The other packages do not depend on it, so M4 can ship without it if it slips. | It is the riskiest part, because it is the only code that writes to library files. |
| S9 | Write-details uses hidden working names without an audio extension: the temporary file is `.<name>.tendshelf-new` and the backup is `.<name>.tendshelf-before`. This replaces the `<name>.before-tags.<ext>` name in the architecture. | The scan skips dot-files (`internal/scan/walk.go:40`). A visible `.m4b` backup would be picked up as a second part by Tendshelf and by other apps reading the same folder. |
| S10 | No new JavaScript. Sheets use the existing "page + `hx-select`" `#sheet` pattern, and polling uses `hx-trigger="every 2s"` on `/hx/jobs/{id}`. The "Use {n} changes" count is re-rendered by the server with `hx-get` on `change`. New CSS goes in `css/pages-details.css`, which gets the default 3 KB `pages-*` budget (`motion_test.go:131`) and no new row. | The JS file is at 38.7 KB of its 38 KB cap. |

Docs: builders do not edit `docs/`. Each package lists its doc edits under "Docs", and the
orchestrator or a scribe applies them when merging the package. The roadmap status follows the same rule.

Shared files and who owns them. Packages in the same group never touch the same file.

| File | Owner, by group |
| --- | --- |
| `NOTICES.md` | G1: M4-1 adds its row directly after the `names.go` row (line 16), and M4-2 adds its rows directly after the `production.go` row (line 19). The two hunks are kept apart on purpose. |
| `internal/db/db.go`, `internal/db/open_test.go`, `migrations/` | M4-1 (0010) in G1; M4-7 (0011) in G3 |
| `cmd/tendshelf/hooks.go`, `main.go`, `internal/web/server.go` (Options) | M4-1 in G1 (hooks only); M4-4 in G2; M4-7 in G3 |
| `internal/mediajobs/jobs.go` | M4-3 in G1; M4-7 in G3 |
| `internal/web/routes_book.go`, `routes_jobs.go`, `pages/book.templ`, `book_copy.go` | M4-6 in G3, M4-8 in G4, M4-9 in G5 |
| `internal/web/csrf.go` | M4-8 |
| `internal/web/static/css/*`, visual tests | M4-10 |

Test names: each package prefixes its package-level test names (for example `TestSidecar`, `TestOnline`, `TestCoverAdopt`,
`TestLookup`, `TestWriteTags`, `TestCompare`, `TestFileWrites`, `TestCoverPick`, `TestWriteUI`), so parallel worktrees never collide.

## Order and parallel groups (at most 3 at once)

| Group | Packages | Needs |
| --- | --- | --- |
| G1 | M4-1 Sidecars, M4-2 Online client, M4-3 Cover store | nothing |
| G2 | M4-4 Lookup service, M4-5 Write engine | M4-4 needs all of G1; M4-5 needs nothing |
| G3 | M4-6 Compare UI, M4-7 Write ledger | M4-6 needs M4-4; M4-7 needs M4-1 (0010) and M4-5 |
| G4 | M4-8 Cover and author UI | M4-6 |
| G5 | M4-9 Write UI | M4-7, M4-8 |
| G6 | M4-10 Styles and captures | everything |

Gates for every package: `make check` (gofmt, vet, `go test -race ./...` on the changed packages and their
dependents) and `make build`. A package with templ changes also runs `make generate` and must leave no diff.

---

## M4-1 Sidecars and precedence (Go: metadata, library, db)

Invariants
- Sidecars are read and never written. Only regular files are read; symlinks are skipped (`DirEntry.Type().IsRegular()`, and `Lstat` for loose-file sidecars). Each file is read only if it is at most 1 MiB; a larger one is ignored. Invalid UTF-8 goes through `strings.ToValidUTF8(s, "�")`.
- A source writes only its own rows. The job still never writes `user` rows.
- A book's sidecar signature changes when a sidecar file is added, removed or changed. A scan then queues `metadata.local` for that book, even when its audio files are unchanged.

Reused: `Service.Local` (`local.go:79-206`) and its desired-rows diff, `Resolve`, `cleanText`/`cleanASIN`/`cleanISBN`/`cleanYear`/`cleanSequence`, `ParseNames`, `ParseSeriesString`, `splitGenres`, `fillProduction`, and the Follower dedupe through `ActiveKeys`. Extended: the `Precedence` list, the `desired` map, and `registerDetails`.

Steps
1. `internal/db/migrations/0010_details.sql`:
   `CREATE TABLE book_sidecars (book_id TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE, signature TEXT NOT NULL, checked_at INTEGER NOT NULL) STRICT, WITHOUT ROWID;`
   `CREATE TABLE online_cache (provider TEXT NOT NULL CHECK (provider IN ('audnexus','audible','google','openlibrary','itunes')), query_key TEXT NOT NULL CHECK (length(query_key) = 64), response_json TEXT NOT NULL CHECK (json_valid(response_json)), fetched_at INTEGER NOT NULL, PRIMARY KEY (provider, query_key)) STRICT, WITHOUT ROWID;`
   `CREATE INDEX online_cache_fetched ON online_cache(fetched_at);`
2. `internal/db/db.go`: set `SchemaVersion = 10`. In `internal/db/open_test.go`, set `tablesAt[10]` and the seed the same way as for 0008/0009 ("filled in init below"). Seed v9 with a book, a `metadata_values` row and a progress row, and check that they survive.
3. `internal/metadata/values.go`: add `SourceJSON = "json"`. Set `Precedence = user, json, opf, txt, nfo, tags, folder, online` and update the package comment.
4. New `internal/metadata/sidecar.go`, with the top comment `// Ports the Extractor extractor_details.py (sidecars, parse_nfo, parse_opf, parse_metadata_json, local_details step 3); see NOTICES.md.`
   - `type sidecarFiles struct{ JSON, Desc, Reader, OPF, NFO string }` holds absolute paths, with "" meaning absent.
   - `findSidecars(rootPath string, single bool) sidecarFiles`. For a folder book, it reads `rootPath` with `os.ReadDir` and lower-cases the names. `metadata.json`, `desc.txt` and `reader.txt` are matched exactly. For `.opf` and `.nfo`, the first one in sorted name order wins. For a single-file book (`rootPath` is the file), it looks for `<stem>.opf`, `<stem>.nfo` and `<stem>.metadata.json` next to the file. `stem` is the file name without its last extension.
   - `parseNFO(text string) Details` ports `extractor_details.py:328-376`. "Title: A: B" is split into title and subtitle. Author and narrator values are split on commas through `ParseNames`. The "Book Description" block, up to `=====`, becomes the description (`cleanText(…, 2000)`). Abridged and Unabridged yes/no set `Abridged`. Years come from the copyright and date keys, and the last four-digit year wins.
   - `parseOPF(data []byte) Details` ports lines 384-444 with `encoding/xml` (`Decoder` with `Strict = true`; `encoding/xml` never resolves external entities). It reads `dc:title`/`dc:subtitle`, `dc:creator` by role (`aut` or empty is an author, `nrt` is a narrator; a `refines` role `nrt` turns an author into a narrator), the `calibre:user_metadata:#narrators` JSON, `dc:identifier` with scheme ISBN/ASIN or `urn:isbn:`, `dc:subject` as genres, `dc:date` as the year, publisher, language, the description (HTML stripped, 2000 runes), and `calibre:series` with `calibre:series_index`. A parse error returns an empty `Details`.
   - `parseMetadataJSON(data []byte) Details` ports lines 448-485. It handles the nested `metadata` object, `authors`, `narrators`, `genres`, `series` (`"Name #9"` through `ParseSeriesString`), `publishedYear` or `publishedDate`, and `abridged` as a bool or as the string "true"/"false".
   - `readSidecar(path string) string`: returns "" when the file cannot be stat'ed, is larger than 1 MiB, or cannot be read.
   - `fromSidecars(f sidecarFiles) map[string]Details` returns the keys `json`, `opf`, `txt` and `nfo`. `txt` is built from desc.txt (description, `cleanText` 2000) and from the first non-empty line of reader.txt (`Narrators = ParseNames(line)`). Call `fillProduction` on each result.
   - `sidecarSignature(f sidecarFiles) string`: SHA-256 hex over sorted lines `kind\x00base\x00size\x00mtime_ns`, using `Lstat`. It returns "" when no sidecar exists.
5. `internal/metadata/local.go` (`local`): for each sidecar source, set `desired[src] = d.Values()`, and loop over the source list `{folder, tags, nfo, txt, opf, json}` instead of the hard-coded pair. Then upsert `book_sidecars`. Update the doc comment: the job also reads sidecar files.
6. New `internal/library/sidecars.go`:
   - `func (t *Tx) PutSidecarSignature(bookID, sig string, at int64) error`: upsert.
   - `func (r *Repo) SidecarCandidates(ctx, libraryID string) ([]SidecarBook, error)` returns `SidecarBook{ID, RootPath string; Single bool; Signature string; Checked bool}` for books with `missing_since IS NULL` and `refused_reason IS NULL`. `Single` is true when an audio file's path equals `root_path`. It LEFT JOINs `book_sidecars`.
7. `internal/metadata/jobs.go`: add `func (s *Service) Finisher() func(ctx context.Context, libraryID string)`. For each candidate it computes `sidecarSignature(findSidecars(...))`. If that signature differs from the stored one, or nothing is stored yet, it queues `metadata.local` using the same dedupe as `Follower`. Errors are logged and never stop the loop.
8. `cmd/tendshelf/hooks.go` `registerDetails`: add `d.scanner.OnScanFinished(svc.Finisher())`.
9. `NOTICES.md`: add the row `internal/metadata/sidecar.go` | TendVox Extractor `extractor_details.py` (`sidecars`, `parse_nfo`, `parse_opf`, `parse_metadata_json`, `local_details`); Audiobookshelf `server/utils/parsers/parseOpfMetadata.js`, `parseNfoMetadata.js` | GPL-3.0 | Method port, idiomatic Go.

Acceptance tests (`internal/metadata/sidecar_test.go` and `local_test.go`)
- Table tests for `parseNFO`, `parseOPF` (the exact XML of the Extractor's `test_details.py:96-103`, a `refines` narrator, the calibre narrators JSON, `urn:isbn:`, malformed XML giving an empty result) and `parseMetadataJSON` (flat, nested `metadata`, `abridged:"true"`, a non-object).
- `TestSidecarPrecedence` ports `test_details.py:85-119` with `t.TempDir()`: the folder `Ann Author/Moon Series/Book 3 - The Tide {Dee Narr}`, the same five files, and tags as stored probe JSON. Expected: author `Opf Author`, narrators `Opf Narr One, Opf Narr Two`, series `Opf Series` #4, year 2021, publisher `Opf Pub`, ASIN from the nfo, the ISBN, and `from["authors"] == "opf"`. Then add metadata.json and expect the Json values and `abridged=true`.
- `TestSidecarLooseFile`: `<stem>.opf` next to a single file is read, and `cover.opf` in the same folder is not.
- `TestSidecarLimits`: a 1 MiB + 1 file, a symlinked sidecar and a directory named `x.opf` are all ignored.
- `TestSidecarFinisher`: no signature queues a job; an unchanged signature queues nothing; touching desc.txt queues again; an active job is not queued twice.
- `TestSidecarUserWins`: a `user` row beats `json`.
- Migration: `TestMigrateFromPreviousFixtures` passes for v10 with the seed rows kept. `TestBackupBeforeMigration` is unchanged and green.

Docs: architecture `metadata_values` row (new order), data-model rows for `book_sidecars` and `online_cache`, and the `internal/metadata` line ("sidecars").
Out of scope: chapters from sidecars (the `ChapterSidecar` source stays unused), `.cue` files, and editing sidecars.

---

## M4-2 Online client (Go: `internal/metadata/online` only)

Invariants
- This is the only package that builds an `http.Client`. Every request, including every redirect hop, passes `guardTransport`: the scheme is `https`, and the host is in `apiHosts` or matches a cover-host suffix. Anything else is refused before dialling.
- Only `Query{Title, Author, ASIN, ISBN}` leaves the machine. `Language` only orders the Audnexus regions and is never sent.
- Timeout 10 s per request. JSON bodies are capped at 2 MiB and covers at 5 MiB; reading stops at cap+1. Requests are spaced per host. A 429 gets one retry after `Retry-After`, clamped to 1-10 s (5 s by default).
- The package does not import the database, `metadata`, `jobs` or `web`. Caching goes through the `Cache` interface.

Steps (new files; each port file starts with `// Ports the Extractor extractor_online.py (<functions>); see NOTICES.md.`)
1. `client.go`:
   - `type Options struct { Cache Cache; CoverDir string; UserAgent string; Now func() time.Time; Transport http.RoundTripper }`. `Transport` is for tests only and is wrapped by the guard anyway.
   - `func New(o Options) *Client`. The `http.Client` has `Timeout: 10*time.Second`. Its transport is `guardTransport{next}`, and `next` defaults to a clone of `http.DefaultTransport`. `CheckRedirect` stops after 3 hops.
   - `apiHosts`: `api.audnex.us`, `api.audible.com`, `www.googleapis.com`, `openlibrary.org`, `itunes.apple.com`.
   - `coverHosts`: suffixes `.media-amazon.com`, `.mzstatic.com`, `.googleusercontent.com`, `.gstatic.com`, `.archive.org`, plus the exact hosts `covers.openlibrary.org`, `books.google.com` and `archive.org`.
   - `gap` per host: 500 ms for the four API hosts other than iTunes; 1 s for `itunes.apple.com` and every other host.
   - `type Cache interface { Get(ctx, provider, key string, maxAge time.Duration) ([]byte, bool, error); Put(ctx, provider, key string, body []byte, at time.Time) error }`. `key` is the SHA-256 hex of the full URL, and the TTL is 7 days.
   - `func (c *Client) getJSON(ctx, provider, rawURL string) (json.RawMessage, error)`: check the cache first. A 404 becomes `null` and is cached. 2xx bodies must be valid JSON. Errors are cached never.
   - `type Error struct{ Code, Site string }` with the codes `refused`, `offline`, `busy`, `too_large`, `bad_answer`, `server`. `func (e *Error) Text() string` gives plain words, for example "Tendshelf could not reach Google Books. Check this computer's internet connection and try again."
2. `book.go`:
   - `type Book struct { Provider, ProviderLabel, ID, Title, Subtitle, Publisher, Description, Language, ISBN, ASIN, CoverURL, Sequence, SeriesName, Format, Rating, RatingCount string; Year int; Authors []Person; Narrators, Genres []string; Score float64; Exact bool }` and `type Person struct{ Name, ASIN string }`.
   - Values come back raw: HTML and "Book 2" are left as they are, because `metadata` cleans them (M4-4).
   - Labels: `audnexus` is "Audible (via Audnexus)", plus "Google Books", "Open Library" and "Apple Books".
3. `providers.go` ports `extractor_online.py:184-284`: `parseAudnexusBook` (nil when there is no ASIN or title; genres are only those with `type == "genre"`; `formatType` goes to `Format`), `parseAudibleProducts` (the first 5 valid 10-character ASINs), `parseGoogleBooks` (cover `http:` becomes `https:`), `parseOpenLibrarySearch`, `parseOpenLibraryWork` (string or `{value}`), and `parseITunes` (artwork `100x100` becomes `600x600bb`, and `artistName` is split on ` & ` and `,`).
4. `score.go`:
   - `fold(r rune) string` uses the table from S5. Map these to a: à á â ã ä å ā ă ą. Map to c: ç ć ĉ č. Map to d: ď đ ð. Map to e: è é ê ë ē ĕ ė ę ě. Map to g: ĝ ğ ġ ģ. Map to h: ĥ ħ. Map to i: ì í î ï ĩ ī ĭ į ı. Map to j: ĵ. Map to k: ķ. Map to l: ĺ ļ ľ ł. Map to n: ñ ń ņ ň. Map to o: ò ó ô õ ö ø ō ŏ ő. Map to r: ŕ ř. Map to s: ś ŝ ş š. Map to t: ţ ť ŧ. Map to u: ù ú û ü ũ ū ŭ ů ű ų. Map to w: ŵ. Map to y: ý ÿ ŷ. Map to z: ź ż ž. Also ß becomes ss, æ ae, œ oe and þ th.
   - `norm(s)`: lower-case, then fold, then replace `&` with ` and `. Keep `unicode.IsLetter`, `unicode.IsDigit` and spaces, collapse spaces and trim. Cut the result to 199 runes (so the `difflib` autojunk rule never applies).
   - `ratio(a, b string) float64` re-implements Python `difflib.SequenceMatcher(None,a,b).ratio()` over runes (longest matching block, applied recursively; 2·M/T; two empty strings give 1.0).
   - `Score(b Book, title, author string) float64` ports lines 290-300. Exact ASIN or ISBN gives 1.0 with `Exact = true`. The result is rounded to 3 places.
5. `lookup.go`:
   - `type Query struct{ Title, Author, ASIN, ISBN, Language string }`. `clean()` trims Title and Author and cuts them to 200 runes. ASIN is kept only if it matches `^[A-Z0-9]{10}$` after upper-casing; ISBN only if it matches `^(\d{9}[\dX]|\d{13})$` after removing spaces and dashes.
   - `ErrEmptyQuery` is returned when Title, ASIN and ISBN are all empty.
   - `Providers = []string{"audnexus","google","openlibrary","itunes"}`.
   - `func (c *Client) Lookup(ctx, q Query, providers []string) (Result, error)` ports `search_provider` and `lookup` (lines 315-387), with the same regions and `LANG_REGION`.
   - `type Result struct{ Candidates []Book; Errors map[string]string /* provider→code */; Searched []string }`. Candidates are sorted by score descending (stable), with at most 10 kept. Unknown providers are ignored.
6. `cover.go`:
   - `func (c *Client) FetchCover(ctx, rawURL string) ([]byte, string, error)`: the URL must be on a cover host. The cap is 5 MiB, and `imageKind` (magic bytes: jpeg, png, webp) must match, otherwise `bad_answer`. It reads the disk cache first and then writes to it: `<CoverDir>/<sha256(url)>.img`, valid for 7 days, written atomically (temporary file plus rename).
   - `func (c *Client) CachedCover(rawURL string) ([]byte, string, bool)` only reads the cache and never fetches.
7. `author.go`:
   - `func (c *Client) Author(ctx, asin, name string) (Author, error)` returns `Author{ASIN, Name, Description, ImageURL string; Genres []string}`.
   - If the ASIN is empty: `GET https://api.audnex.us/authors?name=<name>`, then pick the first entry whose `norm(name)` equals the wanted name, then `GET /authors/{asin}`.
   - This response shape is a **guess** (Audnexus is open source, but its author routes have not been checked against the live API). The orchestrator confirms it with one read-only `curl` before M4-4 merges.
8. `testdata/`: copy the Extractor's `tests/data/*.json` and `cover_sample.{jpg,png,webp}` byte for byte. I checked that they are synthetic (Ann Author, B0TESTBOOK, Example Audio/Press, `…/images/I/test.jpg`). Add `audnexus_author.json` and `audnexus_author_search.json`, both synthetic ("Ann Author", "B0AUTHOR01").
9. `NOTICES.md` rows:
   - `internal/metadata/online/*.go` | TendVox Extractor `extractor_online.py`; Audiobookshelf `server/providers/Audnexus.js`, `Audible.js`, `GoogleBooks.js`, `iTunes.js` | GPL-3.0 | Method port, idiomatic Go.
   - `internal/metadata/online/score.go` (`ratio`) | Python `difflib.SequenceMatcher` | PSF-2.0 | Algorithm re-implemented in Go.

Acceptance tests (the fake transport serves testdata by URL and records each request)
- `TestOnlineParsers` ports `test_details.py:245-263`, with the raw values (Sequence "Book 2", the HTML summary) checked as raw.
- `TestOnlineScore`: these `ratio` values must match Python's to 1e-6: ("abcd","bcde") 0.75; ("the tide","tide") 0.666667; ("ann author","an author") 0.947368; ("harbour lights","harbor lights") 0.962963; ("","x") 0; ("private eyes","private eye") 0.956522. Also: `norm("Brontë & Co.") == "bronte and co"`, a Japanese title stays non-empty, and the fold table works.
- `TestOnlineQueryOnlyFourFields` ports lines 264-284: every recorded URL carries only title, author, ASIN or ISBN values; the language is never sent.
- `TestOnlineCacheAndSpacing` ports lines 285-298 with an injected clock and sleep: a second lookup makes 0 requests; the gap per host is respected; a 429 is retried once; a 404 is cached.
- `TestOnlineRegions` ports lines 299-316.
- `TestOnlineGuard` ports lines 317-333: http, an unlisted host, a redirect to an unlisted host, and an oversized body all give `refused`, `refused`, `refused` and `too_large`. The error texts contain no URL or Go error.
- `TestOnlineCover`: magic bytes (`test_overrides.py:35-43`), a body over 5 MiB, HTML served as `.jpg`, and a cache hit with no request.
- `TestOnlineNoOtherClient` ports lines 334-: it walks every `.go` file in the repo (excluding `_test.go`, `internal/metadata/online/` and `internal/testutil/webdriver/`) and fails on `http.Client{`, `&http.Client`, `http.DefaultClient`, `http.Get(`, `http.Post(` or `net.Dial`.

Docs: none beyond the architecture line, which already matches.
Out of scope: rating display, Audible regions other than the Extractor's list, provider genre-to-category mapping, and Extractor categories.

---

## M4-3 Cover store: upload, choose, adopt (Go: mediajobs, library)

Invariants
- Covers are only picked up from bytes that have been checked: their magic bytes say JPEG, PNG or WebP, they are at most 5 MiB, and they decode within `maxDimension`. WebP is converted to PNG with the existing ffmpeg path used by `loadFolder`.
- A cover with source `upload` or `online` is never replaced by the scan job (`covers.go:341`, which is kept). Each book keeps at most 6 such covers. The oldest one that is not the current cover is deleted, along with its file through `removeIfUnreferenced`.
- Choosing a cover only selects one of that book's own `covers` rows.

Steps
1. New `internal/mediajobs/cover_adopt.go`:
   - `const KindCoverAdopt = "media.cover_adopt"`.
   - `type AdoptPayload struct{ BookID, Source, Staged, URL string }`. `Source` is `upload` (needs `Staged`) or `online` (needs `URL`).
   - `func (c *Covers) Stage(data []byte) (string, error)`: writes `<data>/covers/incoming/<16 random hex>.img` atomically after `imageKind` and a size check. The error codes are `not_picture` and `too_large`.
   - `func (c *Covers) SetFetcher(f func(ctx context.Context, url string) ([]byte, string, error))`.
   - `func (c *Covers) Adopt(ctx, bookID, source string, data []byte, guard) (string, error)` reuses `check`, the WebP conversion, `writeAtomic`, `thumbnail` and `colours`. It then calls `PutCover` and `SetBookCover`, prunes extra covers past the limit of 6, and returns the cover id.
   - `func (c *Covers) Choose(ctx, bookID, coverID string) error` returns `library.ErrNotFound` when the cover is not that book's.
   - `func (c *Covers) Choices(ctx, bookID string) ([]library.Cover, error)`.
   - Handler: an `online` payload with a nil fetcher is a permanent error `online_off`. The staged file is removed after success or after a permanent failure. The result is `{"cover_id": id}`. `NewCovers` also sweeps `covers/incoming` (files older than 24 h) with the existing `sweepTemp`.
2. `internal/mediajobs/jobs.go` `Register`: add `jobs.Kind{Name: KindCoverAdopt, Concurrency: 1, MaxAttempts: 2, Handler: covers.adoptHandler}`.
3. `internal/library/covers.go`: add `func (t *Tx) PruneChosenCovers(bookID string, keep int) ([]Cover, error)`, which returns the rows it deleted.

Acceptance tests (`internal/mediajobs/cover_adopt_test.go`, using `mediatest` fake ffmpeg and `testdata/cover_sample.*` copied from the Extractor)
- `TestCoverAdoptUpload`: JPEG, PNG and WebP all give a row with source `upload`, become the current cover, and get thumbnails.
- `TestCoverAdoptRefuses`: HTML renamed to `.jpg`, 5 MiB + 1 bytes, and an oversized dimension all give a plain error, and no row or file is left.
- `TestCoverAdoptScanKeeps`: run `Covers.Run` after an upload, and the current cover is still the upload.
- `TestCoverAdoptPrune`: after a 7th adoption, 6 remain and the current one is kept.
- `TestCoverChoose`: another book's cover id gives `ErrNotFound`, and the book's own folder cover can be chosen back.

Docs: add the kind `media.cover_adopt` to the "Background jobs" list.
Out of scope: routes and pages (M4-8), cropping and rotating, and cover search beyond the lookup candidates.

---

## M4-4 Lookup service (Go: metadata, library, wiring)

Invariants
- Only a person allowed to edit (checked at the web layer) can start a lookup, apply results or reset details, and only by clicking. The scan and `metadata.local` never call `online`.
- `ApplyOnline` writes only the fields the person ticked, and only values that came from a stored job result. The client never sends a value or URL that the server uses.
- Re-resolving after an apply goes through the same code as `metadata.local`, so the book's columns, people, series and genres never drift apart.
- Cache rows older than 7 days are deleted at the start of each lookup job, so the cache stays bounded.

Reused: `local` resolve and sync. The tail of `local` (from `d, _ := Resolve(rows)` to the end) is split into `syncResolved(tx, b, rows) (bool, error)` and called from both paths. Also reused: `syncPeople`, `jobs.Kind`, `db.GetSetting`/`PutSetting`/`Audit`, and the Follower dedupe.

Steps
1. `internal/metadata/settings.go`:
   - `const SettingOnlineEnabled = "online.enabled"`.
   - `func OnlineEnabled(ctx, q db.RowQueryer) (bool, error)`: true when the key is missing.
   - `func SetOnlineEnabled(ctx, st *db.Store, on bool, userID string) error`: one transaction that also audits `settings.online` with `{"on":bool}`.
2. `internal/metadata/online_cache.go`: `type sqlCache struct{ st *db.Store }` implements `online.Cache` (provider ∈ the CHECK list; the Audible search uses `audible`). `func NewOnlineCache(st *db.Store) online.Cache` and `func PruneOnlineCache(ctx, st, before time.Time) error`.
3. `internal/metadata/online.go`:
   - `const KindOnline = "metadata.online"`.
   - `type LookupPayload struct{ BookID string; Query online.Query; Providers []string }`.
   - `type Candidate struct{ Provider, ProviderLabel string; Score float64; Exact bool; Details Details; CoverURL string; CoverCached bool; AuthorASINs map[string]string }`.
   - `type LookupResult struct{ Query online.Query; FetchedAt int64; Candidates []Candidate; Errors map[string]string; Searched []string }`.
   - `func FromOnline(b online.Book) Details` uses `cleanText` (description 2000 runes, then `html.UnescapeString`), `cleanASIN`, `cleanISBN`, `cleanSequence` ("Book 2" becomes "2"), author names, `Format` "abridged"/"unabridged" to `Abridged`, and `fillProduction(description)`.
   - `func (s *Service) SetOnline(c *online.Client)`.
   - `func (s *Service) LookupQuery(ctx, bookID string) (online.Query, error)` prefills from the resolved book: title, first author, ASIN, ISBN and language.
   - `func (s *Service) StartLookup(ctx, bookID string, q online.Query, more bool, userID string) (jobID string, err error)`. It returns `ErrOnlineOff` and `online.ErrEmptyQuery`. Providers are `["audnexus"]`, or all four when `more` is true.
   - Handler: check `OnlineEnabled` (if off, `jobs.Error{Code:"online_off", Permanent:true}`), prune the cache, call `Lookup`, map the results, call `FetchCover` for the first 3 candidates that have a `CoverURL` (errors are ignored and recorded as `CoverCached=false`), and return the `LookupResult`. The kind has Concurrency 1 and MaxAttempts 1.
   - `func (s *Service) LookupResultFor(ctx, jobID, bookID string) (jobs.Job, LookupResult, error)` returns `ErrNotFound` unless the job is of kind `metadata.online` and its payload `BookID == bookID`.
   - `func (s *Service) CachedCover(url string) ([]byte, string, bool)`.
4. `internal/metadata/apply.go`:
   - `var ApplyFields = []string{title, subtitle, authors, narrators, series, published_year, publisher, description, language, genres, isbn, asin, abridged}`.
   - `func (s *Service) ApplyOnline(ctx, bookID string, c Candidate, fields []string, userID string) ([]string, error)` runs in one `repo.Update` transaction. Unknown fields give `ErrBadField`. A field the candidate has no value for is skipped. Each written field follows S2 (an `online` row, plus a `user` row when the current resolved value is non-empty and different). Then it calls `syncResolved` and sets `people.asin` for authors whose name matches case-insensitively and whose ASIN is empty. It audits `book.details.apply` with `{"provider":…, "fields":[…]}` and returns the fields that changed.
   - `func (s *Service) ResetOnline(ctx, bookID, userID string) error` deletes the book's `user` and `online` rows, re-resolves, and audits `book.details.reset`.
   - `func (s *Service) Sources(ctx, bookID string) (map[string]SourceInfo, error)` returns `SourceInfo{Source string; ObservedAt int64}` per resolved field, for the compare badges.
5. `internal/metadata/author.go`:
   - `const KindAuthor = "metadata.online_author"`, with payload `{person_id}`.
   - `func (s *Service) StartAuthorLookup(ctx, personID, userID string) (string, error)`.
   - Handler: call `client.Author`. If there is an `ImageURL`, call `FetchCover`, write `<data>/people/<sha>.<ext>` atomically, make the 320 px thumbnail `<data>/people/<sha>-320.jpg` with `media.Tools.Thumbnail`, then `UpdatePersonDetails`, and remove the previous image files when no other person uses them.
   - Give `Service` `tools media.Tools` and `dataDir string` through `SetOnline(c, tools, dataDir)`. Use that 3-argument signature in step 3 too.
6. New `internal/library/people_details.go`:
   - `func (t *Tx) UpdatePersonDetails(id, description, asin, imageID string) error`.
   - `func (t *Tx) SetAuthorASIN(bookID, name, asin string) error`.
   - `func (r *Repo) PersonByID(ctx, id string) (Person, error)` is unscoped and used by the job only.
7. Wiring:
   - `cmd/tendshelf/hooks.go`: `registerDetails` returns `(*metadata.Service, *online.Client, error)`. It builds `online.New(online.Options{Cache: metadata.NewOnlineCache(d.st), CoverDir: filepath.Join(d.dataDir,"cache","online-covers"), UserAgent: "Tendshelf/"+d.version})`, calls `svc.SetOnline(client, d.tools, d.dataDir)`, and registers `KindOnline` and `KindAuthor` in `Register`.
   - `registerMediaJobs` also returns `*mediajobs.Covers`.
   - Add `version string` to `deps`.
   - `main.go`: call `covers.SetFetcher(client.FetchCover)` and pass `Details: svc, Covers: covers` to `web.Options`.
   - `internal/web/server.go` Options: add `Details *metadata.Service` and `Covers *mediajobs.Covers`, both optional. When nil, the M4 routes answer 404. Store them on `server`.

Acceptance tests (`internal/metadata/online_test.go`, `apply_test.go`, `author_test.go`; `online.Options.Transport` serves M4-2's testdata)
- `TestLookupFromOnline`: the Audnexus fixture becomes Details with series "The Match Series" #2, no `<p>`, `Abridged=false` and `Dramatized=true`. This finishes the port of `test_details.py:245-252`.
- `TestLookupJob`: the result has candidates sorted by score with `CoverCached` set on the top 3. When online is off the job fails with `online_off` and no request is made. Old cache rows are pruned.
- `TestLookupApplyRules` ports `test_details.py:205-216`. An empty local narrator plus the online value gives only an `online` row. A local year of 2001 plus a ticked online 2023 gives `online` and `user` rows, and the book's year is 2023. An unticked field is unchanged. A field that is unknown gives `ErrBadField`. The author ASIN is set. An audit row is written.
- `TestLookupSidecarBeatsOnlineFill`: after an online fill, adding metadata.json wins on the next `Local`.
- `TestLookupReset`: after a reset, the values come from the files again.
- `TestLookupResultForOtherBook` gives `ErrNotFound`.
- `TestLookupAuthor`: description, ASIN and image are stored and the thumbnail file exists.

Docs: the job kinds `metadata.online` and `metadata.online_author`; `online.enabled` defaults to on; the `<data>/people/` and `<data>/cache/online-covers/` folders.
Out of scope: all UI, typing edits by hand, bulk lookups, and automatic lookups.

---

## M4-5 Write engine (Go: `internal/media` only)

Invariants
- Arguments are fixed lists with `-nostdin -protocol_whitelist file,pipe`. Each run has a context deadline of `2 min + 1 min per GiB` of input, and stderr is capped through `runCapped`.
- These functions never rename or delete anything. They write only to the `dst` path the caller names, plus one metadata file under the caller's `metaDir`, which is outside the library.
- `AudioHash` covers the audio packets only (`-map 0:a -c copy -f hash -hash sha256 -`), so changing tags must not change it.

Steps (new files; no port)
1. `internal/media/writetags.go`:
   - `type TagWrite struct{ Format string; Tags map[string]string; Chapters []WriteChapter; WriteChapters bool }` and `type WriteChapter struct{ StartMs, EndMs int64; Title string }`.
   - `func WriteFormat(container, path string) (string, bool)`: `.m4b`, `.m4a` and `.mp4` with a mov/mp4 container give `mp4`; `.mp3` gives `mp3`; `.flac` gives `flac`; anything else is not supported.
   - `func (t Tools) WriteTags(ctx, src, dst, metaDir string, w TagWrite) error` builds:
     `-nostdin -protocol_whitelist file,pipe -i src [-f ffmetadata -i meta] -map 0 -map_metadata 0 -map_chapters {1|0} -c copy -metadata k=v… [-movflags +use_metadata_tags] [-id3v2_version 4] -f <fmt> -y dst`.
     The ffmetadata file `;FFMETADATA1` with `[CHAPTER] TIMEBASE=1/1000` is written to `metaDir`, escaped according to the ffmetadata rules (`= ; # \` and newline), and removed afterwards.
   - `func (t Tools) AudioHash(ctx, path string) (string, error)` parses `SHA256=<hex>`.
2. `internal/media/booktags.go`:
   - `type BookInfo struct{ Title, Subtitle, Publisher, Description, Language, ASIN, ISBN, Series, Sequence string; Year int; Authors, Narrators, Genres []string }`.
   - `func BookTags(b BookInfo, multiFile bool) map[string]string` sets: `album`=Title; `title`=Title only when `!multiFile` (parts keep their own titles); `artist` and `album_artist`=authors joined with ", "; `composer`=narrators joined with ", "; `date`=Year; `genre`=genres joined with "; "; plus `publisher`, `description`, `language`, `subtitle`, `series`, `series-part`, `asin` and `isbn`. Empty values are left out.
3. `internal/media/space_unix.go` (`//go:build unix`) and `space_other.go`: `func FreeBytes(dir string) (uint64, error)` uses `syscall.Statfs`. The other platforms return `ErrNoSpaceInfo`, and the caller then refuses.
4. `internal/media/filehash.go`: `func FileSHA256(path string) (string, error)` streams the file.
5. `internal/media/mediatest/`: the fake ffmpeg learns `-f hash` (it prints `SHA256=` plus the sha256 of the input file's bytes after a marker, so tests can keep it constant) and `-f ffmetadata`/`-map_chapters` (it copies the input and appends the tags to a JSON trailer that the fake ffprobe reads back). Builder: follow the existing `fakeffmpeg.go` pattern and record the format you choose in the file comment.

Acceptance tests
- `TestWriteTagsArgs`: an exact argument table per format, with and without chapters; ffmetadata escaping cases; and no shell.
- `TestWriteTagsBookTags`: a multi-file book omits `title`; empty values are left out.
- `TestWriteTagsReal`, opt-in with `TENDSHELF_TEST_FFMPEG`: synthesize 3 s sine files as m4b (aac), mp3 and flac with ffmpeg, write the tags, then check that `AudioHash(src)==AudioHash(dst)`, the duration is within 50 ms, and ffprobe reads back every tag written. m4b and mp3 get 3 chapters written and read back with the same count and starts within 1 ms; for flac, the chapter count is unchanged.
- **Guesses to confirm in this test:** an m4b chapter text track is not duplicated by `-map 0` plus `-map_chapters 1`, and the mp3 muxer writes `CHAP` frames that ffprobe reads. If either fails, commit what is green and report `PLAN GAP`.

Docs: none (architecture already describes this).
Out of scope: ogg/opus/wma, embedding cover art, and the ledger and job (M4-7).

---

## M4-6 Compare UI: lookup, compare sheet, apply, online switch (Go web + templ)

Invariants
- Every route checks `share.EditBook` on the book that `loadBook` resolves. Refusals, unknown books, a job that belongs to another book, and online being off all answer 404. The admin switch checks `ManageServer`.
- The apply form posts only `job`, `c` (candidate index) and the `use` field names. The server reads the values and the cover URL from the stored job result.
- Each sheet is the canonical page URL with `?sheet=`. It renders whole without JavaScript, and htmx swaps `#sheet > *`. Polling uses `/hx/jobs/{id}` with `hx-trigger="every 2s"` and stops once the job has finished. Without JavaScript there is a "Check again" link to the same URL.
- No new JS file and no edits to any JS file. No inline style, no `hx-on`, no `js:` values, no trigger filters.

Steps
1. `internal/web/routes_jobs.go`: turn `getJob` into a dispatcher by kind. Add `var jobViews = map[string]func(s *server, w http.ResponseWriter, r *http.Request, j jobs.Job)`. `scan.KindScan` uses the existing scan code (moved unchanged into `getScanJob`), and `metadata.KindOnline` uses `getLookupJob`. An unknown kind answers 404.
2. New `internal/web/routes_details.go` with `registerDetails(mux)`, called at the end of `registerBook` in `routes_book.go`. Do not edit `server.go`, which M4-7 owns in G3.
   - `POST /books/{id}/match`: form fields `title`, `author`, `asin`, `isbn`, `more`. It calls `StartLookup`. `ErrEmptyQuery` gives 422 with the lookup sheet showing the field error "Type a title, an ASIN or an ISBN to look up." Success gives 303 to `/books/{id}/details?sheet=compare&job=<id>`. An htmx request instead gets the polling fragment in `#sheet`.
   - `POST /books/{id}/match/apply`: fields `job`, `c` (0-9), `use` (repeated; must be in `ApplyFields`, plus `cover`). A job that is not done gives 409 and a toast "The lookup is still running." A bad `c` or `use` gives 422. If `cover` is ticked and the candidate has a `CoverURL`, enqueue `media.cover_adopt` with `{Source:"online", URL}` from the result. Audit through `ApplyOnline`. Answer 303 to `/books/{id}/details?applied=<n>`. With htmx, the same 303 is followed and the page `hx-select`s `#book-panel` and closes the sheet through the existing `components/sheet_closed.templ` out-of-band fragment.
   - `GET /books/{id}/match/{job}/cover/{n}`: serves only `CachedCover` (never fetches). It sends `Content-Type` from the magic bytes and `Cache-Control: private, max-age=3600`. A missing cover answers 404.
   - `POST /books/{id}/details/reset`: calls `ResetOnline` and answers 303 to `/books/{id}/details`.
   - `POST /admin/online`: `on=0|1`. It calls `SetOnlineEnabled` and answers 303 to `/admin/libraries`.
3. `internal/web/routes_book.go` `renderBook`, `case "details"`:
   - When `canEdit` (EditBook) and online is on, set `v.LookupHref = "/books/{id}/details?sheet=lookup"`.
   - `?sheet=lookup` gives `s.lookupSheet(r, id, form)`; `?sheet=compare&job=&c=&use=&picked=1` gives `s.compareSheet(...)`.
   - `?applied=n` shows a toast "Used {n} changes." (n=1: "Used 1 change.").
   - The "Reset" disclosure is shown when the book has `user` or `online` rows.
4. New `internal/web/details_views.go`:
   - `compareRows(cur library.BookDetail, src map[string]metadata.SourceInfo, cand metadata.Candidate, use map[string]bool, picked bool) []pages.CompareRow`.
   - `pages.CompareRow{Field, Label, Now, NowBadge, Online, OnlineBadge string; Diff, Ticked, Disabled bool}`.
   - Badges: "From file" for tags, folder, nfo, opf, txt and json; "Online · 2 Nov" from `ObservedAt` or `FetchedAt`; "Edited by you" for `user`.
   - Default ticks when `!picked`: tick only when the values differ, the online value is non-empty, the current source is not `user`, the match is good (`Exact || Score >= 0.8`), and the current value is empty or comes from `folder`. Rows that are the same or have an empty online value are disabled.
   - The cover row is ticked only when the book has no cover.
   - The description shows its first 200 runes plus a `<details>` with the full text.
5. New `internal/web/pages/details_sheets.templ`, with `var DetailsStyles = []string{"css/pages-details.css"}` (the file is created empty here and filled by M4-10):
   - `LookupSheet(v LookupSheetView)`: the four fields, and a `<details>` titled "Search more places" containing the checkbox "Also search Google Books, Open Library and Apple Books".
   - `CompareSheet(v CompareSheetView)` has these states:
     - waiting: "Looking up details…", with the poll;
     - failed: plain text from `online.Error.Text()` or the job error, plus "Try again";
     - none: "Nothing close turned up. Check the spelling of the title and author, or try the ASIN or ISBN.";
     - list: up to 5 candidates as links `?c=n`, each showing the title, provider label, year, and "Exact match" when `Exact`;
     - table: the `ts-compare` markup from the CompareTable README. The checkboxes `use` have `hx-get` set to the sheet URL with `hx-trigger="change"`, `hx-select="#compare-use"`, `hx-target="#compare-use"` and `hx-swap="outerHTML"`, plus a hidden `picked=1`. Buttons: "Use {n} changes" (`#compare-use`, disabled when n=0) and "Keep everything as it is" (closes).
   - `LookupJobFragment(v)` is what `/hx/jobs/{id}` returns: the waiting state again, or the finished sheet.
   - Words come from the board ComparePhone and the copy deck. New strings live in a new `internal/web/details_copy.go`.
6. `internal/web/pages/book.templ`, Details tab: add the "Look up details online" button (`ts-btn`, `hx-get` page + `hx-select="#sheet > *"`, matching the shelf sheet opener) and the reset disclosure "Go back to the details in the files", which holds a POST form. Add the matching fields to `BookView`.
7. `/admin/libraries` (`routes_admin.go` and its templ): an owner-only section "Book details from the internet", with the radios "Look up details when someone asks" and "Never go online", and a Save button.

Acceptance tests (`internal/web/compare_test.go`, `routes_matrix_details_test.go`; `metadata.Service` with a fake-transport `online.Client`)
- Matrix for every new route and sheet with owner, admin, member with an `all` grant, guest with a grant, disabled admin and anonymous. Owner and admin get 2xx or 303; member, guest and disabled get 404; anonymous gets the house redirect to `/login`. For `POST /admin/online`, admin also gets 404. When online is off, every match route gives 404 and the button is absent.
- `TestCompareLeak`: a job for book A requested through book B (sheet, apply, cover, `/hx/jobs`) gives 404, and the response body does not contain A's title.
- `TestCompareFlow`: POST match gives 303 and then the waiting fragment polls. After the job finishes, `/hx/jobs/{id}` returns the table with the default ticks from step 4. Apply with `use=narrators` changes only the narrators, adds an audit row, and shows the toast. Unknown `use` gives 422. A tampered `c=9` gives 422.
- `TestCompareUseCount`: a GET with `picked=1&use=a&use=b` renders "Use 2 changes", and with none it renders a disabled button.
- `TestCompareNoJS`: a full GET of `?sheet=compare&job=` without `HX-Request` renders the sheet inside the page, including the "Check again" link.
- `TestCompareCSP`: no `style=` attribute, no `<script>` and no `hx-on` in the new templ output (pattern of the existing CSP tests).
- One Chromium browser test (`TENDSHELF_TEST_BROWSER`): open, wait, untick, check the count changes, apply, and check the Details tab updates. Run it at desktop-1440 and mobile-390.

Docs: architecture routes table (`/books/{id}/match`, `/match/apply`, `/match/{job}/cover/{n}`, `/details/reset`, `/admin/online`), and the Compare sheet row (canonical `?sheet=compare`, `/hx/jobs/{id}`).
Out of scope: CSS (M4-10), the cover picker and author page (M4-8), and a free-form edit form (`/books/{id}/edit` stays a later item).

---

## M4-7 Write ledger, job and recovery (Go: db, library, mediajobs, wiring)

Invariants
- This job, together with `Writes.Confirm`/`Writes.Undo`, is the only code that renames, writes or deletes in a library folder. It touches only `original_path` and its two hidden siblings.
- The ledger row is committed *before* each file-system step, so after a crash the row always names the step that may be half done. Recovery never runs ffmpeg. It decides with `os.Lstat` and `FileSHA256` only.
- At most one open row per `original_path`, enforced by a partial unique index.
- Recovery never touches a row whose job is running in this process (`Writes.active` set) or is `running` in the database.
- A file is refused before any write when: its container is not supported, it is encrypted or refused, it is missing, its mtime is within the last 10 minutes, free space is below size×1.05, or a `.tendshelf-new` or `.tendshelf-before` sibling already exists. In those cases the whole job writes nothing.

Steps
1. `internal/db/migrations/0011_file_writes.sql`. The table deliberately has no foreign keys, because rows must outlive pruned jobs and deleted books:
   `CREATE TABLE file_writes (id TEXT PRIMARY KEY, job_id TEXT NOT NULL, book_id TEXT NOT NULL, audio_file_id TEXT NOT NULL, state TEXT NOT NULL CHECK (state IN ('copying','verifying','swapping','swapped','confirming','confirmed','undoing','undone','failed')), original_path TEXT NOT NULL, backup_path TEXT NOT NULL, temp_path TEXT NOT NULL, before_sha256 TEXT NOT NULL, after_sha256 TEXT, audio_hash TEXT NOT NULL, error_code TEXT, created_by TEXT NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL) STRICT;`
   `CREATE UNIQUE INDEX file_writes_open ON file_writes(original_path) WHERE state NOT IN ('confirmed','undone','failed');`
   `CREATE INDEX file_writes_book ON file_writes(book_id, state);`
   Set `SchemaVersion = 11`. Add the `open_test.go` entries (seed v10 with a `book_sidecars` row and check that it survives).
2. New `internal/library/filewrites.go`:
   - `FileWrite` struct (all columns) and `NewFileWriteID()` with the prefix `fw_` (see `internal/db/id.go`).
   - `func (t *Tx) InsertFileWrite(*FileWrite) error`, `func (t *Tx) SetFileWriteState(id, from, to string, after *string, code string) error` (`UPDATE … WHERE id=? AND state=?`; 0 rows gives `ErrStateMoved`), `func (r *Repo) OpenFileWrites(ctx) ([]FileWrite, error)` and `func (r *Repo) BookFileWrites(ctx, bookID string, states ...string) ([]FileWrite, error)`.
3. New `internal/mediajobs/writes.go`:
   - `const KindWriteDetails = "media.write_details"`, with payload `{book_id}`.
   - `type Writes struct{ repo; tools; data; jobs Enqueuer; runner Canceller; log; now; mu sync.Mutex; active map[string]bool; OnChanged func(ctx, libraryID string); crash func(point string) error /* tests only */ }`.
   - `NewWrites(...)`, `Preflight(ctx, bookID) ([]Refusal, error)` and `Start(ctx, bookID, userID) (jobID, error)`. `Start` runs Preflight and refuses with the first refusal's plain text.
   - Handler, for each live file in play order:
     1. Insert a `copying` row with before_sha256 and audio_hash, then crash point `copied0`.
     2. `WriteTags` to the temp file (metaDir is `<data>/tmp/writes`), then fsync it.
     3. Move to `verifying`.
     4. Verify: `AudioHash` equal, the probe duration within 50 ms, chapters (when written: same count, starts within 1 ms; otherwise count unchanged), and every written tag read back with trimmed values equal. A failure removes the temp file and moves to `failed` with `verify_audio`, `verify_duration`, `verify_chapters` or `verify_tags`.
     5. Record `after_sha256`. Check that the original's size and mtime are unchanged (a change gives `failed` with code `changed`).
     6. Move to `swapping`, rename the original to the backup (crash point `swap1`), rename the temp file to the original (crash point `swap2`), and fsync the directory.
     7. Move to `swapped`.
     Every state change goes through `UpdateGuarded(ctx, t.Fence, …)`. A lost fence stops the handler at once and leaves the rest to recovery. When the handler finishes, it calls `OnChanged(libraryID)`.
   - The chapters are the book's chapters when the book is a single file. Multi-file books do not get chapters written.
   - Tags come from `media.BookTags`, built from `library.BookDetail`.
   - `func (w *Writes) Confirm(ctx, bookID, userID) (int, error)`: for each `swapped` row, move to `confirming`, remove the backup (crash point `confirm1`), then move to `confirmed`. Audit `book.write.confirm`.
   - `func (w *Writes) Undo(ctx, bookID, userID) (int, error)`: for each `swapped` row, first require `FileSHA256(original)==after_sha256`; otherwise mark the row `failed` with code `needs_attention` and stop. Then move to `undoing`, rename the original to the temp path (crash point `undo1`), rename the backup to the original (crash point `undo2`), remove the temp file, and move to `undone`. Then `OnChanged` and audit `book.write.undo`.
   - All three paths take `w.mu` and mark the book active.
4. `func (w *Writes) Recover(ctx) error`. It skips active rows and rows whose job state is `running`. Then, by state:
   - `copying` or `verifying`: remove the temp file if present, then `failed` with code `interrupted`.
   - `swapping`:
     - original present and backup absent: remove the temp file, then `failed`;
     - original absent and backup present: rename the backup to the original, remove the temp file, then `failed`;
     - both present: if `sha(original)==after_sha256`, move to `swapped`; otherwise `failed` with code `needs_attention` and touch nothing;
     - both absent: `failed` with code `needs_attention` and touch nothing.
   - `confirming`: remove the backup if present, then `confirmed`.
   - `undoing`:
     - backup present and the original's sha equal to `after_sha256`: run the full undo;
     - backup present and the original absent: rename the backup to the original, remove the temp file, then `undone`;
     - backup absent and the original's sha equal to `before_sha256`: remove the temp file, then `undone`;
     - anything else: `needs_attention`.
   After resolving rows, it cancels each `held` `media.write_details` job through `runner.Cancel`. Each outcome is audited as `book.write.recover`.
   `func (w *Writes) Run(ctx)` runs `Recover` once and then every 30 s until ctx ends. The main program starts it after `runner.Start`.
5. `internal/mediajobs/jobs.go` `Register`: add the kind `{Name: KindWriteDetails, Concurrency: 1, MaxAttempts: 1, HoldOnRecovery: true}`. `Register` gains a `*Writes` parameter.
6. Wiring:
   - `cmd/tendshelf/hooks.go`: build `Writes` and set `OnChanged` to the scanner's existing "scan this library" enqueue (the same call as the admin Scan now; reuse it, do not add another).
   - `main.go`: `go writes.Run(ctx)` after `runner.Start`, and pass `Writes: writes`.
   - `server.go` Options: add `Writes *mediajobs.Writes`, which is optional (nil means the routes answer 404).
7. New `internal/metadata/booktags_roundtrip_test.go`: `FromTags(media.BookTags(info,false), true)` equals the details that `info` came from, for a full table (this proves the writer is the inverse of the reader).

Acceptance tests (`internal/mediajobs/writes_test.go`; fake ffmpeg; real files are synthetic bytes under `t.TempDir()`)
- `TestFileWritesHappy`: two files are swapped, the backups are hidden, the temp files are gone, the audio hash is unchanged, and `OnChanged` is called once.
- `TestFileWritesPreflight`: one table row per refusal reason. Each asserts that no ledger row exists and that the folder listing is byte-identical.
- `TestFileWritesVerifyFails`: the fake returns a different hash, so the row is `failed` with `verify_audio`, the original is untouched, and the temp file is gone.
- `TestFileWritesCrash`: a table over the crash points `copied0`, `verifying`, `swapping`, `swap1`, `swap2`, `confirm1`, `undoing`, `undo1` and `undo2`. Each injects an error at that point, then runs `Recover` on a fresh `Writes`, then asserts the final state, which files exist at the three paths, the original's sha, and that ffmpeg was not called during recovery.
- `TestFileWritesRecoverSkipsRunning`: a row whose job is `running` is left alone.
- `TestFileWritesUndoChanged`: an original changed after the swap gives `needs_attention`, and the backup is kept.
- `TestFileWritesRealRoundTrip`, opt-in with `TENDSHELF_TEST_FFMPEG`: m4b, mp3 and flac each go through write, Undo (byte-identical to before) and write again, then Confirm (the backup is gone).

Docs: architecture "Write details into the file" (hidden names from S9, the state list including `swapping`, `confirming` and `undoing`, and the recovery table) and the `file_writes` row.
Out of scope: UI (M4-9), automatic expiry of backups (they stay until the person picks Looks good or Undo; the Details tab lists them), and writing cover art.

---

## M4-8 Cover picker and author details UI (Go web + templ)

Invariants
- The cover routes need `EditBook` on the book; a cover id belonging to another book gives 404. The author routes need `s.canManage` for the lookup, and `getPerson`'s existing visibility rule for the photo. The photo is served only from `<data>/people/`, by the person's own `image_id`, never by a path the client sends.
- Uploads are capped at 5 MiB plus 64 KiB of form data. The CSRF check reads multipart bodies up to 6 MiB **only** for `POST /books/{id}/cover/upload`; every other route keeps the 1 MiB `csrfFormLimit`.
- No new JS. Upload is a plain `<form enctype="multipart/form-data">` with a hidden `_csrf`. htmx is not used for the upload.

Steps
1. `internal/web/csrf.go`: add `csrfUploadLimit = 6 << 20` and `func csrfLimit(r *http.Request) int64`. It returns the upload limit when `r.Method == POST` and the path matches `^/books/[^/]+/cover/upload$`. Use it in the multipart branch (lines 173-175).
2. New `internal/web/routes_cover_pick.go` with `registerCoverPick(mux)`, called at the end of `registerBook`:
   - `GET /books/{id}/details?sheet=cover` is dispatched from `renderBook`, like step 3 of M4-6. It renders the current cover and every `Choices` row as radio cards with thumbnails, the "Use this cover" submit, the upload form ("Choose a picture", hint "JPEG, PNG or WebP, up to 5 MB"), and the adopt status when `?job=` is set.
   - `GET /covers/{bookID}/choices/{coverID}?w=160|320` serves that row's thumbnail through the existing `getCover` file-selection helper, with an explicit cover row passed in. It answers 404 when the row is not the book's or the person cannot edit.
   - `POST /books/{id}/cover` (`cover_id`): calls `Choose`, audits `book.cover.choose`, and answers 303 to `/books/{id}/details`.
   - `POST /books/{id}/cover/upload` (`picture`): wrap the body in `http.MaxBytesReader(5<<20 + 64<<10)` and read the part. Call `Stage`, then enqueue `media.cover_adopt` (upload), audit `book.cover.upload`, and answer 303 to `?sheet=cover&job=<id>`. A non-picture gives 422 with "That file is not a picture Tendshelf can use. Choose a JPEG, PNG or WebP picture." A file that is too large gives 413 with "That picture is larger than 5 MB."
   - `routes_jobs.go`: add a `jobViews` entry for `media.cover_adopt` (EditBook on the payload's book). It shows waiting ("Adding the picture…"), done (the sheet re-rendered with the new cover current), or failed (plain text).
   - Compare sheet (M4-6): the cover row's online preview `<img>` uses the `/match/{job}/cover/{n}` route.
3. `internal/web/pages/book.templ`: a "Change cover" link (EditBook) under the hero cover, opening `?sheet=cover` as a page + `hx-select="#sheet > *"`.
4. Author page (`routes_book.go` `getPerson` and `pages/people.templ`):
   - Show the photo (`/people/{id}/photo`, alt "" next to the name), the bio (`description`, in paragraphs, plain text) and "Look up author online" (`canManage`, online on).
   - Follow the AuthorPage and AuthorTablet boards.
   - New `GET /people/{id}/photo` (the 320 px JPEG, `Cache-Control: private, max-age=86400`, ETag `image_id`) and `POST /people/{id}/lookup` (calls `StartAuthorLookup` and answers 303 to `/authors/{id}?job=`).
   - `jobViews` entry for `metadata.online_author`.
   - Narrators get the photo and bio too, but no lookup button (Audnexus only has authors).
5. New `internal/web/cover_copy.go` holds the new strings (listed in the report as missing from the copy deck).

Acceptance tests (`internal/web/cover_pick_test.go`, `routes_matrix_covers_test.go`)
- Matrix as in M4-6 for the five new routes and both new `jobViews`. Also: a member who can view the book gets 404 on choose, upload and the choices thumbnail, but 200 on `/people/{id}/photo` when they can open a book by that author. A guest with no matching grant gets 404 on the photo.
- `TestCoverPickUpload`: a 4 MiB JPEG passes CSRF and is staged. A 5 MiB + 1 file gives 413. A missing `_csrf` gives 403 (or whatever the house CSRF status is). A 2 MiB urlencoded body to another route is still refused at 1 MiB.
- `TestCoverPickChoose`: another book's cover id gives 404. Choosing gives 303, `books.cover_id` changes, and an audit row is written.
- `TestCoverPickPhotoPath`: an `image_id` holding `../` (planted in the DB) gives 404, because ids must match `^[0-9a-f]{64}$`.
- Browser test (`TENDSHELF_TEST_BROWSER`), desktop-1440 and mobile-390: open the cover sheet, upload a synthetic PNG, wait for the poll, and check the new cover is shown.

Docs: architecture routes table (the cover and people routes) and the `Sheets` row.
Out of scope: drag-and-drop, cropping, narrator lookup, and editing a bio by hand.

---

## M4-9 Write details UI (Go web + templ)

Invariants
- Every route needs `EditBook` on the book and `Writes != nil`. Refusals are 404.
- Starting a write has its own confirm step (a sheet with a POST form). No GET changes anything.
- While swapped rows exist, the Details tab shows "Looks good" and "Undo" for that book and nothing else that writes.

Steps
1. New `internal/web/routes_write.go` with `registerWrite(mux)`, called at the end of `registerBook`:
   - `GET /books/{id}/details?sheet=write` is dispatched from `renderBook`. It runs `Preflight` and lists the files and the details that will be written (title, authors, narrators, series, year and genres, plus chapters for single-file books).
     - Refusals are shown in plain words, for example "This file was changed in the last 10 minutes. Try again later." and "There is not enough free space next to this file."
     - The button "Write details into the files" appears only when there are no refusals.
     - The note: "Tendshelf keeps a copy of each file until you choose Looks good or Undo."
   - `POST /books/{id}/write`: calls `Start`. A refusal gives 422 with the sheet. Success gives 303 to `?sheet=write&job=<id>`.
   - `POST /books/{id}/write/confirm` and `POST /books/{id}/write/undo`: 303 to `/books/{id}/details`, with the toasts "Kept the new details." and "Put the files back as they were."
   - `routes_jobs.go`: a `jobViews` entry for `media.write_details`. It shows waiting ("Writing file {i} of {n}…" from `progress_json`), done (the Looks good and Undo forms), or failed (plain text per `error_code`). `needs_attention` gives "Tendshelf stopped before changing this file. Both copies are still in its folder." with the folder path, which is shown to managers only.
2. `pages/book.templ`, Details tab:
   - The "Write details into the files" opener, shown when there is no pending write.
   - A "Waiting for you" panel when there are swapped rows ("Listen to a little of the book, then choose Looks good or Undo.") with the two forms.
   - A `needs_attention` notice.
3. New `internal/web/write_copy.go` holds all the new strings.
4. `writes.Preflight` reads from the database and does `Lstat`/`Statfs` only, so a GET stays read-only on the library. Assert this in a test.

Acceptance tests (`internal/web/write_ui_test.go`, `routes_matrix_write_test.go`; fake ffmpeg through `mediatest`)
- Matrix for the five routes and the `jobViews` entry. Owner and admin are allowed; member (even with a download grant), guest, disabled and anonymous get the house answers. `Writes == nil` gives 404.
- `TestWriteUIFlow`: sheet, POST, poll to done, the Details tab shows the pending panel, Undo gives 303 and the files are restored (sha equal), the panel is gone, and an audit row is written.
- `TestWriteUIRefusal`: a recently modified file gives the sheet with the refusal and no button. POSTing anyway gives 422, and no ledger row is written.
- `TestWriteUIGetIsReadOnly`: the library folder's listing, mtimes and sizes are unchanged after GETting the sheet.
- Browser test at desktop-1440 and mobile-390: start, wait, Looks good.

Docs: architecture routes table (`/books/{id}/write*`).
Out of scope: writing several books at once, and a scheduled clean-up of backups.

---

## M4-10 Styles, captures and close-out (CSS + visual tests)

Invariants
- Use only `ts-` classes and tokens. There are no hard-coded colours. The four themes (dark, light, sepia, contrast at 7:1) work, as do 200 % text, touch targets of at least 44 px and visible focus.
- `css/pages-details.css` must stay at or under 3 KB gzip (the default `pages-*` budget). If it does not fit, report the overrun; do not squeeze the CSS.
- JS bytes are unchanged (`git diff --stat origin/main -- internal/web/static/js` is empty).

Steps
1. `internal/web/static/css/pages-details.css`:
   - Lookup form layout.
   - Compare sheet: the candidate list. The CompareTable README rules (`is-diff`, `is-chosen`/`is-other`, badges) already exist in `bundle.css:363+`; add only what is missing. On phone the stacked cards follow the README's "On phone" rule.
   - Cover picker grid: 3 columns on phone, 4 on tablet, 6 on desktop. The radio cards use the `radius-cover` 4 px corner and the control 12 px corner.
   - Upload field, write sheet file list, pending panel, author photo (round, 96 px on phone and 128 px from tablet) and bio measure (`size-measure`).
   Link it from `DetailsStyles` on the book and author pages.
2. Visual tests (the `visual_ids_dev_test.go` helpers, seeded ids, `visualTempDir`), each at 390, 768, 1024 and 1440:
   - lookup sheet;
   - compare sheet: waiting, table and nothing found;
   - cover picker;
   - write sheet: ready and refused;
   - pending panel;
   - author page with photo and bio.
   Board check: compare sheet against `screens/core/ComparePhone.png`, and author page against `screens/browse/AuthorPage.png` and `AuthorTablet.png`. Record the side-by-side captures in the report. Surfaces with no board are captured only.
3. Re-run the existing book-page visual set before and after the change (`TENDSHELF_VISUAL_OUT` plus `capturediff`). Only the Details tab may differ, and the differences are named in the report.
4. Accessibility checks: every checkbox has an `aria-label` ("Use online {field}"), the radio cards have their title as the label, and the axe-like assertions used by the existing browser tests pass for the new sheets.

Gates: `make check`, `make build`, `TestCSSBudget`, the new visual tests, and the touched browser specs at desktop-1440 and mobile-390.
Out of scope: TV layouts, `screens/directions/`, and new icons (use the existing set; `TestIconSet` unchanged).

---

## Open items for the orchestrator

- Before M4-4 merges: one read-only `curl` to `https://api.audnex.us/authors?name=…` and to `/authors/{asin}`, to confirm the shape guessed in M4-2 step 7.
- M4-5's real-ffmpeg test settles two guesses: whether the m4b chapter track is duplicated, and whether mp3 `CHAP` frames are read back. A `PLAN GAP` there comes back to the architect.
- The roadmap exit line ("parser tables from the Extractor tests pass") is met by M4-1, M4-2 and M4-4. The write round trip and crash recovery are met by M4-5 and M4-7.
