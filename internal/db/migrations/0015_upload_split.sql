-- 0015_upload_split: a .zip that holds several top-level book folders becomes one upload for each
-- folder. split_from names the upload the zip arrived as (NULL for every other upload), so the
-- Add books page can show the new rows beside the original one.
ALTER TABLE uploads ADD COLUMN split_from TEXT;
CREATE INDEX uploads_split ON uploads(split_from) WHERE split_from IS NOT NULL;
