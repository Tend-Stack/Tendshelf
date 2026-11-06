-- 0024_book_settings: a person's own settings for one book, the speed first. A book with no row
-- uses the person's usual speed (user_settings playback.speed). One row per (person, book).
CREATE TABLE book_settings (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  speed REAL NOT NULL CHECK (speed >= 0.5 AND speed <= 3.0),
  updated_at INTEGER NOT NULL CHECK (updated_at > 0),
  PRIMARY KEY (user_id, book_id)) STRICT, WITHOUT ROWID;
CREATE INDEX book_settings_book ON book_settings(book_id);
