-- 0047_custom_providers: lookup sites an admin added (decision 0046). auth_value is a secret: never shown or logged.
CREATE TABLE custom_providers (
  id         TEXT PRIMARY KEY,                       -- db.NewID("cp")
  name       TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
  base_url   TEXT NOT NULL CHECK (length(base_url) BETWEEN 8 AND 500 AND (base_url LIKE 'https://%' OR base_url LIKE 'http://%')),
  auth_value TEXT NOT NULL DEFAULT '' CHECK (length(auth_value) <= 1000),
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL,
  created_at INTEGER NOT NULL CHECK (created_at > 0)
) STRICT, WITHOUT ROWID;
CREATE UNIQUE INDEX custom_providers_name ON custom_providers(name COLLATE NOCASE);
