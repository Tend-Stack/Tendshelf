-- 0045_removals: "Remove this book and its files" for books put in the library folder by hand (decision 0045).
-- removals is one removal of one book; removal_paths is every file (and folder) it moves, recorded before anything moves.
-- uploads gains the columns the "already in the library" check fills in.
ALTER TABLE uploads ADD COLUMN dup_book_id TEXT;
ALTER TABLE uploads ADD COLUMN dup_tier TEXT CHECK (dup_tier IS NULL OR dup_tier IN ('same_files','some_files','same_details'));
ALTER TABLE uploads ADD COLUMN add_anyway INTEGER NOT NULL DEFAULT 0 CHECK (add_anyway IN (0,1));

CREATE TABLE removals (
  id         TEXT PRIMARY KEY,
  library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_id  TEXT NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE,
  book_id    TEXT NOT NULL,
  title      TEXT NOT NULL,
  root_path  TEXT NOT NULL,
  mode       TEXT NOT NULL CHECK (mode IN ('files','keep_files')),
  state      TEXT NOT NULL CHECK (state IN ('planned','moving','held','restored','purged','failed')),
  error_code TEXT,
  plan_hash  TEXT NOT NULL CHECK (length(plan_hash) = 64),
  files      INTEGER NOT NULL DEFAULT 0,
  bytes      INTEGER NOT NULL DEFAULT 0,
  job_id     TEXT,
  removed_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  purge_after INTEGER
) STRICT;
CREATE UNIQUE INDEX removals_active_book ON removals(book_id) WHERE state IN ('planned','moving');
CREATE INDEX removals_library ON removals(library_id, mode, state);

CREATE TABLE removal_paths (
  removal_id TEXT NOT NULL REFERENCES removals(id) ON DELETE CASCADE,
  idx        INTEGER NOT NULL,
  kind       TEXT NOT NULL CHECK (kind IN ('file','extra','dir')),
  path       TEXT NOT NULL,
  held_path  TEXT,
  size       INTEGER NOT NULL DEFAULT 0,
  mtime_ns   INTEGER NOT NULL DEFAULT 0,
  quick_hash TEXT,
  state      TEXT NOT NULL CHECK (state IN ('planned','held','kept','restored','purged')),
  PRIMARY KEY (removal_id, idx)
) STRICT, WITHOUT ROWID;
