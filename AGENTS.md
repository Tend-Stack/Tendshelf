# Tendshelf contributor and agent rules

Read `README.md`, `docs/architecture.md` and the current phase in `docs/roadmap.md` before changing
code. Read `docs/vision.md` before changing anything a listener sees. These rules override habit.

## Check what exists first

Before any change, inventory what is already built (packages, tables, jobs, pages, settings, external
connections), including anything similar or partly built. Keep what works, extend what is partial,
replace only what is wrong. Never add a parallel or duplicate system. Say in the commit or report what
was reused, extended or replaced.

## Stack constraints (hard)

- **No Node.js, npm, npx, bundlers, Tailwind, PostCSS or any JavaScript build step** in the shipped app,
  the build, CI or development scripts. A change that needs `package.json` is rejected.
- **Go standard library first.** `net/http` with Go 1.22+ method/path patterns for routing; no web
  framework. Every new Go module needs operator approval and a row in `NOTICES.md` in the same commit.
  Approved and in use: `modernc.org/sqlite` v1.61.1 and `github.com/a-h/templ` v0.3.1100 (runtime; the
  CLI runs through the `tool` directive in `go.mod`, one version source), `golang.org/x/net` v0.61.0
  (`html`, `html/charset`), `golang.org/x/text` v0.43.0 (`unicode/norm`, `encoding/...`; ebook reading,
  `internal/ebook` only) and `github.com/ledongthuc/pdf` (PDF text layer, `internal/ebook/pdf.go` only) and `rsc.io/qr` v0.2.0 (QR codes as SVG, `internal/web/qr.go` only, decision 0028). Approved for later phases
  (operator, 2026-11-03), each added with its `NOTICES.md` row: Wails v3 (desktop
  module only), fsnotify, go-webauthn and an OpenID Connect client; see "Dependencies and approvals" in
  `docs/architecture.md`. Android (decision 0024), under `android/` only: `kotlin-test`, `junit` and `hamcrest` for tests, Kotlin (through AGP), `androidx.media3`
  (exoplayer, session, datasource), `androidx.webkit`, `androidx.core`, and `androidx.work` if downloads need it; every
  other module needs approval. Each gets a `NOTICES.md` row and is pinned by Gradle dependency verification. The
  "no build step" rule stays for the shipped server; Gradle is allowed under `android/` only, and fetches at build time only.
- **One server binary**, pure Go (`CGO_ENABLED=0` must build). Static assets and migrations are embedded
  with `embed`. External programs: `ffmpeg` and `ffprobe` only, located via config or `PATH`
  ([decision 0018](docs/decisions/0018-book-reader.md)). Tendshelf never downloads runtimes or models.
- **templ** for HTML. Generated `*_templ.go` files are committed so `go build` works without the templ
  CLI; `make generate` regenerates them and CI fails if the tree changes afterwards. Pin the templ CLI
  version to the `github.com/a-h/templ` version in `go.mod`.
- **HTMX** is vendored as one static file (no CDN). Plain JavaScript is allowed only in
  `internal/web/static/js/` as ES modules loaded directly by the browser: the audio player, the
  service worker, offline storage, the book uploader (`upload.js`, loaded only on the Add books page,
  decision 0015) and small progressive enhancements. No framework, no transpiling. `native-audio.js` is loaded only in the Android shell
  (`data-shell="android"`).
- **CSS** is hand-written in `internal/web/static/css/` with custom properties for design tokens.
- **SQLite** via the pure-Go driver, WAL mode, one database file in the data directory.
- **Wails v3** for desktop, in a separate Go module under `desktop/` so the server binary never links
  CGO, GTK or WebKit. Pages load from an http(s) origin, never Wails' asset scheme
  (decision 0020). The window keeps GTK's decorations with a hidden titlebar, never `Frameless`
  (decision 0023). **Android** is a separate Gradle project under `android/`: Kotlin, AndroidX and Media3,
  built by the Gradle wrapper with no Android Studio and no Node. It never links into the server. No Google Play
  services, Firebase or analytics. Other mobile platforms are later ([decision 0024](docs/decisions/0024-android-app.md)).

## templ and HTMX conventions

- Pages live in `internal/web/pages/`, reusable pieces in `internal/web/components/`. One templ
  component per file area; component names are nouns (`BookCard`, `ChapterList`).
