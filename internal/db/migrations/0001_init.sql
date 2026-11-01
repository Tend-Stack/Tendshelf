CREATE TABLE settings (key TEXT PRIMARY KEY, value_json TEXT NOT NULL CHECK (json_valid(value_json))) STRICT, WITHOUT ROWID;
CREATE TABLE users (
  id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE COLLATE NOCASE, display_name TEXT NOT NULL DEFAULT '',
  email TEXT, role TEXT NOT NULL CHECK (role IN ('owner','admin','member','guest')), password_hash TEXT,
  disabled INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0,1)), created_at INTEGER NOT NULL, last_seen_at INTEGER) STRICT;
CREATE UNIQUE INDEX users_one_owner ON users(role) WHERE role = 'owner';
CREATE TABLE sessions (
  token_hash BLOB PRIMARY KEY CHECK (length(token_hash) = 32), user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL, expires_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL, user_agent TEXT NOT NULL DEFAULT '') STRICT;
CREATE INDEX sessions_user ON sessions(user_id);
CREATE INDEX sessions_expires ON sessions(expires_at);
CREATE TABLE user_settings (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, key TEXT NOT NULL,
  value_json TEXT NOT NULL CHECK (json_valid(value_json)), updated_at INTEGER NOT NULL, PRIMARY KEY (user_id, key)) STRICT, WITHOUT ROWID;
CREATE TABLE jobs (
  id TEXT PRIMARY KEY, kind TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('queued','running','done','failed','cancelled','held')),
  payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json)),
  result_json TEXT CHECK (result_json IS NULL OR json_valid(result_json)), error_code TEXT, error_text TEXT,
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0), max_attempts INTEGER NOT NULL DEFAULT 3 CHECK (max_attempts >= 1),
  lease_until INTEGER, not_before INTEGER NOT NULL DEFAULT 0,
  progress_json TEXT CHECK (progress_json IS NULL OR json_valid(progress_json)),
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  CHECK ((state = 'running') = (lease_until IS NOT NULL))) STRICT;
CREATE INDEX jobs_claim ON jobs(kind, not_before, created_at, id) WHERE state = 'queued';
CREATE INDEX jobs_lease ON jobs(lease_until) WHERE state = 'running';
-- audit_log has no foreign key on user_id: the audit trail keeps the id after a user is deleted.
CREATE TABLE audit_log (
  id TEXT PRIMARY KEY, at INTEGER NOT NULL, user_id TEXT, action TEXT NOT NULL, target TEXT NOT NULL DEFAULT '',
  detail_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(detail_json))) STRICT;
CREATE INDEX audit_log_at ON audit_log(at);
