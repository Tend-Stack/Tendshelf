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
