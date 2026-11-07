-- sign_in_links: a one-time link an admin makes so a person can sign in and choose a password (decision 0030). Only the SHA-256
-- of the token is stored. session_hash is the session the link started; it unlocks /me/new-password for one hour.
CREATE TABLE sign_in_links (id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER, revoked_at INTEGER,
  session_hash BLOB REFERENCES sessions(token_hash) ON DELETE SET NULL CHECK (session_hash IS NULL OR length(session_hash) = 32)) STRICT;
CREATE UNIQUE INDEX sign_in_links_open ON sign_in_links(user_id) WHERE used_at IS NULL AND revoked_at IS NULL;
CREATE INDEX sign_in_links_session ON sign_in_links(session_hash) WHERE session_hash IS NOT NULL;