- Canonical page URLs always return a full page. htmx 4 attribute inheritance is explicit: navigation
  uses `hx-boost:inherited="target:#main select:#main swap:outerSync"` and `hx-headers:inherited` on
  `<body>`, so the player in the layout keeps playing. `hx-target`, `hx-select` and `hx-swap` beside a
  plain `hx-boost` are not inherited. Events use colon names (`htmx:after:swap`, `htmx:config:request`);
  the request header `HX-Trigger` is now `HX-Source` (`tag#id`); `hx-delete` sends no form data.
- htmx 4 swaps 4xx and 5xx responses (only 204 and 304 do not swap), so an error response must be safe to
  swap: for an htmx request answer with an out-of-band toast fragment, not a full page.
- Fragments that are not pages live under `/hx/...` and return only the fragment. Every `/hx/` handler
  is also reachable by a plain form or link fallback where the action matters without JavaScript
  (login, playback start, progress mark-as-finished).
- No inline scripts, no `style=""` attributes, no `eval`. The CSP is `script-src 'self'; style-src 'self'`;
  htmx is configured with `<meta name="htmx-config" content='{"includeIndicatorCSS":false,...}'>`
  (`includeIndicatorStyles` and `allowEval` no longer exist in htmx 4; the CSP alone blocks eval). Because
  htmx 4 runs them through `Function`, the following are banned everywhere: `hx-on`, `js:` values and
  trigger filters (`hx-trigger="x[cond]"`). `globalViewTransitions` is now `transitions`.
- Unsafe methods (POST, PUT, PATCH, DELETE) carry the CSRF token in the `X-CSRF-Token` header (set once
  with `hx-headers` on `<body>`) or a hidden form field; the server checks it on every unsafe request.
- Long work never blocks a request: start a job, return a fragment that polls `/hx/jobs/{id}` with
  `hx-trigger="every 2s"` until the job finishes, then swaps in the result.
- Error fragments use plain words ("This file is copy-protected, so Tendshelf will not open it"),
  never stack traces or raw ffmpeg output. Details go to the server log.

## Database and migrations

- Schema version is `PRAGMA user_version`; `PRAGMA application_id` is `0x54534846` ("TSHF"). Both are
  checked on open. Migrations are numbered SQL files in `internal/db/migrations/NNNN_name.sql`,
  embedded, forward-only, each applied in its own transaction that also sets `user_version`.
- **Older-binary refusal**: open the existing file read-only first, read `application_id` and
  `user_version`; if the version is newer than the binary supports, or the application id is not
  Tendshelf's, refuse with a typed error (`db.ErrSchemaTooNew`, `db.ErrNotTendshelf`) and do not open
  it writable, checkpoint it or change a byte.
- Before migrating an existing database, write a verified copy with `VACUUM INTO` to
  `<data>/backups/tendshelf-v<old>-<UTC timestamp>.db`; keep the newest three.
- Every migration ships with a test that migrates from the previous version's fixture and from empty,
  and checks data survives. Never edit a released migration.
- Times are INTEGER Unix milliseconds UTC; positions and durations are INTEGER milliseconds; IDs are
  ULID-style TEXT (`internal/db/id.go`).

## Audio files are the user's

- Library files are read-only to Tendshelf except through two paths. (1) The "write details into the
  file" job, which stream-copies to a temporary file, verifies audio hash, duration, chapters and tags,
  keeps the original as a backup and supports Undo. (2) Adding books by upload
  ([decision 0015](docs/decisions/0015-upload-into-library.md)), only in `internal/upload`: it writes only
  into the one folder the owner chose for uploads in that library (off by default), and a contributor's
  uploads only into that person's own folder inside it
  ([decision 0029](docs/decisions/0029-contributors.md)), only creates new
  folders and files, records every path before creating it in `upload_paths`, builds the book inside a
  hidden `.tendshelf-upload-<id>` folder and publishes it by one rename to a name that did not exist, and
  never overwrites, renames or deletes anything it did not create. "Remove this book" deletes only
  `upload_paths` files still unchanged since they were placed (size, mtime and quick hash), then only the
  empty folders it created. No other code path may write, rename or delete a library file.
- Copy-protected files (`.aax`, `.aaxc`, `.aa`, `.m4p`, or streams ffprobe reports as encrypted) are
  listed as refused and never decoded, streamed or remuxed. Tendshelf never removes or works around DRM.
  An upload with a copy-protected file is refused before its bytes are sent (by extension) or after
  ffprobe reports encryption on the staged copy; its staged bytes are deleted and nothing reaches the
  library folder.
