-- 0014_uploads: adding books by upload (decision 0015). libraries.upload_folder_id names the one
-- folder of the library that receives uploads (NULL = uploads off). uploads and upload_files are the
-- sessions and the resume offsets. upload_paths is the ledger of every path an upload created in the
-- library folder: a row is written before the path is created, and "Remove this book" deletes only
-- from it. upload_paths has no foreign keys on purpose, like file_writes: it must outlive the upload
-- row and the book.
ALTER TABLE libraries ADD COLUMN upload_folder_id TEXT REFERENCES library_folders(id) ON DELETE SET NULL;

CREATE TABLE uploads (
  id             TEXT PRIMARY KEY,
  library_id     TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_id      TEXT NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE,
  created_by     TEXT NOT NULL,
  label          TEXT NOT NULL,
  state          TEXT NOT NULL CHECK (state IN ('receiving','queued','placing','placed','refused','failed','cancelled','expired')),
  bytes_total    INTEGER NOT NULL CHECK (bytes_total >= 0),
  bytes_received INTEGER NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
  job_id         TEXT,
  scan_job_id    TEXT,
  book_path      TEXT,
  temp_path      TEXT,
  refused_file   TEXT,
  error_code     TEXT,
  skipped        INTEGER NOT NULL DEFAULT 0 CHECK (skipped >= 0),
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
) STRICT;

CREATE INDEX uploads_person ON uploads(created_by, state);

CREATE TABLE upload_files (
  id             TEXT PRIMARY KEY,
  upload_id      TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
  idx            INTEGER NOT NULL,
  rel_path       TEXT NOT NULL,
  size           INTEGER NOT NULL CHECK (size >= 0),
  modified_ms    INTEGER NOT NULL DEFAULT 0,
  sha256         TEXT, -- optional: the hash the client promised for the whole file
  bytes_received INTEGER NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
  UNIQUE (upload_id, rel_path)
) STRICT;

CREATE TABLE upload_paths (
  id                TEXT PRIMARY KEY,
  upload_id         TEXT NOT NULL,
  library_folder_id TEXT NOT NULL,
  path              TEXT NOT NULL,
  kind              TEXT NOT NULL CHECK (kind IN ('dir','file')),
  state             TEXT NOT NULL CHECK (state IN ('planned','staged','present','removed','kept')),
  size              INTEGER,
  mtime_ns          INTEGER,
  quick_hash        TEXT,
  sha256            TEXT,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
) STRICT;

CREATE INDEX upload_paths_upload ON upload_paths(upload_id);
CREATE UNIQUE INDEX upload_paths_live ON upload_paths(path) WHERE state IN ('planned','staged','present');
