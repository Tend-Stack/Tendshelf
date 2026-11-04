# M5 R3 Alignment: plan

Sources: `docs/read-along.md` §2 (pipeline, worker, storage, job, failure codes) and §7 (tests), roadmap R3,
decision 0001 points 3-4, `docs/architecture.md` (process model, `internal/align*` rows, tables, jobs, routes),
R2 plan (`m5-r2-pairing.md`). Reused from TendVocal (same author; NOTICES rows): `runtimes/alignment/worker.py`,
`model-{en,es}.json`, `dependencies-{torch,pypi}.lock`, `pyproject.toml`, `internal/review/{number_forms,
fidelity_normalize}.go`. Exit: synthetic accuracy (clean ≥ 99 %, each perturbed case ≥ 95 % of narrated
sentences start within ±250 ms; zero invented or out-of-order times); crash/resume tests; opt-in real run recorded.

## Packages and order

| Package | Runs | Shared files it touches |
| --- | --- | --- |
| P1 Data, normaliser, doc loader, job hold (~200 calls) | first; commits step P1.1 (migration) first | `internal/db/db.go`, `internal/jobs/{jobs,runner,store}.go`, `internal/ebook/doc.go`, `NOTICES.md`, `docs/architecture.md`, roadmap |
| P2 `internal/align/runtime` (~240) | after P1.1 is merged | `internal/media/priority_*.go` (+2 call sites), `AGENTS.md`, `docs/architecture.md`, `docs/read-along.md` §2 Worker, new decision 0017, roadmap |
| P4 `runtimes/align/` worker (~150) | after P1.1, beside P2 | `NOTICES.md`, `Makefile`, roadmap |
| P3a `internal/align` core and accuracy harness (~250) | after P1 merged; beside P2/P4 | `docs/read-along.md` §2 stages 4-6 and timing payload, roadmap |
| P3b `align.book` job and service (~230) | after P2 and P3a merged | `internal/media/pcm.go` (new), roadmap |
| P5 `/admin/runtimes`, Text tab, wiring (~250) | after P3b and P4 merged | `cmd/tendshelf/{main,hooks}.go`, `routes_jobs.go`, `routes_ebooks_pair.go`, `ebook_copy.go`, `pages/book_text.templ`, page CSS, `routes_matrix*_test.go`, `a11y_pages_test.go`, `docs/architecture.md` routes, roadmap |

At most three at once: wave 1 P1, then P2 + P4 (+ the rest of P1); wave 2 P3a beside P2/P4; wave 3 P3b; wave 4 P5.
Each appends its own roadmap paragraph under R3; the orchestrator resolves roadmap, NOTICES and architecture merges.
Test prefixes (unique, also for package-level helpers): P1 `TestMigration0018*`, `TestAlignStore*`, `TestNormalize*`,
`TestReadDoc*`, `TestWordSpans*`, `TestJobsHold*` (helpers `alst*`, `normT*`); P2 `TestRuntime*` (helpers `rt*`);
P4 `TestWorkerBundle*`; P3a `TestAlignCore*`, `TestAlignAccuracy*` (helpers `core*`, `syn*`); P3b `TestAlignJob*`
(helpers `ajob*`); P5 `TestAdminRuntimes*`, `TestTextAlign*` (helpers `art*`, `talign*`).

## Invariants (all packages)

1. Tendshelf never downloads a runtime or a model, and the worker never gets a network path: no `http.Client`, no
   listener, no port. It speaks only over its own stdin/stdout pipes (decision 0017). `internal/metadata/online`
   stays the only package that creates an `http.Client`.
2. Only `internal/align/runtime` starts the worker: absolute Python path, fixed argument list, no shell, environment
   rebuilt from an allowlist, per-launch nonce, deadlines on ready and on each request, capped stdout, stderr and
   payloads, lowered priority, `Pdeathsig` on Linux plus exit on stdin EOF.
3. Times are measured, never interpolated: a word is timed only by Viterbi with score ≥ 0.30; a sentence's start and
   end come from timed words only; anything else is a gap with a reason. Zero out-of-order times (post-pass drops them).
4. The server builds with `CGO_ENABLED=0`, starts and passes every test without Python; real runs are opt-in
   (`TENDSHELF_TEST_ALIGN_RUNTIME`). Missing runtime only makes read-along unavailable (`runtime_missing`).
5. `align.book` never sets `HoldOnRecovery`; its durable checkpoints are its own data: `.em` windows and
   `alignment_chapters` rows with `state='aligned'` plus their timing file. A restart recomputes neither.
6. A person decides at `held`: window failed 3 times (`worker_failed`), runtime identity changed (`runtime_changed`),
   an audio quick hash changed mid-job (`audio_changed`). Resume accepts the current runtime and audio.
7. Every write to `alignments` from the job repeats `WHERE id=? AND job_id=? AND state IN ('queued','emitting',
   'aligning','held')`, so a Cancel or Discard is never overwritten.
8. Library audio and ebook files are only read. Fixtures are synthetic (`internal/testutil/wav`, generated
   emissions); nothing from `~/Books`; real-run material lives under `.local/tests/align-accuracy/`, never committed.

## Decisions

- **Pipes, not loopback HTTP** (operator decision 1). The worker is a private child: one request at a time over
  stdin/stdout with a JSON header line plus a raw binary body. It needs no port, no token against other local
  processes, no HTTP client exception, and stdin EOF ends it even when `Pdeathsig` misses. The per-launch nonce from
  decision 0001 stays (in env, echoed in the ready line and every response) to bind replies to this launch.
  Decision 0017 amends 0001 point 3; the rejected option was an `http.Client` confined to `internal/align/runtime`
  as a documented loopback exception (more code, a listening port, and a second exception to the rule).
- **Worker script embedded** (operator decision 2): `runtimes/align/worker.py` is embedded in the binary through
  package `runtimes/align` and written to `<data>/runtimes/align/worker/worker-<sha12>.py` (0600) before launch, so
  protocol and worker always match the binary. The owner installs only Python packages and model files.
- **Discovery** (operator decision 3): flags/env `-align-python` (`TENDSHELF_ALIGN_PYTHON`, default
  `<data>/runtimes/align/venv/bin/python`), `-align-models` (`TENDSHELF_ALIGN_MODELS`, default
  `<data>/runtimes/align/models`, one subfolder per language: `en/`, `es/`), `-align-threads`
  (`TENDSHELF_ALIGN_THREADS`, default `max(1, min(4, NumCPU/2))`), `-align-cache-gib` (default 2).
