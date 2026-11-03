-- 0011_file_writes: the ledger of "write details into the file" (M4-7). One row per audio file and
-- write. There are deliberately no foreign keys: a row must outlive a pruned job and a deleted book,
-- because it is the only record of a hidden backup file that sits next to the person's audio.
CREATE TABLE file_writes (
  id            TEXT PRIMARY KEY,
  job_id        TEXT NOT NULL,
  book_id       TEXT NOT NULL,
  audio_file_id TEXT NOT NULL,
  state         TEXT NOT NULL CHECK (state IN ('copying','verifying','swapping','swapped','confirming','confirmed','undoing','undone','failed')),
  original_path TEXT NOT NULL,
  backup_path   TEXT NOT NULL,
  temp_path     TEXT NOT NULL,
  before_sha256 TEXT NOT NULL,
  after_sha256  TEXT,
  audio_hash    TEXT NOT NULL,
  error_code    TEXT,
  created_by    TEXT NOT NULL,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT;

-- At most one open row per file.
CREATE UNIQUE INDEX file_writes_open ON file_writes(original_path) WHERE state NOT IN ('confirmed','undone','failed');
CREATE INDEX file_writes_book ON file_writes(book_id, state);
