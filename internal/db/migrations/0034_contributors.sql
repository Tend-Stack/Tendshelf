-- 0034_contributors: people who add books of their own (decision 0029).
-- A contributor is a member with users.adds_books = 1; the CHECK refuses the flag on any other role,
-- so no role value is added and users is not rebuilt.
ALTER TABLE users ADD COLUMN adds_books INTEGER NOT NULL DEFAULT 0 CHECK (adds_books IN (0,1) AND (adds_books = 0 OR role = 'member'));
ALTER TABLE invites ADD COLUMN adds_books INTEGER NOT NULL DEFAULT 0 CHECK (adds_books IN (0,1) AND (adds_books = 0 OR role = 'member'));

-- The folder chosen once for a person inside one upload folder ("Added by <name>").
CREATE TABLE contributor_folders (
  folder_id  TEXT NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  path       TEXT NOT NULL UNIQUE,
  created_at INTEGER NOT NULL,
  PRIMARY KEY (folder_id, user_id)
) STRICT, WITHOUT ROWID;

-- Who added a book: one row per published upload. No foreign keys, like upload_paths, so the row survives
-- the sweep of placed uploads, a rescan, a role change, a disabled account and a library reset.
CREATE TABLE upload_books (
  root_path  TEXT PRIMARY KEY,
  upload_id  TEXT NOT NULL,
  library_id TEXT NOT NULL,
  added_by   TEXT NOT NULL,
  created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX upload_books_by ON upload_books(added_by, root_path);

INSERT OR IGNORE INTO upload_books (root_path, upload_id, library_id, added_by, created_at)
  SELECT book_path, id, library_id, created_by, updated_at FROM uploads
  WHERE state = 'placed' AND book_path IS NOT NULL ORDER BY updated_at DESC;
