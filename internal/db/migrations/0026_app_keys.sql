-- app_keys (S5): the devices table gains two kinds, 'opds' and 'koreader', for app keys that
-- open one surface each. SQLite cannot change a CHECK, so the table is rebuilt; no table has a
-- foreign key to devices. Rows, hashes and the live/revoked state survive unchanged.
CREATE TABLE devices_new (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64),
  kind TEXT NOT NULL CHECK (kind IN ('web','pwa','desktop','mobile','opds','koreader')),
  token_hash BLOB UNIQUE CHECK (token_hash IS NULL OR length(token_hash) = 32), created_at INTEGER NOT NULL,
  last_seen_at INTEGER, revoked_at INTEGER, CHECK (revoked_at IS NULL OR token_hash IS NULL)) STRICT;
INSERT INTO devices_new (id, user_id, name, kind, token_hash, created_at, last_seen_at, revoked_at)
  SELECT id, user_id, name, kind, token_hash, created_at, last_seen_at, revoked_at FROM devices;
DROP TABLE devices;
ALTER TABLE devices_new RENAME TO devices;
CREATE INDEX devices_user ON devices(user_id);
