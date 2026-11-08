-- sessions.name: a name the person gave one browser sign-in ("Kitchen laptop"). '' means none, and the
-- row shows the label worked out from the User-Agent. Sign-in activity never writes this column.
ALTER TABLE sessions ADD COLUMN name TEXT NOT NULL DEFAULT '' CHECK (length(name) <= 64);
