-- device_pairings: a signed-in session asking to add a phone by QR (decision 0028). Only hashes of the
-- QR token and of the claim are stored; the 4-digit match code is the one thing kept in clear, for
-- three minutes, because the owner types it. A pairing ends with the session that showed the QR.
CREATE TABLE device_pairings (id TEXT PRIMARY KEY,
  token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  owner_session BLOB NOT NULL REFERENCES sessions(token_hash) ON DELETE CASCADE,
  state TEXT NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting','claimed','approved')),
  claim_hash BLOB UNIQUE CHECK (claim_hash IS NULL OR length(claim_hash) = 32),
  challenge BLOB CHECK (challenge IS NULL OR length(challenge) = 32),
  match_code TEXT CHECK (match_code IS NULL OR length(match_code) = 4),
  phone_name TEXT, phone_ip TEXT,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL) STRICT;
CREATE INDEX device_pairings_owner ON device_pairings(owner_session);
