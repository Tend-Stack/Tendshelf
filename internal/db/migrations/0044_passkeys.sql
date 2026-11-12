-- Passkeys (WebAuthn, decision 0041): a random user handle per account, the credentials, and the ceremonies under way.
ALTER TABLE users ADD COLUMN passkey_handle BLOB CHECK (passkey_handle IS NULL OR length(passkey_handle) = 32);
CREATE UNIQUE INDEX users_passkey_handle ON users(passkey_handle) WHERE passkey_handle IS NOT NULL;
CREATE TABLE passkeys (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  credential_id BLOB NOT NULL UNIQUE CHECK (length(credential_id) BETWEEN 16 AND 1023),
  rp_id TEXT NOT NULL CHECK (length(rp_id) BETWEEN 1 AND 253), public_key BLOB NOT NULL CHECK (length(public_key) BETWEEN 32 AND 1024),
  alg INTEGER NOT NULL CHECK (alg IN (-7, -8, -257)), sign_count INTEGER NOT NULL DEFAULT 0 CHECK (sign_count BETWEEN 0 AND 4294967295),
  backup_eligible INTEGER NOT NULL CHECK (backup_eligible IN (0,1)), backed_up INTEGER NOT NULL CHECK (backed_up IN (0,1)),
  transports TEXT NOT NULL DEFAULT '' CHECK (length(transports) <= 128), aaguid BLOB CHECK (aaguid IS NULL OR length(aaguid) = 16),
  name TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 256), created_at INTEGER NOT NULL, last_used_at INTEGER, copied_at INTEGER) STRICT;
CREATE INDEX passkeys_user ON passkeys(user_id);
CREATE TABLE passkey_ceremonies (challenge_hash BLOB PRIMARY KEY CHECK (length(challenge_hash) = 32),
  purpose TEXT NOT NULL CHECK (purpose IN ('signin','register')), rp_id TEXT NOT NULL, origin TEXT NOT NULL,
  user_id TEXT REFERENCES users(id) ON DELETE CASCADE, session_hash BLOB REFERENCES sessions(token_hash) ON DELETE CASCADE,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, addr_key TEXT NOT NULL DEFAULT '',
  CHECK ((purpose = 'register') = (user_id IS NOT NULL AND session_hash IS NOT NULL))) STRICT, WITHOUT ROWID;
CREATE INDEX passkey_ceremonies_expires ON passkey_ceremonies(expires_at);
