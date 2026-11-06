-- 0024_watch_folders: watch folders and a scheduled scan (S1). libraries.watch is "Watch for new books"
-- (1 = on). It is on for every library, new and existing: a scan is read-only on library files, and the
-- vision's first job is "I drop files in and they appear". scan_interval_min, unused until now, becomes
-- the scheduled fallback scan in minutes (0 = never); libraries that had no schedule get 360 (six hours).
ALTER TABLE libraries ADD COLUMN watch INTEGER NOT NULL DEFAULT 1 CHECK (watch IN (0, 1));
UPDATE libraries SET scan_interval_min = 360 WHERE scan_interval_min = 0;
