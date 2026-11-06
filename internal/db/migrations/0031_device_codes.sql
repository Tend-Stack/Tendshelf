-- device_codes: one-time sign-in codes that let the phone app swap a signed-in WebView session for a
-- device token (decision 0026). Only the hash of a code is stored. A code belongs to one session and
-- is deleted with it. sessions.device_id binds a session to the device it was swapped for, so ending
-- the device ends the session; it has no foreign key because device rows are pruned and rebuilt.
ALTER TABLE sessions ADD COLUMN device_id TEXT;
CREATE TABLE device_codes (code_hash BLOB PRIMARY KEY CHECK (length(code_hash) = 32),
  session_hash BLOB NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
  challenge BLOB NOT NULL CHECK (length(challenge) = 32),
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL) STRICT, WITHOUT ROWID;
CREATE INDEX device_codes_session ON device_codes(session_hash);
