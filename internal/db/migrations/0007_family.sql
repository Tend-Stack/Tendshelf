-- 0007_family: grants, invites, share links and device tokens (M2 Family).
CREATE TABLE grants (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope TEXT NOT NULL CHECK (scope IN ('all','library','shelf','book')),
  library_id TEXT REFERENCES libraries(id) ON DELETE CASCADE, shelf_id TEXT REFERENCES shelves(id) ON DELETE CASCADE,
  book_id TEXT REFERENCES books(id) ON DELETE CASCADE,
  can_download INTEGER NOT NULL DEFAULT 0 CHECK (can_download IN (0,1)), can_read INTEGER NOT NULL DEFAULT 0 CHECK (can_read IN (0,1)),
  expires_at INTEGER CHECK (expires_at IS NULL OR expires_at > created_at),
  created_by TEXT REFERENCES users(id) ON DELETE SET NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
  CHECK ((library_id IS NOT NULL) = (scope = 'library') AND (shelf_id IS NOT NULL) = (scope = 'shelf')
    AND (book_id IS NOT NULL) = (scope = 'book'))) STRICT;
CREATE UNIQUE INDEX grants_target ON grants(user_id, scope, coalesce(library_id, shelf_id, book_id, ''));
CREATE INDEX grants_library ON grants(library_id);
CREATE INDEX grants_shelf ON grants(shelf_id);
CREATE INDEX grants_book ON grants(book_id);
CREATE INDEX grants_created_by ON grants(created_by);
CREATE TABLE invites (id TEXT PRIMARY KEY, token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 80),
  email TEXT CHECK (email IS NULL OR length(email) BETWEEN 3 AND 254), note TEXT NOT NULL DEFAULT '' CHECK (length(note) <= 500),
  role TEXT NOT NULL CHECK (role IN ('admin','member','guest')),
  grants_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(grants_json) AND json_type(grants_json) = 'array'),
  expires_at INTEGER CHECK (expires_at IS NULL OR expires_at > created_at),
  max_uses INTEGER NOT NULL DEFAULT 1 CHECK (max_uses BETWEEN 1 AND 50),
  use_count INTEGER NOT NULL DEFAULT 0 CHECK (use_count BETWEEN 0 AND max_uses),
  revoked_at INTEGER, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL) STRICT;
CREATE INDEX invites_created_by ON invites(created_by);
CREATE TABLE share_links (id TEXT PRIMARY KEY, token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
  book_id TEXT NOT NULL REFERENCES books(id) ON DELETE CASCADE, created_by TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label TEXT NOT NULL DEFAULT '' CHECK (length(label) <= 80), password_hash TEXT,
  can_read INTEGER NOT NULL DEFAULT 0 CHECK (can_read IN (0,1)), expires_at INTEGER NOT NULL, revoked_at INTEGER,
  created_at INTEGER NOT NULL, CHECK (expires_at > created_at AND expires_at <= created_at + 7776000000)) STRICT;
CREATE INDEX share_links_book ON share_links(book_id);
CREATE INDEX share_links_created_by ON share_links(created_by);
CREATE TABLE devices (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL CHECK (length(trim(name)) BETWEEN 1 AND 64), kind TEXT NOT NULL CHECK (kind IN ('web','pwa','desktop','mobile')),
  token_hash BLOB UNIQUE CHECK (token_hash IS NULL OR length(token_hash) = 32), created_at INTEGER NOT NULL,
  last_seen_at INTEGER, revoked_at INTEGER, CHECK (revoked_at IS NULL OR token_hash IS NULL)) STRICT;
CREATE INDEX devices_user ON devices(user_id);
