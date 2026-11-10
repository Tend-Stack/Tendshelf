-- oidc_identities: a provider account (issuer + sub) linked to one Tendshelf account (decision 0037).
CREATE TABLE oidc_identities (issuer TEXT NOT NULL CHECK (length(issuer) BETWEEN 1 AND 512),
  subject TEXT NOT NULL CHECK (length(subject) BETWEEN 1 AND 255), user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  email TEXT CHECK (email IS NULL OR length(email) <= 254), linked_via TEXT NOT NULL CHECK (linked_via IN ('account','email','new')),
  created_at INTEGER NOT NULL, last_used_at INTEGER, PRIMARY KEY (issuer, subject)) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX oidc_identities_user ON oidc_identities(user_id, issuer);
-- oidc_logins: one sign-in under way, 10 minutes. Only SHA-256 of the state is kept; the raw state is the browser's ts_oidc cookie.
CREATE TABLE oidc_logins (id TEXT PRIMARY KEY, state_hash BLOB NOT NULL UNIQUE CHECK (length(state_hash) = 32),
  purpose TEXT NOT NULL CHECK (purpose IN ('signin','link')), issuer TEXT NOT NULL,
  nonce TEXT NOT NULL CHECK (length(nonce) = 43), verifier TEXT NOT NULL CHECK (length(verifier) = 43),
  next TEXT NOT NULL DEFAULT '/' CHECK (length(next) <= 2048),
  user_id TEXT REFERENCES users(id) ON DELETE CASCADE, session_hash BLOB REFERENCES sessions(token_hash) ON DELETE CASCADE,
  subject TEXT, email TEXT, email_verified INTEGER NOT NULL DEFAULT 0 CHECK (email_verified IN (0,1)),
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, used_at INTEGER,
  addr_key TEXT NOT NULL DEFAULT '',
  CHECK ((purpose = 'link') = (user_id IS NOT NULL AND session_hash IS NOT NULL))) STRICT;
CREATE INDEX oidc_logins_expires ON oidc_logins(expires_at);