- **Pins reuse TendVocal's verified set** (operator decision 4): Python 3.12.14, `torch==2.8.0+cpu`,
  `torchaudio==2.8.0+cpu`, `transformers==5.19.0`, `numpy==2.5.3`, `safetensors==0.8.0`, hash-locked;
  `facebook/wav2vec2-base-960h@22aad52d435eb6dbaf354bdad9b0da84ce7d6156` (en),
  `jonatasgrosman/wav2vec2-large-xlsr-53-spanish@80106547eee5065e01618d6588c22c256e8d0e70` (es).
- **Jobs gain a handler hold**: `jobs.Error.Hold` sends the job to `held` without spending an attempt (like a
  shutdown requeue). The runner had no way for a handler to ask for a person; `write_details` is unchanged.
- **A held row means a person resumed**: when the handler starts and finds `alignments.state='held'`, it adopts the
  current audio key and model and continues. So Resume from `/admin/jobs` or from the Text tab behave the same.
- **Multi-region mapping**: a 10-minute audio block may map to up to 4 text regions (densest window, remove its
  hits, repeat while ≥ 3 hits remain). The doc's single densest window would lose one of two swapped chapters.
- **Text between regions is aligned only when contiguous** in both audio and text; otherwise `not-in-recording`.
- **Coarse transcript is not stored**; it is recomputed from the cached `.em` (cheap); read-along.md is corrected.
- **Package name** of `internal/align/runtime` is `runtime`; every importer aliases it `alignrt`; inside it the
  standard library is imported as `goruntime`.
- **Check is a job** (`align.check`): hashing 1.7 GB and loading torch take seconds to a minute, longer than a request.
- **Routes follow R2's shape** (`/books/{id}/text/{fileID}/...` forms, htmx-boosted) instead of the planned
  `/hx/editions/...` names; `docs/architecture.md` routes are rewritten in P5.

## Migration `internal/db/migrations/0018_alignments.sql` (P1.1)

```sql
-- 0018_alignments: read-along R3 (docs/read-along.md §2). runtime_components: one checked model per (kind,
-- language); usable only when check_error = '' and disabled = 0. alignments: one job per (edition, audio set);
-- at most one active row per edition. alignment_chapters: per text chapter, timing file <data>/align/<chapter_key>.json.gz.
CREATE TABLE runtime_components (
  id            TEXT PRIMARY KEY,
  kind          TEXT NOT NULL CHECK (kind IN ('emissions-model')),
  name          TEXT NOT NULL CHECK (length(name) > 0),
  language      TEXT NOT NULL CHECK (length(language) BETWEEN 2 AND 8),
  revision      TEXT NOT NULL CHECK (length(revision) > 0),
  model_sha256  TEXT NOT NULL CHECK (length(model_sha256) = 64),
  files_json    TEXT NOT NULL CHECK (json_valid(files_json) AND json_type(files_json) = 'array'),
  identity_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(identity_json) AND json_type(identity_json) = 'object'),
  python_path   TEXT NOT NULL,
  worker_sha256 TEXT NOT NULL CHECK (length(worker_sha256) = 64),
  licence_spdx  TEXT NOT NULL,
  licence_url   TEXT NOT NULL,
  source_url    TEXT NOT NULL,
  check_error   TEXT NOT NULL DEFAULT '',
  checked_at    INTEGER NOT NULL,
  disabled      INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)),
  UNIQUE (kind, language)
) STRICT;

CREATE TABLE alignments (
  id              TEXT PRIMARY KEY,
  edition_id      TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  book_id         TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  source          TEXT NOT NULL DEFAULT 'model' CHECK (source IN ('model','overlay')),
  recipe          TEXT NOT NULL CHECK (length(recipe) > 0),
  runtime_id      TEXT REFERENCES runtime_components(id) ON DELETE SET NULL,
  model_sha256    TEXT CHECK (model_sha256 IS NULL OR length(model_sha256) = 64),
  audio_key       TEXT NOT NULL CHECK (length(audio_key) = 64),
  doc_sha256      TEXT NOT NULL CHECK (length(doc_sha256) = 64),
  state           TEXT NOT NULL CHECK (state IN ('queued','emitting','aligning','done','partial','failed','cancelled','held')),
  labels_json     TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(labels_json) AND json_type(labels_json) = 'array'),
  job_id          TEXT,
  coverage        REAL NOT NULL DEFAULT 0 CHECK (coverage >= 0 AND coverage <= 1),
  sentences_total INTEGER NOT NULL DEFAULT 0 CHECK (sentences_total >= 0),
  sentences_timed INTEGER NOT NULL DEFAULT 0,
  error_code      TEXT,
  created_at      INTEGER NOT NULL,
  updated_at      INTEGER NOT NULL,
  finished_at     INTEGER,
  superseded_by   TEXT REFERENCES alignments(id) ON DELETE SET NULL,
  CHECK (sentences_timed BETWEEN 0 AND sentences_total)
) STRICT;
CREATE UNIQUE INDEX alignments_active ON alignments(edition_id) WHERE state IN ('queued','emitting','aligning','held');
CREATE INDEX alignments_edition ON alignments(edition_id, created_at);
CREATE INDEX alignments_book ON alignments(book_id);
CREATE INDEX alignments_job ON alignments(job_id) WHERE job_id IS NOT NULL;

CREATE TABLE alignment_chapters (
  alignment_id    TEXT NOT NULL REFERENCES alignments(id) ON DELETE CASCADE,
  chapter_idx     INTEGER NOT NULL CHECK (chapter_idx >= 0),
  state           TEXT NOT NULL CHECK (state IN ('pending','aligned','unmatched','failed')),
  chapter_key     TEXT CHECK (chapter_key IS NULL OR length(chapter_key) = 64),
  start_ms        INTEGER,
  end_ms          INTEGER,
  sentences_total INTEGER NOT NULL DEFAULT 0 CHECK (sentences_total >= 0),
  sentences_timed INTEGER NOT NULL DEFAULT 0,
  mean_confidence REAL,
  updated_at      INTEGER NOT NULL,
  PRIMARY KEY (alignment_id, chapter_idx),
  CHECK (sentences_timed BETWEEN 0 AND sentences_total),
  CHECK (start_ms IS NULL OR (start_ms >= 0 AND end_ms IS NOT NULL AND end_ms >= start_ms))
) STRICT, WITHOUT ROWID;
CREATE INDEX alignment_chapters_key ON alignment_chapters(chapter_key) WHERE chapter_key IS NOT NULL;
```
`internal/db/migration_0018_test.go`: `TestMigration0018Alignments` opens `ms[:17]` with `supported: 17` (pattern of
`migration_0017_test.go`), seeds a book, ebook file and edition (`ebookFileSQL` helper from the 0016 test), reopens at
18: the edition survives; insert a component, a `done` and a `queued` alignment for the edition (both succeed), a
second `queued` fails (`alignments_active`); delete the component → `runtime_id IS NULL`; delete the edition → both
alignments and their chapter rows are gone. `TestMigration0018FromEmpty`: bad `state`, `coverage=1.5`,
`sentences_timed > sentences_total`, `start_ms` without `end_ms`, duplicate (`kind`,`language`) each fail.
`internal/db/db.go`: `SchemaVersion = 18`.

