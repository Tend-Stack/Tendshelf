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
  Approved so far: none (see "Dependencies awaiting approval" in `docs/architecture.md`).
- **One server binary**, pure Go (`CGO_ENABLED=0` must build). Static assets and migrations are embedded
  with `embed`. External programs: `ffmpeg` and `ffprobe` only, located via config or `PATH`.
- **templ** for HTML. Generated `*_templ.go` files are committed so `go build` works without the templ
  CLI; `make generate` regenerates them and CI fails if the tree changes afterwards. Pin the templ CLI
  version to the `github.com/a-h/templ` version in `go.mod`.
- **HTMX** is vendored as one static file (no CDN). Plain JavaScript is allowed only in
  `internal/web/static/js/` as ES modules loaded directly by the browser: the audio player, the
  service worker, offline storage and small progressive enhancements. No framework, no transpiling.
- **CSS** is hand-written in `internal/web/static/css/` with custom properties for design tokens.
- **SQLite** via the pure-Go driver, WAL mode, one database file in the data directory.
- **Wails v3** for desktop, in a separate Go module under `desktop/` so the server binary never links
  CGO, GTK or WebKit. Native mobile is later; the PWA comes first.

## templ and HTMX conventions

- Pages live in `internal/web/pages/`, reusable pieces in `internal/web/components/`. One templ
  component per file area; component names are nouns (`BookCard`, `ChapterList`).
- Canonical page URLs always return a full page. Navigation uses `hx-boost` with
  `hx-target="#main" hx-select="#main" hx-swap="outerHTML"` so the player in the layout keeps playing.
- Fragments that are not pages live under `/hx/...` and return only the fragment. Every `/hx/` handler
  is also reachable by a plain form or link fallback where the action matters without JavaScript
  (login, playback start, progress mark-as-finished).
- No inline scripts, no `hx-on`, no `eval`. The CSP is `script-src 'self'`; htmx is configured with
  `<meta name="htmx-config" content='{"includeIndicatorStyles":false,"allowEval":false}'>`.
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

- Library files are read-only to Tendshelf except through the "write details into the file" job, which
  stream-copies to a temporary file, verifies audio hash, duration, chapters and tags, keeps the original
  as a backup and supports Undo. No other code path may write, rename or delete a library file.
- Copy-protected files (`.aax`, `.aaxc`, `.aa`, `.m4p`, or streams ffprobe reports as encrypted) are
  listed as refused and never decoded, streamed or remuxed. Tendshelf never removes or works around DRM.
- ffmpeg/ffprobe run with `-protocol_whitelist file,pipe` (and `-nostdin`), fixed argument lists (never
  a shell), a context deadline and output size limits.

## Security and privacy

- **No telemetry**, analytics, crash reporting, update pings or third-party assets (fonts, CDNs, map
  tiles). The only outbound requests are user-initiated metadata lookups and cover downloads, through
  `internal/metadata/online`, the only package that creates an `http.Client`. The one other outbound
  connection is the admin-configured SMTP server for invitation emails (`internal/share/mail.go`).
- All authorization goes through `share.Can(user, action, target)`. Listing and search queries filter
  by grants in SQL; never fetch everything and filter in Go (counts and search must not leak titles).
- Session, invite and API tokens are random 32-byte values; only their SHA-256 is stored.
- The server binds `127.0.0.1:8095` by default; exposing it is an explicit setting.

## Accessibility and layout

- WCAG 2.2 AA: contrast 4.5:1 for text, visible focus, every control reachable and labelled for a
  keyboard and a screen reader, touch targets at least 44 by 44 CSS px, no information by colour alone,
  `prefers-reduced-motion` and `prefers-color-scheme` honoured, text resizable to 200 %.
- Design at 390 px wide first, then 768 and 1440. The player must be usable one-handed at 390 px.
- Plain words in the interface. Advanced options sit behind a disclosure.

## Research module (fair practice, hard rules)

The Research module is off by default, owner-enabled, admin-only. When disabled, its routes are not
registered (404) and nothing in the interface mentions it. When enabled:

- Owned files only, local analysis only. Copy-protected files are refused, as everywhere.
- Keep the numbers, delete the clips. Clips are deleted after measuring unless an admin explicitly keeps
  them for listening, for at most 30 days. Clips are never served to non-admins, never included in
  downloads, feeds, backups or exports, and never used to train or clone a voice.
- Transcripts from optional speech-to-text are used for counts only and never stored.
- Exports contain numbers and categories only (`tendvox-reference-styles` v1), never audio or text.

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
- Gates: `make check` (gofmt, `go vet`, `go test -race ./...`) and `make build`. Run both before a commit.
- Do not report a check you did not run.

## Where things live

- Docs: `docs/` (vision, feature map, architecture, roadmap), `docs/design/` (visual design inputs),
  `docs/decisions/NNNN-title.md` for decisions that change a rule here.
- Update `docs/roadmap.md` status with each completed slice: scope, checks run, limits, next step.
- Commits: conventional style (`feat(scan): ...`). No secrets, local paths, real books or data files.
