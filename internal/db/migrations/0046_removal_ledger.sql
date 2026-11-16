-- 0046_removal_ledger: what a removal took from the upload ledger and from who-added-it, so Put back returns it (decision 0045 addendum).
ALTER TABLE removals ADD COLUMN source TEXT NOT NULL DEFAULT 'library' CHECK (source IN ('library','upload'));
CREATE TABLE removal_ledger (
  removal_id     TEXT NOT NULL REFERENCES removals(id) ON DELETE CASCADE,
  upload_path_id TEXT NOT NULL,
  PRIMARY KEY (removal_id, upload_path_id)
) STRICT, WITHOUT ROWID;
CREATE TABLE removal_upload_books (
  removal_id TEXT NOT NULL REFERENCES removals(id) ON DELETE CASCADE,
  root_path  TEXT NOT NULL,
  upload_id  TEXT NOT NULL,
  library_id TEXT NOT NULL,
  added_by   TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (removal_id, root_path)
) STRICT, WITHOUT ROWID;
