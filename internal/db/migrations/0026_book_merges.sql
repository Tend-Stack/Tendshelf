-- 0026_book_merges: merging two copies of one audiobook (S4). merged_folders remembers the folder of the copy that was
-- merged away, so a scan never brings it back while the merge stands; deleting the kept book frees the folder again
-- (the safe direction). book_not_duplicates is "Not the same" on the Duplicates page, one row per pair (a_id < b_id).
CREATE TABLE merged_folders (root_path TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  from_id TEXT NOT NULL, to_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  merged_by TEXT REFERENCES users(id) ON DELETE SET NULL, merged_at INTEGER NOT NULL CHECK (merged_at > 0)) STRICT, WITHOUT ROWID;
CREATE INDEX merged_folders_to ON merged_folders(to_id);
CREATE INDEX merged_folders_library ON merged_folders(library_id);
CREATE TABLE book_not_duplicates (a_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, b_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL, CHECK (a_id < b_id), PRIMARY KEY (a_id, b_id)) STRICT, WITHOUT ROWID;
CREATE INDEX book_not_duplicates_b ON book_not_duplicates(b_id);
