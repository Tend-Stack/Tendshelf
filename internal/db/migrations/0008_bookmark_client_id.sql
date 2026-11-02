-- 0008_bookmark_client_id: an optional id the app chooses for a bookmark, so a retried create is not saved twice (P3-9b).
ALTER TABLE bookmarks ADD COLUMN client_id TEXT CHECK (client_id IS NULL OR length(client_id) BETWEEN 1 AND 64);
CREATE UNIQUE INDEX bookmarks_client ON bookmarks(user_id, client_id) WHERE client_id IS NOT NULL;