- Ebook files (text editions, `docs/reader.md`) follow the same rules: library ebooks are
  read-only, uploads are kept byte-identical, merging an ebook book into an audiobook and "Not the same book"
  change database rows only and never open, write, rename or delete a file, and copy-protected ebooks (Adobe ADEPT, Apple, Readium LCP,
  encrypted EPUB content, PDF `/Encrypt`, encrypted MOBI, any KFX) are refused, never decrypted. Add books also takes ebooks
  (`internal/upload`, same rules): a `.kfx` is refused before its bytes are sent, and a staged ebook that is copy-protected or
  cannot be opened refuses the whole upload with its staged bytes deleted and nothing placed.
- ffmpeg/ffprobe run with `-protocol_whitelist file,pipe` (ffmpeg also `-nostdin`; ffprobe has no such
  option and reads /dev/null), fixed argument lists (never a shell), a context deadline and output size
  limits.

## Security and privacy

- The Android app talks only to the server the person typed in, with no other outbound request. Sign-out deletes the
  person's downloads. ("The only package that creates an `http.Client`" stays a server rule.)
- **No telemetry**, analytics, crash reporting, update pings or third-party assets (fonts, CDNs, map
  tiles). The only outbound requests are user-initiated metadata lookups and cover downloads, through
  `internal/metadata/online`, the only package in the server that creates an `http.Client`. The one other outbound
  connection is the admin-configured SMTP server for invitation emails (`internal/share/mail.go`).
  The desktop shell also checks `/healthz` on the server the person typed, at Connect and at launch
  (`internal/desktopshell`, decision 0020). That is its own client, and it sends no credentials.
- All authorization goes through `share.Can(user, action, target)`. Listing and search queries filter
  by grants in SQL; never fetch everything and filter in Go (counts and search must not leak titles).
- A contributor is a member with `users.adds_books = 1` (decision 0029). They edit, look up and remove only books their own uploads placed (`upload_books`,
  read inside `share.Can`, never from the request), never write into a library file, and remove only files they placed.
- Session, invite and API tokens are random 32-byte values; only their SHA-256 is stored. The one exception is an app key (OPDS, KOReader
  sync): 24 typable characters from `a-z2-7` (120 bits), one surface each, rate limited, SHA-256 stored ([decision 0025](docs/decisions/0025-app-keys.md)).
- **A device token never becomes a session.** No route takes a bearer token and answers with a cookie, so a token reaches
  `/api/v1/` only and never a page, `/hx/`, `/admin` or `/me` route. The phone app signs in on the server's own page and trades a
  one-time code plus its PKCE verifier for a token at `POST /api/v1/auth/token`; the app never sees the password
  ([decision 0026](docs/decisions/0026-app-sign-in.md)).
- The server binds `127.0.0.1:8095` by default; exposing it is an explicit setting.
- The service worker (`/sw.js`, source `internal/web/static/js/sw.js`) answers only hashed `/static/` files and,
  from the signed-in person's own cache, `/stream/{id}` (with or without `?compat=1`), `/covers/{bookID}`,
  `/peaks/{id}`, `/hx/player/{bookID}`, `/hx/books/{id}/read/chapters/{idx}`, `/books/{id}/read/images/{blockID}`,
  the `/offline` shell for a navigation that cannot reach the server, and the book's reader shell for a
  navigation to `/books/{id}/read` (or `?sheet=contents`) that cannot reach it. It never caches or answers
  `/s/...`, `/invite/...`, `/pair/...`, `/login`, `/setup`, `/logout`, `/api/...`, `/reading/...`, any request with
  `download=1`, any non-GET request, or any other page. It is never registered inside a Wails window, inside the Android
  app's window (`/sw.js` answers 404 there), on share-link pages or outside a secure context. Sign-out and a change of person delete that person's caches
  and offline records (decisions [0012](docs/decisions/0012-offline-pocket.md),
  [0019](docs/decisions/0019-offline-reading.md)).

- `tendshelf:` carries only a link, never a code (decision 0028): the app's one way in from outside is
  `tendshelf://open?link=<address>`, treated as untrusted text, and no page or message ever offers a bare
  `tendshelf:` link (the server's `intent:` address names the app's package).

## Accessibility and layout

- WCAG 2.2 AA: contrast 4.5:1 for text, visible focus, every control reachable and labelled for a
  keyboard and a screen reader, touch targets at least 44 by 44 CSS px, no information by colour alone,
  `prefers-reduced-motion` and `prefers-color-scheme` honoured, text resizable to 200 %.
- Breakpoints follow the design: phone 360-767 (designed at 390, primary), tablet 768-1279 (designed
  at 1024), desktop 1280+ (designed at 1440). Check at 390, 768, 1024 and 1440 px. The player must be
  usable one-handed at 390 px.
