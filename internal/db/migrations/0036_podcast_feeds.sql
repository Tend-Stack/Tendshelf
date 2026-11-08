-- podcast_feeds: a private RSS address for one person and one book or series (decision 0031). Only the SHA-256 of the token is stored.
-- book_id and series_id have no foreign key on purpose: a feed whose target is gone answers 404 and stays listed until turned off.
CREATE TABLE podcast_feeds (id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL DEFAULT '' CHECK (length(name) <= 64),
  book_id TEXT, series_id TEXT,
  created_at INTEGER NOT NULL, last_used_at INTEGER, revoked_at INTEGER,
  CHECK ((book_id IS NULL) <> (series_id IS NULL))) STRICT;
CREATE INDEX podcast_feeds_user ON podcast_feeds(user_id) WHERE revoked_at IS NULL;
