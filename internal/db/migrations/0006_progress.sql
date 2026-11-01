-- 0006_progress: listening progress, bookmarks and listening sessions (P1c). Text locations arrive with R4/R5.
CREATE TABLE progress (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  position_ms INTEGER NOT NULL CHECK (position_ms >= 0), duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
  finished_at INTEGER CHECK (finished_at IS NULL OR finished_at > 0),
  client_updated_at INTEGER NOT NULL CHECK (client_updated_at > 0), server_updated_at INTEGER NOT NULL,
  device_id TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  device_label TEXT NOT NULL DEFAULT '' CHECK (length(device_label) <= 64),
  PRIMARY KEY (user_id, book_id)) STRICT, WITHOUT ROWID;
CREATE INDEX progress_recent ON progress(user_id, client_updated_at);
CREATE INDEX progress_book ON progress(book_id);
CREATE TABLE bookmarks (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, position_ms INTEGER NOT NULL CHECK (position_ms >= 0),
  note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 2000),
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, deleted_at INTEGER) STRICT;
CREATE INDEX bookmarks_live ON bookmarks(user_id, book_id, created_at) WHERE deleted_at IS NULL;
CREATE INDEX bookmarks_sync ON bookmarks(user_id, updated_at);
CREATE INDEX bookmarks_book ON bookmarks(book_id);
CREATE TABLE listening_sessions (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, device_id TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL CHECK (ended_at >= started_at),
  start_ms INTEGER NOT NULL CHECK (start_ms >= 0), end_ms INTEGER NOT NULL CHECK (end_ms >= 0),
  listened_ms INTEGER NOT NULL CHECK (listened_ms >= 0), speed_pct INTEGER NOT NULL CHECK (speed_pct BETWEEN 50 AND 300)) STRICT;
CREATE INDEX listening_sessions_current ON listening_sessions(user_id, book_id, device_id, ended_at);
CREATE INDEX listening_sessions_user ON listening_sessions(user_id, started_at);
CREATE INDEX listening_sessions_book ON listening_sessions(book_id);
