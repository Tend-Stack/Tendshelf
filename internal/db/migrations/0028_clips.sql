-- clips: a bookmark may end (end_ms, a moment in the audio after position_ms) and has a kind from a closed list.
-- Existing rows stay plain bookmarks with no end.
ALTER TABLE bookmarks ADD COLUMN end_ms INTEGER CHECK (end_ms IS NULL OR end_ms > position_ms);
ALTER TABLE bookmarks ADD COLUMN kind TEXT NOT NULL DEFAULT 'bookmark' CHECK (kind IN ('bookmark','favourite','quote','note','revisit'));
