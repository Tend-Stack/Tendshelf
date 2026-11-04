-- 0020_drop_alignment: read-along is dropped (decision 0018). Highlights keep their text range; the audio times go.
-- The foreign key to alignments forces a table rebuild; it must happen before alignments is dropped.
CREATE TABLE highlights_new (
  id           TEXT PRIMARY KEY,
  user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id      TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  edition_id   TEXT REFERENCES editions(id) ON DELETE SET NULL,
  chapter_idx  INTEGER NOT NULL CHECK (chapter_idx >= 1),
  start_sid    TEXT NOT NULL CHECK (length(start_sid) BETWEEN 6 AND 40),
  start_off    INTEGER NOT NULL CHECK (start_off >= 0),
  end_sid      TEXT NOT NULL CHECK (length(end_sid) BETWEEN 6 AND 40),
  end_off      INTEGER NOT NULL CHECK (end_off >= 0),
  quote        TEXT NOT NULL CHECK (length(quote) <= 4000),
  quote_prefix TEXT NOT NULL DEFAULT '' CHECK (length(quote_prefix) <= 32),
  quote_suffix TEXT NOT NULL DEFAULT '' CHECK (length(quote_suffix) <= 32),
  colour       TEXT NOT NULL DEFAULT 'yellow' CHECK (colour IN ('yellow','green','blue','pink')),
  note         TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 2000),
  client_id    TEXT CHECK (client_id IS NULL OR length(client_id) BETWEEN 1 AND 64),
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  deleted_at   INTEGER
) STRICT;
INSERT INTO highlights_new (id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote,
  quote_prefix, quote_suffix, colour, note, client_id, created_at, updated_at, deleted_at)
SELECT id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote,
  quote_prefix, quote_suffix, colour, note, client_id, created_at, updated_at, deleted_at FROM highlights;
DROP TABLE highlights;
ALTER TABLE highlights_new RENAME TO highlights;
CREATE INDEX highlights_live ON highlights(user_id, book_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX highlights_chapter ON highlights(user_id, edition_id, chapter_idx) WHERE deleted_at IS NULL;
CREATE INDEX highlights_sync ON highlights(user_id, updated_at);
CREATE INDEX highlights_book ON highlights(book_id);
CREATE INDEX highlights_edition ON highlights(edition_id) WHERE edition_id IS NOT NULL;
CREATE UNIQUE INDEX highlights_client ON highlights(user_id, client_id) WHERE client_id IS NOT NULL;
DROP TABLE alignment_chapters;
DROP TABLE alignments;
DROP TABLE runtime_components;
-- A job kind with no handler is never claimed, and a running one would be held forever. These jobs have no children.
DELETE FROM jobs WHERE kind IN ('align.book','align.check');
