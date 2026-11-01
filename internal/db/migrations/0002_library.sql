-- 0002_library: libraries, folders, books, audio files, chapters, people, series, labels, covers.
-- books_fts arrives with search (migration 0003 rebuilds it from these rows).
CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT NOT NULL CHECK (length(trim(name)) > 0),
  scan_interval_min INTEGER NOT NULL DEFAULT 0 CHECK (scan_interval_min >= 0), created_at INTEGER NOT NULL) STRICT;
CREATE TABLE library_folders (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL) STRICT;
CREATE INDEX library_folders_library ON library_folders(library_id);
CREATE TABLE books (id TEXT PRIMARY KEY, library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  folder_id TEXT NOT NULL REFERENCES library_folders(id) ON DELETE CASCADE, root_path TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL DEFAULT '', sort_title TEXT NOT NULL DEFAULT '', subtitle TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', publisher TEXT NOT NULL DEFAULT '', published_year INTEGER,
  language TEXT NOT NULL DEFAULT '', isbn TEXT NOT NULL DEFAULT '', asin TEXT NOT NULL DEFAULT '',
  abridged INTEGER NOT NULL DEFAULT 0 CHECK (abridged IN (0,1)), dramatized INTEGER NOT NULL DEFAULT 0 CHECK (dramatized IN (0,1)),
  explicit INTEGER NOT NULL DEFAULT 0 CHECK (explicit IN (0,1)), duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
  cover_id TEXT REFERENCES covers(id) ON DELETE SET NULL,
  refused_reason TEXT CHECK (refused_reason IS NULL OR refused_reason IN ('drm','unsupported','damaged','no_audio')),
  added_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, missing_since INTEGER) STRICT;
CREATE INDEX books_library_sort ON books(library_id, sort_title COLLATE NOCASE);
CREATE INDEX books_folder ON books(folder_id);
CREATE INDEX books_cover ON books(cover_id);
CREATE TABLE audio_files (id TEXT PRIMARY KEY, book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  path TEXT NOT NULL UNIQUE, idx INTEGER NOT NULL CHECK (idx >= 0), size INTEGER NOT NULL CHECK (size >= 0),
  mtime_ns INTEGER NOT NULL, quick_hash TEXT NOT NULL CHECK (length(quick_hash) = 64), sha256 TEXT CHECK (sha256 IS NULL OR length(sha256) = 64),
  container TEXT NOT NULL DEFAULT '', codec TEXT NOT NULL DEFAULT '', bitrate INTEGER NOT NULL DEFAULT 0,
  channels INTEGER NOT NULL DEFAULT 0, sample_rate INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0), offset_ms INTEGER NOT NULL DEFAULT 0 CHECK (offset_ms >= 0),
  encrypted INTEGER NOT NULL DEFAULT 0 CHECK (encrypted IN (0,1)),
  probe_json TEXT CHECK (probe_json IS NULL OR json_valid(probe_json)), missing_since INTEGER, updated_at INTEGER NOT NULL) STRICT;
CREATE INDEX audio_files_book ON audio_files(book_id, idx);
CREATE INDEX audio_files_quick_hash ON audio_files(quick_hash);
CREATE INDEX audio_files_unprobed ON audio_files(book_id) WHERE probe_json IS NULL;
CREATE TABLE chapters (book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, idx INTEGER NOT NULL CHECK (idx >= 0),
  start_ms INTEGER NOT NULL CHECK (start_ms >= 0), end_ms INTEGER NOT NULL, title TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL CHECK (source IN ('file','files','sidecar','online','user')),
  CHECK (end_ms >= start_ms), PRIMARY KEY (book_id, idx)) STRICT, WITHOUT ROWID;
CREATE TABLE metadata_values (book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, field TEXT NOT NULL,
  source TEXT NOT NULL, value_json TEXT NOT NULL CHECK (json_valid(value_json)), observed_at INTEGER NOT NULL,
  PRIMARY KEY (book_id, field, source)) STRICT, WITHOUT ROWID;
CREATE TABLE people (id TEXT PRIMARY KEY, name TEXT NOT NULL CHECK (length(trim(name)) > 0), sort_name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', image_id TEXT, asin TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL) STRICT;
CREATE INDEX people_name ON people(name COLLATE NOCASE);
CREATE TABLE book_people (book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE, role TEXT NOT NULL CHECK (role IN ('author','narrator')),
  idx INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (book_id, role, person_id)) STRICT, WITHOUT ROWID;
CREATE INDEX book_people_person ON book_people(person_id);
CREATE TABLE series (id TEXT PRIMARY KEY, name TEXT NOT NULL CHECK (length(trim(name)) > 0), sort_name TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL) STRICT;
CREATE INDEX series_name ON series(name COLLATE NOCASE);
CREATE TABLE book_series (book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  series_id TEXT NOT NULL REFERENCES series(id) ON DELETE CASCADE, sequence TEXT NOT NULL DEFAULT '', idx INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (book_id, series_id)) STRICT, WITHOUT ROWID;
CREATE INDEX book_series_series ON book_series(series_id);
CREATE TABLE book_labels (book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('genre','tag')), value TEXT NOT NULL COLLATE NOCASE CHECK (length(trim(value)) > 0),
  PRIMARY KEY (book_id, kind, value)) STRICT, WITHOUT ROWID;
CREATE INDEX book_labels_value ON book_labels(kind, value);
CREATE TABLE covers (id TEXT PRIMARY KEY, book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  source TEXT NOT NULL CHECK (source IN ('embedded','folder','online','upload')),
  mime TEXT NOT NULL CHECK (mime IN ('image/jpeg','image/png','image/webp')),
  width INTEGER NOT NULL CHECK (width > 0), height INTEGER NOT NULL CHECK (height > 0),
  sha256 TEXT NOT NULL CHECK (length(sha256) = 64), path TEXT NOT NULL,
  tint TEXT CHECK (tint IS NULL OR (length(tint) = 7 AND tint GLOB '#[0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f][0-9a-f]')),
  accent_json TEXT CHECK (accent_json IS NULL OR json_valid(accent_json)), created_at INTEGER NOT NULL,
  UNIQUE (book_id, sha256)) STRICT;
