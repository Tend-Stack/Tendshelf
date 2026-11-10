-- 0041_queue_episodes: one Up next holds books and podcast episodes (decision 0038, plan P5).
-- A row names its item by item_id (the book or the episode id) and by exactly one of book_id and
-- episode_id, so the table still cascades with the book, the episode and the person. Positions stay
-- signed and shared by both kinds, so a mixed queue plays in one order. Existing rows are copied as books.
CREATE TABLE queue_items_new (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL,
  book_id TEXT REFERENCES books(id) ON DELETE CASCADE,
  episode_id TEXT REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  position INTEGER NOT NULL, added_at INTEGER NOT NULL CHECK (added_at > 0),
  CHECK ((book_id IS NULL) <> (episode_id IS NULL)),
  CHECK (item_id = COALESCE(book_id, episode_id)),
  PRIMARY KEY (user_id, item_id)) STRICT, WITHOUT ROWID;
INSERT INTO queue_items_new(user_id, item_id, book_id, episode_id, position, added_at)
  SELECT user_id, book_id, book_id, NULL, position, added_at FROM queue_items;
DROP TABLE queue_items;
ALTER TABLE queue_items_new RENAME TO queue_items;
CREATE INDEX queue_items_order ON queue_items(user_id, position, item_id);
CREATE INDEX queue_items_book ON queue_items(book_id) WHERE book_id IS NOT NULL;
CREATE INDEX queue_items_episode ON queue_items(episode_id) WHERE episode_id IS NOT NULL;
