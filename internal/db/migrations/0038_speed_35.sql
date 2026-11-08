-- 0038_speed_35: the top playback speed goes from 3.0 to 3.5. SQLite cannot alter a CHECK, so the two
-- tables that bound a speed are rebuilt: book_settings (speed <= 3.5) and listening_sessions
-- (speed_pct BETWEEN 50 AND 350). Nothing references either table, so dropping them cascades nowhere.
CREATE TABLE book_settings_new (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE,
  speed REAL NOT NULL CHECK (speed >= 0.5 AND speed <= 3.5),
  updated_at INTEGER NOT NULL CHECK (updated_at > 0),
  PRIMARY KEY (user_id, book_id)) STRICT, WITHOUT ROWID;
INSERT INTO book_settings_new (user_id, book_id, speed, updated_at)
  SELECT user_id, book_id, speed, updated_at FROM book_settings;
DROP TABLE book_settings;
ALTER TABLE book_settings_new RENAME TO book_settings;
CREATE INDEX book_settings_book ON book_settings(book_id);

CREATE TABLE listening_sessions_new (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, device_id TEXT NOT NULL CHECK (length(device_id) BETWEEN 1 AND 64),
  started_at INTEGER NOT NULL, ended_at INTEGER NOT NULL CHECK (ended_at >= started_at),
  start_ms INTEGER NOT NULL CHECK (start_ms >= 0), end_ms INTEGER NOT NULL CHECK (end_ms >= 0),
  listened_ms INTEGER NOT NULL CHECK (listened_ms >= 0), speed_pct INTEGER NOT NULL CHECK (speed_pct BETWEEN 50 AND 350)) STRICT;
INSERT INTO listening_sessions_new (id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
  SELECT id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct FROM listening_sessions;
DROP TABLE listening_sessions;
ALTER TABLE listening_sessions_new RENAME TO listening_sessions;
CREATE INDEX listening_sessions_current ON listening_sessions(user_id, book_id, device_id, ended_at);
CREATE INDEX listening_sessions_user ON listening_sessions(user_id, started_at);
CREATE INDEX listening_sessions_book ON listening_sessions(book_id);
CREATE INDEX listening_sessions_recent ON listening_sessions(user_id, ended_at DESC, id);
