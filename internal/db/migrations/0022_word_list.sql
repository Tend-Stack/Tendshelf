-- 0022_word_list: words a person saved while reading. One row per person, book and word; edition SET NULL keeps the word.
CREATE TABLE word_list (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id    TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL,
  word       TEXT NOT NULL CHECK (length(word) BETWEEN 1 AND 64),
  folded     TEXT NOT NULL CHECK (length(folded) BETWEEN 1 AND 128),
  quote      TEXT NOT NULL DEFAULT '' CHECK (length(quote) <= 400),
  sid        TEXT CHECK (sid IS NULL OR length(sid) BETWEEN 6 AND 40),
  off        INTEGER NOT NULL DEFAULT 0 CHECK (off >= 0),
  created_at INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX word_list_person ON word_list(user_id, book_id, folded);
CREATE INDEX word_list_recent ON word_list(user_id, created_at);
CREATE INDEX word_list_book ON word_list(book_id);
CREATE INDEX word_list_edition ON word_list(edition_id) WHERE edition_id IS NOT NULL;
