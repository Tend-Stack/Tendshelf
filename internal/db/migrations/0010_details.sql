-- 0010_details: the sidecar signature that tells a scan when a book's sidecar files changed (M4-1), and the cache of online lookups (M4-2).
CREATE TABLE book_sidecars (
  book_id    TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
  signature  TEXT NOT NULL,
  checked_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE TABLE online_cache (
  provider      TEXT NOT NULL CHECK (provider IN ('audnexus','audible','google','openlibrary','itunes')),
  query_key     TEXT NOT NULL CHECK (length(query_key) = 64),
  response_json TEXT NOT NULL CHECK (json_valid(response_json)),
  fetched_at    INTEGER NOT NULL,
  PRIMARY KEY (provider, query_key)
) STRICT, WITHOUT ROWID;

CREATE INDEX online_cache_fetched ON online_cache(fetched_at);