## P1 Data, normaliser, doc loader, job hold

Steps:
1. Migration, test and `SchemaVersion` above; commit `feat(db): migration 0018 alignments` alone.
2. `internal/ebook/doc.go`: `ReadDoc(dir, sha string) (*Doc, error)`: open `DocPath`, gzip reader, `io.LimitReader` at
   512 MiB uncompressed (`ErrDocTooLarge`), decode, recompute `DocSHA(Encode(doc))`; mismatch → `ErrDocCorrupt`.
   `type WordSpan struct{ Text string; Sentence int; DotAfter bool }`; `WordSpans(b Block) []WordSpan` (UTF-16 offsets
   → string via `unicode/utf16`; sentence = the span whose [start,end) holds the word start, -1 if none; `DotAfter`
   when the next code unit is `.`); `SentenceID(blockID string, k int) string` = `blockID + ".s" + strconv.Itoa(k)`.
3. `internal/align/normalize.go` (package `align`; header comment and NOTICES row naming TendVocal
   `internal/review/number_forms.go` and `fidelity_normalize.go`): `const NormalizeRecipe = "tendshelf-normalize-v1"`;
   `type Unit struct{ Src int; Norm string; Wildcard bool }`; `Normalize(lang string, words []ebook.WordSpan) []Unit`.
   Rules: NFC (`golang.org/x/text/unicode/norm`), lower case, `’ʼ` → `'`, hyphen/dash splits words; English
   cardinals/ordinals/years-with-context per the port; a bare 4-digit number → one wildcard unit; Spanish cardinals
   0-999 999 999 (`un/uno/una` → `uno`, `veintiún`, `cien/ciento`, `quinientos`, `mil`, `millón/millones`); Spanish
   ordinals (`1º`) → wildcard; abbreviations only when `DotAfter`: en `mr→mister mrs→missus dr→doctor vs→versus
   etc→et cetera`, `st` → wildcard; es `sr→señor sra→señora dr→doctor dra→doctora ud→usted uds→ustedes
   etc→etcétera`. Other languages: words lower-cased, no number rules. A word that yields nothing stays one wildcard.
