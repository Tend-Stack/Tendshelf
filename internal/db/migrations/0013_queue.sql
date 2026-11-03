-- 0013_queue: Up next, a person's ordered list of books to play after the current one.
-- Positions are signed so "Play next" can take min(position) - 1 without renumbering.
CREATE TABLE queue_items (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  position INTEGER NOT NULL, added_at INTEGER NOT NULL CHECK (added_at > 0),
  PRIMARY KEY (user_id, book_id)) STRICT, WITHOUT ROWID;
CREATE INDEX queue_items_order ON queue_items(user_id, position, book_id);
CREATE INDEX queue_items_book ON queue_items(book_id);