- Plain words in the interface. Advanced options sit behind a disclosure.

## Design

- The visual source of truth is `docs/design/tendshelf-handoff/` ([decision 0002](docs/decisions/0002-design-handoff.md)). Buttons, chips, segmented controls and fields use the shared 12 px corner `--radius-control`, not pills ([decision 0004](docs/decisions/0004-control-corners.md)).
  Read its `README.md` and `docs/HANDOFF.md` before building a page; inside it, `HANDOFF.md` >
  component `README.md` > `BRAND.md` > board PNG > board HTML. This file and `docs/architecture.md`
  still win on stack, routes, security and privacy.
- Its `tokens.css` and `bundle.css` are the app's stylesheets (`internal/web/static/css/`); extend
  `bundle.css` with new `ts-` classes. No hard-coded colours, no inline styles; every page works in
  `dark`, `light`, `sepia` and `contrast` (7:1). Board HTML is reference markup, not code to paste.
- The Android app's bundled start and offline pages use the same `tokens.css` and `bundle.css`, copied at build time.
- Fonts are self-hosted (Fraunces, Fira Sans, Fira Mono); ignore the handoff's Google Fonts link.
- Use the copy deck's words exactly; a missing string follows the voice rules in `BRAND.md` and is
  listed in the report. 13 px is the smallest text. One resume button per book on any screen: for a book in two formats it belongs to the format used last (`lastFormat`, the newer of the listening and reading places), and the other format is a plain secondary button with no place and no time.
- No TV layouts and nothing from `screens/directions/`.

## NOTICES discipline

When you port logic closely from another project (Audiobookshelf is GPL-3.0, compatible with
AGPL-3.0), add the Tendshelf file and the source file names to `NOTICES.md` in the same commit, and a
one-line comment at the top of the Go file naming the source. Read for understanding; write idiomatic Go.
Do not copy JavaScript structure, models or API shapes wholesale. Vendored files and new modules also
get a row.

## Tests and checks

- Every change ships with tests: table-driven unit tests for parsers and merges, `httptest` tests for
  handlers (including the forbidden cases for each role), migration tests, and a fake ffmpeg/ffprobe
  (a small Go test binary) for media code. Real ffmpeg tests are opt-in via `TENDSHELF_TEST_FFMPEG`.
- Test fixtures are synthetic or public-domain audio generated by the tests; never commit real
  audiobooks, covers or personal data. Generated test output goes under `.local/tests/<suite>/<run-id>/`
  (ignored) and is removed when the run is recorded.
- Page packages record a side-by-side check against their boards at 390, 768, 1024 and 1440 px using
  the opt-in capture helper (`TENDSHELF_TEST_BROWSER`, system Chromium driven from a Go test; no Node).
- Captures are repeatable: to prove a change is pixel-identical, run `TENDSHELF_VISUAL_OUT=/tmp/before make visual`, make the change, run it again into `/tmp/after`, then `go run ./internal/testutil/capturediff /tmp/before /tmp/after` (prints the names that differ and "N of M identical"); `make visual-compare` runs the set twice and does the same. New visual tests must not put random values on screen (map order, `t.TempDir()` paths, random ids, tokens): use the `visualTempDir`/seeded-id helpers in `internal/web/visual_ids_dev_test.go`.
- Android: `make apk` builds the release APK (signed only when the `TS_ANDROID_*` variables are set, see `docs/android.md`); `make apk-debug` builds the debug APK (`android/`, Gradle wrapper); an emulator checklist is recorded in the
  roadmap's status note. A CI job comes with A10, gated on the Android SDK being on the runner.
- Gates: `make check` (gofmt, `go vet`, `go test -race ./...`) and `make build`. Run both before a commit.
- Size budgets for CSS and JS measure the gzip bytes the server sends (static assets are precompressed at start-up). Write JS and CSS for reading: one statement per line, names that say what they do, and a comment wherever the reason is not obvious. Never minify, squeeze or strip comments to meet a budget; report the overrun instead.
- Do not report a check you did not run.

## Where things live

- Docs: `docs/` (vision, feature map, architecture, roadmap), `docs/design/` (visual design inputs),
  `docs/decisions/NNNN-title.md` for decisions that change a rule here.
- Update `docs/roadmap.md` status with each completed slice: scope, checks run, limits, next step.
- Commits: conventional style (`feat(scan): ...`). No secrets, local paths, real books or data files.
