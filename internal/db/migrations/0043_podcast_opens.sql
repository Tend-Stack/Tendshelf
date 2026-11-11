-- 0043_podcast_opens: a show a person opened from search or the suggestions without following it (decision 0040).
CREATE TABLE podcast_opens (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  show_id TEXT NOT NULL REFERENCES podcast_shows(id) ON DELETE CASCADE,
  opened_at INTEGER NOT NULL CHECK (opened_at > 0),
  PRIMARY KEY (user_id, show_id)) STRICT, WITHOUT ROWID;
CREATE INDEX podcast_opens_show ON podcast_opens(show_id);
CREATE INDEX podcast_opens_recent ON podcast_opens(user_id, opened_at);
