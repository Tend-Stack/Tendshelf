-- 0019_highlights: read-along R4 (docs/read-along.md §3). A highlight is a text range plus the time range taken from the
-- alignment when it was made; its note is the note column (a note on a moment stays bookmarks.note). Bookmarks made in the
-- text also record the edition and sentence. edition_id is SET NULL so a forgotten edition keeps the person's marks (quote kept).
CREATE TABLE highlights (
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
  start_ms     INTEGER CHECK (start_ms IS NULL OR start_ms >= 0),
  end_ms       INTEGER,
  alignment_id TEXT REFERENCES alignments(id) ON DELETE SET NULL,
  colour       TEXT NOT NULL DEFAULT 'yellow' CHECK (colour IN ('yellow','green','blue','pink')),
  note         TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 2000),
  client_id    TEXT CHECK (client_id IS NULL OR length(client_id) BETWEEN 1 AND 64),
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL,
  deleted_at   INTEGER,
  CHECK ((start_ms IS NULL) = (end_ms IS NULL)),
  CHECK (end_ms IS NULL OR end_ms >= start_ms)
) STRICT;
CREATE INDEX highlights_live ON highlights(user_id, book_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX highlights_chapter ON highlights(user_id, edition_id, chapter_idx) WHERE deleted_at IS NULL;
CREATE INDEX highlights_sync ON highlights(user_id, updated_at);
CREATE INDEX highlights_book ON highlights(book_id);
CREATE INDEX highlights_edition ON highlights(edition_id) WHERE edition_id IS NOT NULL;
CREATE UNIQUE INDEX highlights_client ON highlights(user_id, client_id) WHERE client_id IS NOT NULL;
ALTER TABLE bookmarks ADD COLUMN edition_id TEXT REFERENCES editions(id) ON DELETE SET NULL;
ALTER TABLE bookmarks ADD COLUMN sid TEXT CHECK (sid IS NULL OR length(sid) BETWEEN 6 AND 40);
