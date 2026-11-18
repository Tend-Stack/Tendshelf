-- 0047_cast_tokens: a short-lived address a Cast device uses to fetch one book, episode or station (decision 0047). Only the SHA-256 is stored.
-- target_id has no foreign key: a target that is gone answers 404 until the row expires.
CREATE TABLE cast_tokens (token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK (kind IN ('book','episode','station')), target_id TEXT NOT NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, CHECK (expires_at > created_at)) STRICT, WITHOUT ROWID;
CREATE INDEX cast_tokens_user ON cast_tokens(user_id, created_at);
CREATE INDEX cast_tokens_expires ON cast_tokens(expires_at);