4. `internal/align/store.go`: `const RecipeAlign = "tendshelf-align-v1"`; types `Alignment` (all columns; `Labels
   []string`), `ChapterRow`; `Store` with `Insert(ctx, a) error`, `Get(ctx, id)`, `ByJob(ctx, jobID)`,
   `Active(ctx, editionID) (Alignment, bool, error)`, `Latest(ctx, editionID)` (newest `done`/`partial` with
   `superseded_by IS NULL`), `SetState(ctx, id, jobID, state, code string) (bool, error)` (invariant 7 `WHERE`),
   `Finish(ctx, id, jobID, Result-like fields) (bool, error)` (also sets `superseded_by` on older done/partial of the
   edition, one tx), `Adopt(ctx, id, jobID, audioKey, modelSHA, runtimeID)` (only from `held`), `Chapters(ctx, id)`,
   `PutChapter(ctx, id, jobID, ChapterRow)` (upsert, guarded by the parent's active state in the same tx),
   `ChapterByKey(ctx, key) (ChapterRow, bool, error)` (any alignment, `state='aligned'`). IDs `db.NewID("al")`.
5. `internal/jobs/jobs.go`: `Error` gains `Hold bool`; `store.go`: `sqlHoldHandler` = `sqlHold` with
   `attempts=attempts-1`; `runner.go` `finish` default branch: `errors.As(herr, &je) && je.Hold` → `sqlHoldHandler`.
6. `docs/architecture.md` table rows 191-193 rewritten to the real columns; jobs line ~236 mentions the handler hold.

Tests: `TestMigration0018*`; `TestReadDocRoundTrip` (WriteDoc then ReadDoc equal), `TestReadDocCorrupt` (edited
gzip → `ErrDocCorrupt`), `TestReadDocTooLarge` (limit lowered through an unexported var); `TestWordSpans` (table:
ASCII, `é`, emoji surrogate pair, `Mr.`, word outside sentences); `TestNormalizeEnglish`/`TestNormalizeSpanish`
(tables: `21→twenty one`, `3rd→third`, `1984→*`, `Mr.→mister`, `St.→*`, `121→ciento veintiuno`, `1º→*`,
`naïve` stays `naïve`); `TestAlignStoreGuard` (SetState after cancel returns false and leaves `cancelled`),
`TestAlignStoreSupersede`, `TestAlignStoreChapterByKey`; `TestJobsHoldFromHandler` (held, code kept, attempts back
to 0, Resume runs again with `Attempt` 1), `TestJobsHoldNotForWriteDetails` (shutdown path unchanged).
Gates: `go test -race ./internal/db/... ./internal/ebook/... ./internal/align/... ./internal/jobs/...`, `make check`,
`make build`. Out of scope: Spanish ordinals spelled out, other languages' numbers, timing-file GC.

## P2 `internal/align/runtime` (package `runtime`, alias `alignrt`)

Invariants: 1, 2, 4. No `net` or `net/http` import in the package (test-enforced).
Steps:
1. `internal/media/priority_{unix,other}.go`: rename `lowerPriority` → exported `LowerPriority`; update the callers.
2. `config.go`: `type Config struct{ Python, ModelsDir, WorkDir string; Script []byte; Manifests map[string]Manifest;
   Threads int; IdleStop, ReadyTimeout, RequestTimeout, DescribeTimeout time.Duration; Env []string }` (defaults 5 m,
   180 s, 120 s, 180 s; `Env` tests only). `type Manifest struct{ Format string `"tendshelf-emissions-model"`; Version
   int; Language, Repository, Revision, LicenceSPDX, LicenceURL, SourceURL string; Files []ManifestFile{Name string;
   Size int64; SHA256 string} }`; `ParseManifest([]byte) (Manifest, error)` (names: no `/`, `\`, `..`; sha 64 hex).
   `ModelSHA(files []ManifestFile) string` = SHA-256 of `name\tsize\tsha256\n` lines sorted by name.
   `Locate(cfg) error` codes: `python_missing` (not found or not absolute after `filepath.Abs`), `python_unsafe`
   (not a regular executable file, or it or its folder is world-writable), `models_missing`.
3. `components.go`: `Component` (row), `Components` store: `Get(ctx, lang)`, `Upsert(ctx, c)`, `Usable(ctx, lang,
   pythonPath, workerSHA) (Component, bool, error)` (`check_error=''`, `disabled=0`, same python path and worker sha).
4. `check.go`: `Check(ctx, cfg, lang) (Component, error)`: Locate; manifest for lang; per file `os.Lstat` (regular,
   size equal) then SHA-256 → `model_mismatch`/`models_missing`; write script; run describe through `runOnce`
   (stdout cap 1 MiB, stderr cap 64 KiB) with args `[-I -B -u <script> --describe --model-dir <dir> --language
   <lang> --threads N --device cpu]`; parse `Identity`; must equal manifest (repository, revision, language) and
   `ModelSHA` → else `identity_mismatch`; failure to run → `describe_failed`. Upsert the row in every case
   (`check_error` = code or `''`). `KindCheck = "align.check"`, `Register(r *jobs.Runner)` (Concurrency 1,
   MaxAttempts 1, payload `{"language":"en"}`, result `{"code":""}`).
5. `worker.go`: `type Identity struct{ Protocol, Model, Revision, ModelSHA256, Language, Delimiter string; Vocab
   []string; Blank, FrameMs, SampleRate int; Versions map[string]string }` (JSON camelCase as read-along.md);
   `type Request struct{ ID, FileQuickHash string; OffsetMs int64; PCM []byte }`; `type Emissions struct{ Frames,
   Vocab, FrameMs int; LogProbs []float32 }`; `type Emitter interface{ Identity() Identity; Emit(context.Context,
   Request) (Emissions, error) }`; `Manager` = `New(cfg, *Components, *slog.Logger)`, `Emitter(ctx, lang)
   (Emitter, error)` (at most one process; another language stops the current one first), `Close() error`,
   `CPUTime() time.Duration` (sum of exited processes' user+system time).
   Launch: `exec.Command` (not `CommandContext`; lifetime is the manager's), args `[-I -B -u <script> --serve
   --model-dir <ModelsDir>/<lang> --language <lang> --threads N --device cpu]`; env exactly `LC_ALL=C`,
   `TENDSHELF_ALIGN_NONCE=<64 hex from crypto/rand>`, `OMP_NUM_THREADS=N`, `MKL_NUM_THREADS=N`, `HF_HUB_OFFLINE=1`,
   `TRANSFORMERS_OFFLINE=1`, `HF_HOME=<WorkDir>/hf-home`, `HOME=<WorkDir>/home`, `TMPDIR=<WorkDir>/tmp` (folders
   0700), then `cfg.Env`; `SysProcAttr` `Pdeathsig: SIGKILL, Setpgid: true` in `proc_linux.go` (other OS: none);
   `media.LowerPriority(pid)` after start; stderr into a 64 KiB ring, logged on exit.
   Ready: first stdout line within `ReadyTimeout`, ≤ 64 KiB: `{"status":"ready","nonce":…,"identity":{…}}`; nonce,
   protocol `tendshelf-emissions-v1`, `ModelSHA256` equal to the usable component's → else kill, `ErrIdentity`.
   Request: header line `{"op":"emissions","id","audioSha256","samples","fileQuickHash","offsetMs"}` then
   `samples*2` bytes s16le (samples ≤ 544 000, else `ErrRequestTooLarge` before writing). Response header ≤ 64 KiB
   `{"id","nonce","audioSha256","frames","vocabSize","frameMs","bytes"}` + `bytes` of float16 LE, or
   `{"id","nonce","error","detail"}`. Checks: id, nonce, hash, `vocabSize == len(Vocab)`, `frameMs == 20`,
   `|frames − (⌊(samples−400)/320⌋+1)| ≤ 2`, `bytes == frames*vocabSize*2 ≤ 4 MiB`, every value finite and ≤ 1e-3.
   One request at a time (mutex). Deadline `RequestTimeout` or ctx: on expiry kill the process. Errors:
   `ErrWorkerCrashed` (exit or EOF mid-request; uncertain), `ErrProtocol` (any check; definite), `ErrTimeout`,
   `ErrIdentity`. After any error the process is killed; the next `Emitter` call relaunches. Idle timer stops it
   (close stdin, wait 5 s, kill). `float16.go`: `halfToFloat(uint16) float32`.
6. `internal/align/runtime/fakeworker/fake.go`: `Main(mode string)` speaking the protocol; modes `ok`,
   `no-ready`, `noise` (junk line before ready), `bad-nonce`, `bad-identity`, `crash` (exit after reading a request),
   `hang`, `oversize`, `bad-frames`, `env` (puts its sorted environment in `identity.versions`), `slow-ok`.
   `ok` returns `-ln(32)` everywhere with the right frame count. `runtime_test.go` `TestMain`: when
   `TENDSHELF_FAKE_ALIGN_WORKER` is set, call `fakeworker.Main` and exit (the pattern of `media/media_test.go:19-22`);
   tests pass `Python: os.Args[0]` and `Env: ["TENDSHELF_FAKE_ALIGN_WORKER=<mode>"]`.
7. Docs: `docs/decisions/0017-alignment-worker-pipes.md` (context, decision, consequences, rejected loopback HTTP);
   a line "Amended by 0017 (pipes instead of loopback)" under 0001 point 3; `AGENTS.md` external-programs sentence
   adds "over its own stdin/stdout pipes, never a port"; `docs/architecture.md` line 107 "loopback emissions worker"
   → "pipe-connected emissions worker", idle stop and parent-death unchanged; `docs/read-along.md` Worker and
   lifetime paragraphs rewritten to the protocol above.

Tests: `TestRuntimeLocate` (table: missing, relative, not executable, world-writable dir), `TestRuntimeManifest`,
`TestRuntimeModelSHA`, `TestRuntimeCheckOK`/`TestRuntimeCheckMismatch`/`TestRuntimeCheckIdentity` (fake model files
written by the test; manifest built from them), `TestRuntimeUsable` (python path or worker sha changed → false),
`TestRuntimeReady`, `TestRuntimeReadyTimeout`, `TestRuntimeNoise`, `TestRuntimeNonce`, `TestRuntimeIdentity`,
`TestRuntimeEmitOK`, `TestRuntimeEmitOversize`, `TestRuntimeEmitBadFrames`, `TestRuntimeCrash` (then a relaunch
works), `TestRuntimeTimeout`, `TestRuntimeOneAtATime` (20 goroutines; fake reports max in flight 1),
`TestRuntimeIdleStop` (IdleStop 200 ms; pid gone), `TestRuntimeParentDeath` (Linux: the test binary in mode
`parent` launches a worker and prints its pid; the test kills the parent with SIGKILL; worker gone within 5 s),
`TestRuntimeEnvCleared` (only the allowlist plus the fake switch), `TestRuntimeFloat16` (table incl. -inf, subnormal),
`TestRuntimeImportsNoNet` (`go/parser` over the package's non-test files: no `net`, `net/http`).
Gates: `go test -race ./internal/align/runtime/... ./internal/media/...`, `CGO_ENABLED=0 go build ./...`, `make check`,
`make build`. Out of scope: GPU devices (`--device` stays `cpu`), Windows/macOS paths for the default Python, network
namespaces or seccomp around the worker.

## P4 `runtimes/align/` (package `alignfiles`)

Steps:
1. `runtimes/align/worker.py` derived from TendVocal `runtimes/alignment/worker.py` (header names it; NOTICES row).
   `PROTOCOL = "tendshelf-emissions-v1"`; argparse `--model-dir --language --threads --device --describe --serve`;
   first thing: `proto = os.fdopen(os.dup(1), "wb", buffering=0)`, `os.dup2(2, 1)` so library prints never reach
   the protocol; replace `socket.socket.connect`/`connect_ex`/`bind` with a raising function before importing torch;
   `torch.set_num_threads(threads)`; `Wav2Vec2ForCTC.from_pretrained(dir, local_files_only=True,
   use_safetensors=True)` and `Wav2Vec2FeatureExtractor.from_pretrained(dir, local_files_only=True)`; vocab from
   `vocab.json` ordered by id, `blank` = id of `<pad>`, delimiter `|`; identity `modelSha256` computed exactly like
   Go `ModelSHA` from the files on disk (hash checked against `--model-dir` contents at start). `--describe` prints
   one identity JSON line and exits; `--serve` pops `TENDSHELF_ALIGN_NONCE`, prints the ready line, then loops
   reading header + body from stdin until EOF; per request: s16le → float32 /32768, feature extractor, `log_softmax`
   on logits, `float16` little-endian bytes. Errors answer `{"error":"bad_request"|"internal"}`; never a traceback
   on the protocol stream (traceback to stderr). Torch and transformers import lazily inside the backend class.
2. `runtimes/align/models/en.json`, `es.json`: TendVocal `model-{en,es}.json` file lists and hashes, reshaped to the
   P2 `Manifest` (`format` `tendshelf-emissions-model`, `version` 1, `language`, `repository`, `revision`,
   `licenceSpdx` `Apache-2.0`, `licenceUrl` and `sourceUrl` `https://huggingface.co/<repo>/tree/<revision>`).
3. `runtimes/align/requirements-torch.lock`, `requirements-pypi.lock`, `pyproject.toml`: copied from TendVocal
   (`name = "tendshelf-align-runtime"`), header comment states Linux x86-64, CPU, Python 3.12.14.
4. `runtimes/align/bundle.go`: `//go:embed worker.py models/*.json *.lock pyproject.toml`; `WorkerPy() []byte`,
   `WorkerSHA256() string`, `ManifestJSON(lang string) ([]byte, bool)`, `Download(name string) ([]byte, string, bool)`
   for the allowlist `requirements-torch.lock`, `requirements-pypi.lock`, `pyproject.toml`, `models/en.json`,
   `models/es.json` (content type `text/plain; charset=utf-8`).
5. `runtimes/align/test_worker.py` (stdlib `unittest`, stub backend, no torch): framing, stdout isolation, EOF exit,
   oversize refusal, nonce echo. `Makefile`: `test-align-real` runs `TENDSHELF_TEST_ALIGN_RUNTIME=$(ALIGN_PYTHON)
   go test -count=1 -run 'TestRuntimeReal|TestAlignJobReal|TestWorkerBundlePython' ./internal/align/... ./runtimes/...`.
6. NOTICES rows: worker, manifests and locks (TendVocal, same author), both models (Apache-2.0, model cards).

Tests (Go, package `alignfiles`): `TestWorkerBundleManifests` (parse with a local struct, 64-hex hashes, safe names,
`model.safetensors` present), `TestWorkerBundleLocks` (every requirement line pinned `==` with `--hash=sha256:`),
`TestWorkerBundleProtocol` (worker.py contains the protocol string), `TestWorkerBundlePython` (opt-in: runs
`$TENDSHELF_TEST_ALIGN_RUNTIME -I runtimes/align/test_worker.py`). `internal/align/runtime/real_test.go`
`TestRuntimeReal` (opt-in, also needs `TENDSHELF_TEST_ALIGN_MODELS`): Check en, launch, Emit 34 s of
`testutil/wav` tone, frames 1699 ± 2, logs CPU seconds per audio second. P3b writes this test (it needs P2).
Gates: `go test ./runtimes/...`, `make check`, `make build`. Out of scope: a CUDA/ROCm lock, ARM locks, an installer.

## P3a `internal/align` core (pure Go, no IO but the cache)

Steps (all in package `align`; recipe constants in `params.go`: `WindowMs=30000`, `OverlapMs=1000`, `FrameMs=20`,
`QStep=0.1`, `BlockMs=600000`, `PauseFrames=15`, `MinHits=3`, `MaxRegions=4`, `SegMaxMs=30000`, `PadFrames=10`,
`RateMin=0.5`, `RateMax=6`, `NWMaxCells=4_000_000`, `TimedMin=0.30`, `High=0.75`, `Low=0.40`, `DoneMin=0.90`,
`PartialMin=0.05`, `AbridgedSpeech=0.85`, `AbridgedText=0.70`, `EditionNear=0.08`; `Params` struct with
`DefaultParams()`; tests may shrink `BlockMs` only):
1. `tokens.go`: `Vocab` (`NewVocab(symbols []string, blank int, delim string) (Vocab, error)`; upper-case mode when
   the vocab has no lower-case letters); `Text` built by `BuildText(doc *ebook.Doc, lang string, v Vocab) Text`:
   flat `Units []TUnit{Chapter, Block, Sentence, Word int; Norm string; Tokens []int; Wildcard, Front bool}` plus
   sentence table (SID via `ebook.SentenceID`); a unit with a letter outside the vocab becomes wildcard.
2. `cache.go`: `.em` = magic `TSEM\x00\x01`, `uint32 frames`, `uint16 vocab`, `uint16 frameMs`, `uint32 crc32` of
   the payload, payload `frames*vocab` bytes; `Quantise(logp float32) uint8 = clamp(round(-logp/0.1), 0, 255)`;
   `Cache{Root}`: `Path(fileHash, modelSHA, window)` = `<Root>/<fileHash>/<modelSHA>/<window:05d>.em`, `Has`, `Read`
   (bad magic/crc → delete, return missing), `Write` (temp, fsync, rename), `Touch(fileHash, modelSHA)`,
   `Prune(maxBytes int64, keep map[string]bool) (int64, error)` (oldest dir mtime first).
3. `track.go`: `Track{Files []TrackFile{QuickHash string; OffsetMs, DurationMs int64}}`; frames per file =
   `ceil(DurationMs/20)`; global frame index = concatenation; `TimeMs(g) = file.OffsetMs + local*20`;
   `Source` interface `Frames(from, to int64) (Matrix, error)` over cached windows (P3b implements on `Cache`;
   tests on memory).
4. `greedy.go`: `Greedy(src, track, v) ([]Heard, []Pause)`: argmax per frame, collapse repeats, blank/delim split
   words; `Heard{Norm string; StartF, EndF int64}`; pauses = runs of ≥ `PauseFrames` blank-or-delim frames.
5. `mapping.go`: blocks of `BlockMs` cut at the nearest pause; edition 5-grams of `Norm` unique in the edition;
   multi-region search (decision); per region LIS of hits → anchor pairs; between anchors word-level Needleman-Wunsch
   (exact, near = Levenshtein ≤ 1 per 4 letters), ≤ `NWMaxCells`, else 3-gram re-anchor unique in the gap, else gap
   unaligned. `Mapping{Pairs []Pair{Heard, Unit int; Exact bool}; Regions; SpeechMapped, NearShare float64}`.
6. `viterbi.go`: `ForceAlign(m Matrix, units [][]int, wild []bool, v Vocab) ([]Span, error)`: CTC trellis over
   2L+1 states with delimiter tokens between units, skip over blank between different tokens, wildcard = one star
   state (self-loop, emission = best non-blank non-delim); backtrack; `Span{StartF, EndF int64; Score float64}`,
   score = mean `exp(logp)` over frames assigned to the unit's token states. Too few frames → `ErrTooShort`.
7. `align.go`: `Align(in Input) (Result, error)` with `Input{Text; Vocab; Src Source; Track; ModelSHA string;
   Params; Reuse func(chapter int, key string) (*Timing, bool)}`: segments between anchor pairs merged while ≤
   `SegMaxMs`, audio `[heardStart(first) − PadFrames, heardStart(next anchor)]`; rate or length outside bounds →
   units `not-in-recording`; word timed when score ≥ `TimedMin`, wildcard gets neighbours' measured edges and mark
   `between` (never a sentence edge); monotonic post-pass; sentence confidence `0.5·mean timed score + 0.3·share
   timed + 0.2·share exact`; gaps `not-in-recording`, `not-found`, `unsupported`; chapter key = SHA-256 of
   `RecipeAlign\nModelSHA\n<sha of chapter Norms joined by " ">\n<comma list of quick hashes of mapped files>`;
   `Reuse` hit skips Viterbi for that chapter. `Result{Chapters []ChapterResult; Total, Timed int; Coverage float64;
   Labels []string; State string; ViterbiRuns int}`; state per the thresholds (`failed` with code `no_match`).
8. `timing.go`: `tendshelf-timing-v1` as read-along.md plus `"wm"` (per word 0 timed, 1 between, 2 not timed);
   `WriteTiming(dir, key, *Timing)` gzip, empty header, atomic; `ReadTiming`. read-along.md updated (payload, `wm`,
   multi-region, contiguity rule, transcript not stored).
9. `internal/align/aligntest/`: `Doc(chapters [][]string) *ebook.Doc` (sentences → blocks with correct UTF-16
   offsets), `Words(seed, n)` (seeded pseudo-words from a 600-word letters-only list), `Script` (spoken sentences
   with per-word start/end ms, pauses 200-800 ms, paragraph 1.2 s, rate 1.5-4.5 words/s), `Gen(script, vocab,
   noise float64, seed) (float matrix, truth)`: per word the token peaks spread evenly over its frames (peak
   `log 0.85`, else blank), `|` after each word, seeded Gaussian noise on logits before log-softmax; `Music(ms)` frames
   of random low peaks; `Intro(words)` speech not in the text.

Tests: `TestAlignCoreVocab`, `TestAlignCoreEmRoundTrip`, `TestAlignCoreEmCorrupt`, `TestAlignCorePrune`,
`TestAlignCoreGreedy`, `TestAlignCoreMapSwapped` (two regions), `TestAlignCoreNW` (cell bound), `TestAlignCoreViterbi`
(hand matrix, exact spans), `TestAlignCoreWildcard`, `TestAlignCoreQuantised` (uint8 vs float boundaries within 1
frame), `TestAlignCoreDeterministic` (same timing bytes twice), `TestAlignCoreReuse` (`Reuse` hit → `ViterbiRuns`
excludes it). Accuracy (6 chapters × ~400 words): `TestAlignAccuracyClean` ≥ 99 %, `…Intro`, `…FrontMatter`,
`…Swapped`, `…Abridged` (20 % paragraphs removed; label `abridged`), `…OtherEdition` (5 % substituted, inserted
sentences; label `different_edition`), `…Unsupported`, `…Music` (3 min) each ≥ 95 %; every case asserts zero timed
sentences absent from the script and zero out-of-order starts (`syn` helper `synCheck`). Shares are logged.
Gates: `go test -race ./internal/align/ ./internal/align/aligntest/`, `make check`, `make build`.
Out of scope: Media Overlays (R6), calibration of `High`/`Low` beyond the real run, GPU.

## P3b `align.book` job and service

Steps:
1. `internal/media/pcm.go`: `(t Tools) PCM16k(ctx, src string, fromMs, durMs int64, w io.Writer) error` through
   `ffmpegTo` (low priority) with args `ffmpegCommon + -ss <s.mmm> -i file:<src> -t <s.mmm> -map 0:a:0 -vn -sn -dn
   -ac 1 -ar 16000 -c:a pcm_s16le -f s16le pipe:1`; deadline 60 s + durMs/10; output cap `durMs*32 + 1 MiB`.
   Test `TestPCM16kArgs` in `internal/media` with the mediatest fake ffmpeg (argument list, cap, deadline).
2. `internal/align/service.go`: `KindBook = "align.book"`; `Deps{Store *db.Store; Library *library.Repo; Ebooks
   *ebook.Store; Runner *jobs.Runner; Runtimes Runtimes; Decoder Decoder; DataDir string; CacheMaxBytes int64;
   Log *slog.Logger}`; `Runtimes` interface `{Usable(ctx, lang) (alignrt.Component, bool, error); Emitter(ctx, lang)
   (alignrt.Emitter, error)}`; `Decoder` interface `{PCM16k(...)}`. `Register(r)`: Concurrency 1, MaxAttempts 3,
   HoldOnRecovery false. `Start(ctx, editionID, bookID, userID) (Alignment, error)`: active row → return it; edition
   ready and paired to the book else `ErrEditionNotReady`; language prefix (`en-GB` → `en`) with a usable component
   else `ErrRuntimeMissing`; audio files all present, probed, not encrypted else `ErrAudioUnavailable`;
   `audio_key` = SHA-256 of `\n`-joined quick hashes in `Idx` order; newest done/partial with the same doc, audio,
   model and recipe → return it; else insert `queued` and enqueue `{"alignmentId"}` in one flow (row then job id).
   `Cancel`/`Discard(ctx, id)`: `runner.Cancel` then `state='cancelled'`; `Resume(ctx, id)`: only `held` →
   `runner.Resume`; `Status(ctx, editionID) (Status{Alignment; Job *jobs.Job; Chapters; Stale bool}, bool, error)`
   (`Stale` when the current audio key differs); `Reconcile(ctx)`: active rows whose job is `failed` → `failed`
   with the job's code, `cancelled` or missing → `cancelled`, `queued` → `queued`.
3. `internal/align/job.go` handler: load row; finished states → nil. Row `held` → `Adopt` current audio key and
   model. Else audio key or model differ → hold (`audio_changed`, `runtime_changed`). `SetState emitting`; per file,
   windows `0..ceil(DurationMs/30000)-1`; for each run of missing windows one `PCM16k` stream from `max(0,
   k*30000−1000)`; per window request `[k*30000−1000, (k+1)*30000+1000]` clipped, keep frames for `[k*30000,
   min((k+1)*30000, dur))`, quantise, `Cache.Write`; an Emit error retries twice (fresh `Emitter`), the third →
   hold `worker_failed`; emitter identity model sha ≠ row → hold `runtime_changed`. End of stage: recompute quick
   hashes (`media` quick hash) → differ → hold `audio_changed`. `SetState aligning`; `ReadDoc`; `align.Align` with
   `Reuse` = `Store.ChapterByKey` + timing file exists; each new chapter: `WriteTiming` then `PutChapter`; `Finish`;
   `Cache.Prune(CacheMaxBytes, keep current files)`. Progress `{"stage","chaptersDone","chaptersTotal",
   "audioDoneMs","audioTotalMs","leftMs"}`; `leftMs` only after 10 windows measured in this run. Holds return
   `&jobs.Error{Code, Text, Hold: true}` after `SetState held`. `no_match` → permanent `jobs.Error`.
4. Opt-in `internal/align/real_test.go` `TestAlignJobReal` (see "Real run") and `internal/align/runtime/real_test.go`
   `TestRuntimeReal` (P4 tests paragraph).

Tests (fake `Emitter` slices an `aligntest.Gen` matrix by `FileQuickHash`/`OffsetMs`; fake `Decoder` writes zero
samples of the right length; real `jobs.Runner` on a temp store): `TestAlignJobRuns` (done, coverage, chapter rows,
timing files), `TestAlignJobKindNoHold` (`HoldOnRecovery` false), `TestAlignJobResumeEmitting` and
`TestAlignJobResumeAligning` (a hook cancels with `ErrShutdown` after window n / chapter n; restart; total Emit
calls == windows, Viterbi runs == chapters), `TestAlignJobLeaseRecovery` (expired lease → queued → resumes),
`TestAlignJobCancel` (row `cancelled`, later handler writes refused), `TestAlignJobWorkerFailedHolds` (3 failures →
held, attempts unchanged; Resume completes), `TestAlignJobRuntimeChanged`, `TestAlignJobAudioChanged`,
`TestAlignJobRechapter` (new doc with one chapter edited: 0 Emit calls, 1 Viterbi chapter), `TestAlignJobStart*`
(runtime missing, audio unavailable, active dedup, finished reuse), `TestAlignJobReconcile`, `TestAlignJobNoMatch`.
Gates: `go test -race ./internal/align/... ./internal/media/... ./internal/jobs/...`, `make check`, `make build`.
Out of scope: deleting stale timing files, backup changes (check with `grep -n cache internal/backups/*.go` that
backups copy only the database; guess: they do), Spanish real run.

## P5 `/admin/runtimes`, Text tab, wiring

Steps:
1. `cmd/tendshelf`: flags of decision 3; `alignrt.New` with `Script: alignfiles.WorkerPy()`, manifests parsed from
   `alignfiles.ManifestJSON`; register `align.check` and `align.book`; `Reconcile` after start; `Manager.Close` on
   shutdown; `web.Options{Align, Runtimes}`. Missing Python only logs one Info line.
2. `internal/web/routes_runtimes.go` (gate `share.ManageServer` on `TargetServer`, like `emailGate`):
   `GET /admin/runtimes` (one card per language: Ready / Not set up / Needs a check / Problem in plain words; model,
   revision, size, licence link, official model page link, Python path, threads, CPU estimate from read-along.md;
   install steps in a disclosure); `POST /admin/runtimes/check` (`language` = `en`|`es`; else 422) → enqueue,
   re-render with a pending line polling `/hx/jobs/{id}` every 2s; `GET /admin/runtimes/files/{name...}` (allowlist,
   else 404). `pages/admin_runtimes.templ`, `runtimes_copy.go` (all strings NEW, listed in the report). Link it where
   `/admin/email` is linked. `routes_jobs.go` `jobViews`: `alignrt.KindCheck` (ManageServer, else 404) and
   `align.KindBook` (EditBook on the alignment's book via `ByJob`, else 404).
3. Text tab (`routes_ebooks_pair.go`, `pages/book_text.templ`, `ebook_copy.go`): `ebookPairGo` becomes the deck's
   "Pair and prepare read-along" when a runtime for the edition's language is usable (pairs, then `Start`), else
   stays "Pair with this audiobook". Paired ready edition: "Prepare read-along" `POST /books/{id}/text/{fileID}/align`;
   active: "Preparing read-along" with stage and chapters line, polling `/hx/jobs/{jobID}`, `Cancel` `POST
   …/align/cancel`; held: reason line plus `Resume` (`…/align/resume`) and `Discard` (`…/align/discard`); done:
   "Read-along is ready for {title}"; partial: "Read-along works for {n} % of the book."; labels and `no_match`,
   `runtime_missing`, `audio_changed` ("Prepare read-along again") lines from read-along.md's failure table. Gate
   EditBook; file not paired to that book → 404; `ErrRuntimeMissing`/`ErrAudioUnavailable` → 409 with an OOB toast
   for htmx, the full page with the line otherwise; `ErrNotHeld`/finished → 409 toast "This has already finished."
   Styles: `pages-text.css` and the admin page CSS file only; core CSS untouched (budget over, operator pending).
4. `docs/architecture.md` routes 241-246 rewritten to the real routes; roadmap R3 status.

Tests: `TestAdminRuntimesForbidden` (each role without ManageServer → 404 for all three routes), `TestAdminRuntimesPage`
(not set up / ready / needs check from seeded rows), `TestAdminRuntimesCheck` (job queued, fragment polls, 422 bad
language), `TestAdminRuntimesFiles` (allowlist, `../` → 404), `TestTextAlignStart` (EditBook 303/200; reader 404;
other book's file 404), `TestTextAlignRuntimeMissing` (409 toast), `TestTextAlignPairAndPrepare`,
`TestTextAlignCancelResumeDiscard`, `TestTextAlignJobFragment` (polls while running, stops when done; other user 404),
matrix rows for every new route, `a11y_pages_test.go` seed `admin-runtimes` (0 findings), opt-in
`TestTextAlignVisual` against board `screens/reader/PreparingPhone` at 390, 768, 1024, 1440.
Gates: `make generate` (clean tree), `go test -race ./internal/web/... ./cmd/...`, `make check`, `make build`.
Out of scope: `/about` credits page (not in the R3 roadmap scope; licences show on `/admin/runtimes`), the reader
(R4), one-click install, timing-file serving routes (R4).

## Operator install steps (hand to the operator after P5 merges)

1. `D=~/.local/share/tendshelf/runtimes/align; mkdir -p $D/models/en $D/models/es`
2. `uv venv --python 3.12.14 $D/venv` (uv already has 3.12.14 on this machine).
3. Save the two lock files from `/admin/runtimes` (or `runtimes/align/` in the repo), then
   `uv pip install --python $D/venv/bin/python --require-hashes --no-deps --index-url https://download.pytorch.org/whl/cpu -r requirements-torch.lock`
   and `uv pip install --python $D/venv/bin/python --require-hashes --no-deps -r requirements-pypi.lock`.
4. Models, from the official pages at the pinned revision (every file listed in `models/en.json`):
   `curl -fL -o $D/models/en/<file> https://huggingface.co/facebook/wav2vec2-base-960h/resolve/22aad52d435eb6dbaf354bdad9b0da84ce7d6156/<file>`;
   Spanish likewise from `jonatasgrosman/wav2vec2-large-xlsr-53-spanish/resolve/80106547eee5065e01618d6588c22c256e8d0e70/`.
   Shortcut on this machine: copy `~/Projects/TendVocal/.local/alignment-smoke/{en,es}/*` (same revisions; the
   Check verifies every hash anyway).
5. Open `/admin/runtimes`, press Check for each language; the card must say Ready.

## Real run (opt-in, operator fetches by hand)

Sample: LibriVox "Alice's Adventures in Wonderland", chapter 1, any solo reading (librivox.org, MP3 64 kbps), and
Project Gutenberg eBook #11 (plain text UTF-8), cut to Chapter I. Put `audio.mp3`, `text.txt` and `truth.tsv`
(`start_ms<TAB>first five words` for every sentence of the first 10 minutes, checked by ear) in
`.local/tests/align-accuracy/sample/`. Run `make test-align-real ALIGN_PYTHON=$D/venv/bin/python` with
`TENDSHELF_TEST_ALIGN_MODELS=$D/models TENDSHELF_TEST_ALIGN_SAMPLE=.local/tests/align-accuracy/sample`.
`TestAlignJobReal` builds the doc with `ebook.Read`, runs the job and writes
`.local/tests/align-accuracy/<run-id>/report.json` (share within ±250 ms, CPU seconds per audio hour from
`Manager.CPUTime`, timing and cache bytes). The orchestrator records the three numbers in the roadmap and replaces
the guesses in read-along.md, then removes the run folder. Target (guess): ≥ 95 %.

## Operator decisions (defaults apply unless changed)

1. Transport: stdin/stdout pipes (default) or loopback HTTP with an `http.Client` exception in `internal/align/runtime`.
2. Worker script embedded in the binary (default) or installed by the owner beside the venv.
3. Discovery and CPU: `<data>/runtimes/align/{venv,models}`, threads `min(4, CPUs/2)`, nice 10, one job at a time,
   2 GiB emissions cache (default).
4. Pins: TendVocal's Python 3.12.14 + torch 2.8.0 CPU set and the two model revisions above (default).
