-- 0005_shelves: personal shelves (sharing a shelf arrives with P3).
CREATE TABLE shelves (id TEXT PRIMARY KEY, owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80), shared INTEGER NOT NULL DEFAULT 0 CHECK (shared IN (0,1)),
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL) STRICT;
CREATE UNIQUE INDEX shelves_owner_name ON shelves(owner_id, name COLLATE NOCASE);
CREATE TABLE shelf_items (shelf_id TEXT NOT NULL REFERENCES shelves(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, idx INTEGER NOT NULL CHECK (idx >= 0),
  added_at INTEGER NOT NULL, PRIMARY KEY (shelf_id, book_id)) STRICT, WITHOUT ROWID;
CREATE INDEX shelf_items_order ON shelf_items(shelf_id, idx, book_id);
CREATE INDEX shelf_items_book ON shelf_items(book_id);
