-- 0016_ebooks: read-along R1 (docs/read-along.md §1). ebook_files is every ebook file Tendshelf knows:
-- found in a library folder (origin 'library', read-only, path set) or uploaded on a book's Text page
-- (origin 'upload', bytes at <data>/ebooks/<sha256>.<ext>, path NULL). book_id is the pairing (NULL =
-- unpaired, waits for the R2 inbox). editions is one canonical document read from one file's exact
-- bytes by one recipe; the document is <data>/text/<doc_sha256>.json.gz. edition_chapters lists chapters.
CREATE TABLE ebook_files (
  id             TEXT PRIMARY KEY,
  origin         TEXT NOT NULL CHECK (origin IN ('library','upload')),
  library_id     TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_id      TEXT REFERENCES library_folders(id) ON DELETE CASCADE,
  path           TEXT UNIQUE,
  book_id        TEXT REFERENCES books(id) ON DELETE SET NULL,
  paired_by      TEXT CHECK (paired_by IS NULL OR paired_by IN ('folder','upload','isbn','title','person')),
  ext            TEXT NOT NULL CHECK (ext IN ('epub','pdf','txt','md','markdown','html','htm','xhtml','docx','fb2','fb2.zip','mobi','azw3','azw','kfx','kfx-zip')),
  size           INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns       INTEGER NOT NULL DEFAULT 0,
  quick_hash     TEXT NOT NULL CHECK (length(quick_hash) = 64),
  sha256         TEXT CHECK (sha256 IS NULL OR length(sha256) = 64),
  format         TEXT CHECK (format IS NULL OR format IN ('epub','pdf','txt','md','html','docx','fb2','mobi','azw3')),
  state          TEXT NOT NULL CHECK (state IN ('pending','ready','refused','failed')),
  refused_reason TEXT CHECK (refused_reason IS NULL OR refused_reason IN ('drm','unsupported','damaged','too_large','no_text')),
  error_code     TEXT,
  job_id         TEXT,
  original_name  TEXT NOT NULL DEFAULT '',
  uploaded_by    TEXT REFERENCES users(id) ON DELETE SET NULL,
  missing_since  INTEGER,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  CHECK ((origin = 'library') = (path IS NOT NULL)),
  CHECK ((path IS NULL) = (folder_id IS NULL)),
  CHECK (origin = 'library' OR sha256 IS NOT NULL),
  CHECK ((state = 'refused') = (refused_reason IS NOT NULL))
) STRICT;
CREATE INDEX ebook_files_book ON ebook_files(book_id) WHERE book_id IS NOT NULL;
CREATE INDEX ebook_files_folder ON ebook_files(folder_id) WHERE folder_id IS NOT NULL;
CREATE INDEX ebook_files_quick_hash ON ebook_files(quick_hash);
CREATE INDEX ebook_files_pending ON ebook_files(state) WHERE state = 'pending';
CREATE UNIQUE INDEX ebook_files_upload_once ON ebook_files(book_id, sha256) WHERE origin = 'upload' AND book_id IS NOT NULL;

CREATE TABLE editions (
  id            TEXT PRIMARY KEY,
  ebook_file_id TEXT NOT NULL REFERENCES ebook_files(id) ON DELETE CASCADE,
  source_sha256 TEXT NOT NULL CHECK (length(source_sha256) = 64),
  recipe        TEXT NOT NULL CHECK (length(recipe) > 0),
  doc_sha256    TEXT NOT NULL CHECK (length(doc_sha256) = 64),
  doc_bytes     INTEGER NOT NULL CHECK (doc_bytes > 0),
  format        TEXT NOT NULL CHECK (format IN ('epub','pdf','txt','md','html','docx','fb2','mobi','azw3')),
  layout        TEXT NOT NULL DEFAULT 'flow' CHECK (layout IN ('flow','page')),
  title         TEXT NOT NULL DEFAULT '',
  subtitle      TEXT NOT NULL DEFAULT '',
  authors_json  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(authors_json) AND json_type(authors_json) = 'array'),
  language      TEXT NOT NULL DEFAULT '',
  isbn          TEXT NOT NULL DEFAULT '',
  asin          TEXT NOT NULL DEFAULT '',
  chapter_count INTEGER NOT NULL CHECK (chapter_count >= 0),
  word_count    INTEGER NOT NULL CHECK (word_count >= 0),
  char_count    INTEGER NOT NULL CHECK (char_count >= 0),
  page_count    INTEGER NOT NULL DEFAULT 0 CHECK (page_count >= 0),
  created_at    INTEGER NOT NULL,
  UNIQUE (ebook_file_id, source_sha256, recipe)
) STRICT;
CREATE INDEX editions_doc ON editions(doc_sha256);

CREATE TABLE edition_chapters (
  edition_id     TEXT NOT NULL REFERENCES editions(id) ON DELETE CASCADE,
  idx            INTEGER NOT NULL CHECK (idx >= 1),
  title          TEXT NOT NULL DEFAULT '',
  front          INTEGER NOT NULL DEFAULT 0 CHECK (front IN (0,1)),
  first_line     TEXT NOT NULL DEFAULT '',
  first_block_id TEXT NOT NULL DEFAULT '',
  block_count    INTEGER NOT NULL CHECK (block_count >= 0),
  word_count     INTEGER NOT NULL CHECK (word_count >= 0),
  char_count     INTEGER NOT NULL CHECK (char_count >= 0),
  src            TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (edition_id, idx)
) STRICT, WITHOUT ROWID;
