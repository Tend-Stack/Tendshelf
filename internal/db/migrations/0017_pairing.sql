-- 0017_pairing: read-along R2 (docs/read-along.md §1 Pairing). hold: NULL = automatic pairing may pair the file;
-- 'person' = a person unpaired it; 'kept' = a person kept it as an ebook on its own. Automatic pairing touches only
-- hold IS NULL. book_default marks the book's edition (one per book). ebook_not_same records "Not the same book" for
-- one (file, book). text_choices is each person's edition of a book. An ASIN match is stored as paired_by 'isbn'.
ALTER TABLE ebook_files ADD COLUMN hold TEXT CHECK (hold IS NULL OR hold IN ('person','kept'));
ALTER TABLE ebook_files ADD COLUMN book_default INTEGER NOT NULL DEFAULT 0 CHECK (book_default IN (0,1));
CREATE UNIQUE INDEX ebook_files_book_default ON ebook_files(book_id) WHERE book_default = 1 AND book_id IS NOT NULL;
CREATE INDEX ebook_files_unpaired ON ebook_files(created_at, id) WHERE book_id IS NULL;

CREATE TABLE ebook_not_same (
  ebook_file_id TEXT NOT NULL REFERENCES ebook_files(id) ON DELETE CASCADE,
  book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  user_id       TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (ebook_file_id, book_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX ebook_not_same_book ON ebook_not_same(book_id);

CREATE TABLE text_choices (
  user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id       TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  ebook_file_id TEXT NOT NULL REFERENCES ebook_files(id) ON DELETE CASCADE,
  updated_at    INTEGER NOT NULL,
  PRIMARY KEY (user_id, book_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX text_choices_file ON text_choices(ebook_file_id);
