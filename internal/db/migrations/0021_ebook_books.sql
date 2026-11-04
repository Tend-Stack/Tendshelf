-- 0021_ebook_books: an ebook with no audiobook is a book (decision 0018). kind names who owns the row: audio = the scanner,
-- text = the ebook service. reading_positions is one text place per person and book; reading_sessions measures speed.
ALTER TABLE books ADD COLUMN kind TEXT NOT NULL DEFAULT 'audio' CHECK (kind IN ('audio','text'));
CREATE INDEX books_kind ON books(library_id, kind);
-- What the file says beyond editions' columns: description, publisher, year, series, series_index.
ALTER TABLE editions ADD COLUMN meta_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(meta_json) AND json_type(meta_json) = 'object');
-- The text book this file came from when it was merged; a split restores that id, so old links work again.
ALTER TABLE ebook_files ADD COLUMN merged_from TEXT;
CREATE INDEX ebook_files_merged ON ebook_files(merged_from) WHERE merged_from IS NOT NULL;
-- A merged-away id still answers: pages redirect, a reader's position save lands on the book that took over.
CREATE TABLE merged_books (
  from_id   TEXT PRIMARY KEY,
  to_id     TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  merged_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
CREATE INDEX merged_books_to ON merged_books(to_id);
CREATE TABLE reading_positions (
  user_id           TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id           TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  edition_id        TEXT REFERENCES editions(id) ON DELETE SET NULL,
  chapter_idx       INTEGER NOT NULL CHECK (chapter_idx >= 1),
  sid               TEXT NOT NULL DEFAULT '' CHECK (sid = '' OR length(sid) BETWEEN 6 AND 40),
  off               INTEGER NOT NULL DEFAULT 0 CHECK (off >= 0),
  quote             TEXT NOT NULL DEFAULT '' CHECK (length(quote) <= 64),
  char_pos          INTEGER NOT NULL CHECK (char_pos >= 0),
  percent_bp        INTEGER NOT NULL CHECK (percent_bp BETWEEN 0 AND 10000),
  finished_at       INTEGER CHECK (finished_at IS NULL OR finished_at > 0),
  client_updated_at INTEGER NOT NULL CHECK (client_updated_at > 0),
  server_updated_at INTEGER NOT NULL,
  device_id         TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  device_label      TEXT NOT NULL DEFAULT '' CHECK (length(device_label) <= 64),
  PRIMARY KEY (user_id, book_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX reading_positions_recent ON reading_positions(user_id, client_updated_at);
CREATE INDEX reading_positions_book ON reading_positions(book_id);
CREATE INDEX reading_positions_edition ON reading_positions(edition_id) WHERE edition_id IS NOT NULL;
CREATE TABLE reading_sessions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  device_id  TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  started_at INTEGER NOT NULL,
  ended_at   INTEGER NOT NULL CHECK (ended_at >= started_at),
  chars_read INTEGER NOT NULL CHECK (chars_read >= 0),
  active_ms  INTEGER NOT NULL CHECK (active_ms >= 0)
) STRICT;
CREATE INDEX reading_sessions_current ON reading_sessions(user_id, book_id, device_id, ended_at);
CREATE INDEX reading_sessions_user ON reading_sessions(user_id, started_at);
CREATE INDEX reading_sessions_book ON reading_sessions(book_id);
