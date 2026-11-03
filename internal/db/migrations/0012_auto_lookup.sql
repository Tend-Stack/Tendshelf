-- 0012_auto_lookup: automatic and bulk book-detail lookups (AL-3). libraries.auto_lookup_at is when
-- the owner turned on "look up details online for new books" (NULL = off); book_lookups is both the
-- skip list and the resume cursor of the bulk job; online_usage counts network requests per UTC day
-- so a restart cannot reset the daily cap.
ALTER TABLE libraries ADD COLUMN auto_lookup_at INTEGER CHECK (auto_lookup_at IS NULL OR auto_lookup_at > 0);

CREATE TABLE book_lookups (
  book_id    TEXT PRIMARY KEY REFERENCES books(id) ON DELETE CASCADE,
  outcome    TEXT NOT NULL CHECK (outcome IN ('matched','needs_look','no_match','failed','kept','no_query','chosen','dismissed')),
  provider   TEXT NOT NULL DEFAULT '',
  job_id     TEXT NOT NULL DEFAULT '',
  checked_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX book_lookups_job ON book_lookups(job_id);

CREATE TABLE online_usage (
  day      INTEGER NOT NULL, -- UTC day number: unix ms / 86400000
  provider TEXT NOT NULL CHECK (provider IN ('audnexus','google','openlibrary','itunes')),
  requests INTEGER NOT NULL CHECK (requests >= 0),
  PRIMARY KEY (day, provider)
) STRICT, WITHOUT ROWID;
