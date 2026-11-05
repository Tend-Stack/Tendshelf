-- 0023_jobs_retention: job rows are swept once finished, and lookups by kind no longer scan every row.
-- jobs_kind serves "latest job of a kind" lookups (the claim index covers queued rows only).
CREATE INDEX jobs_kind ON jobs(kind, created_at);
-- jobs_finished serves the retention sweep of done, failed and cancelled rows.
CREATE INDEX jobs_finished ON jobs(updated_at) WHERE state IN ('done','failed','cancelled');
