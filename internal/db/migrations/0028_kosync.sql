-- kosync (S6): KOReader progress sync. An ebook file gets the digest KOReader names a document by
-- (partial MD5 over samples of the bytes), and each person keeps the last place a KOReader sent for each
-- document, so KOReader gets back its own exact pointer. Nothing existing is rebuilt.
ALTER TABLE ebook_files ADD COLUMN partial_md5 TEXT CHECK (partial_md5 IS NULL OR length(partial_md5) = 32);
CREATE INDEX ebook_files_partial_md5 ON ebook_files(partial_md5) WHERE partial_md5 IS NOT NULL;
CREATE TABLE kosync_progress (
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  document   TEXT NOT NULL CHECK (length(document) = 32),
  progress   TEXT NOT NULL CHECK (length(progress) <= 512),
  percentage REAL NOT NULL CHECK (percentage >= 0 AND percentage <= 1),
  device     TEXT NOT NULL CHECK (length(device) <= 64),
  device_id  TEXT NOT NULL CHECK (length(device_id) <= 64),
  updated_at INTEGER NOT NULL CHECK (updated_at > 0),
  PRIMARY KEY (user_id, document)) STRICT, WITHOUT ROWID;
CREATE INDEX kosync_progress_recent ON kosync_progress(user_id, updated_at);
