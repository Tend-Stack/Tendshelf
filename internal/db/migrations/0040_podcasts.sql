-- Podcasts and radio (decision 0038). Shows, episodes and stations are stored once per server; subscriptions, state,
-- favourites and plays belong to one person and go with their user. A show is keyed by its normalized feed address,
-- an episode by its guid_key, a station by its stream address. ondacast_slug is only a hint.
CREATE TABLE podcast_shows (id TEXT PRIMARY KEY, feed_url TEXT NOT NULL UNIQUE CHECK (length(feed_url) BETWEEN 8 AND 2048),
  title TEXT NOT NULL DEFAULT '', author TEXT NOT NULL DEFAULT '', notes_html TEXT NOT NULL DEFAULT '', site_url TEXT NOT NULL DEFAULT '',
  language TEXT NOT NULL DEFAULT '', art_url TEXT NOT NULL DEFAULT '', art_path TEXT NOT NULL DEFAULT '', art_sha TEXT NOT NULL DEFAULT '',
  ondacast_slug TEXT, etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '', fetched_at INTEGER, ok_at INTEGER,
  fail_code TEXT NOT NULL DEFAULT '', fail_count INTEGER NOT NULL DEFAULT 0, next_refresh_at INTEGER,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL) STRICT;
CREATE TABLE podcast_episodes (id TEXT PRIMARY KEY, show_id TEXT NOT NULL REFERENCES podcast_shows(id) ON DELETE CASCADE,
  guid_key TEXT NOT NULL, guid TEXT NOT NULL DEFAULT '', title TEXT NOT NULL, notes_html TEXT NOT NULL DEFAULT '',
  link TEXT NOT NULL DEFAULT '', published_at INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER, audio_url TEXT NOT NULL,
  audio_type TEXT NOT NULL DEFAULT '', audio_bytes INTEGER, season INTEGER, number INTEGER,
  kind TEXT NOT NULL DEFAULT 'full' CHECK (kind IN ('full','trailer','bonus')), chapters_url TEXT NOT NULL DEFAULT '',
  chapters_json TEXT, cache_path TEXT, cache_bytes INTEGER, cached_at INTEGER, gone_at INTEGER,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, UNIQUE (show_id, guid_key)) STRICT;
CREATE INDEX podcast_episodes_show ON podcast_episodes(show_id, published_at DESC, id);
CREATE INDEX podcast_episodes_cached ON podcast_episodes(cached_at) WHERE cache_path IS NOT NULL;
CREATE TABLE podcast_subscriptions (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  show_id TEXT NOT NULL REFERENCES podcast_shows(id) ON DELETE CASCADE, followed_at INTEGER NOT NULL, seen_at INTEGER NOT NULL,
  speed REAL CHECK (speed IS NULL OR speed BETWEEN 0.5 AND 3.5),
  skip_intro_s INTEGER NOT NULL DEFAULT 0 CHECK (skip_intro_s BETWEEN 0 AND 600),
  skip_outro_s INTEGER NOT NULL DEFAULT 0 CHECK (skip_outro_s BETWEEN 0 AND 600),
  server_auto INTEGER NOT NULL DEFAULT 0 CHECK (server_auto IN (0,1)),
  keep_latest INTEGER NOT NULL DEFAULT 3 CHECK (keep_latest BETWEEN 1 AND 50),
  PRIMARY KEY (user_id, show_id)) STRICT, WITHOUT ROWID;
CREATE INDEX podcast_subscriptions_show ON podcast_subscriptions(show_id);
CREATE TABLE podcast_episode_state (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  episode_id TEXT NOT NULL REFERENCES podcast_episodes(id) ON DELETE CASCADE,
  position_ms INTEGER NOT NULL DEFAULT 0 CHECK (position_ms >= 0), played INTEGER NOT NULL DEFAULT 0 CHECK (played IN (0,1)),
  played_at INTEGER, favourite INTEGER NOT NULL DEFAULT 0 CHECK (favourite IN (0,1)), favourite_at INTEGER, last_played_at INTEGER,
  device_id TEXT NOT NULL DEFAULT '', client_updated_at INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, episode_id)) STRICT, WITHOUT ROWID;
CREATE INDEX podcast_state_history ON podcast_episode_state(user_id, last_played_at DESC) WHERE last_played_at IS NOT NULL;
CREATE INDEX podcast_state_fav ON podcast_episode_state(user_id, favourite_at DESC) WHERE favourite = 1;
CREATE INDEX podcast_state_episode ON podcast_episode_state(episode_id);
-- Radio tables ship with podcasts so stations never need a schema change before R1.
CREATE TABLE radio_stations (id TEXT PRIMARY KEY, stream_url TEXT NOT NULL UNIQUE CHECK (length(stream_url) BETWEEN 8 AND 2048),
  name TEXT NOT NULL, site_url TEXT NOT NULL DEFAULT '', art_url TEXT NOT NULL DEFAULT '', art_path TEXT NOT NULL DEFAULT '',
  codec TEXT NOT NULL DEFAULT '', bitrate_kbps INTEGER, country TEXT NOT NULL DEFAULT '', genre TEXT NOT NULL DEFAULT '',
  ondacast_slug TEXT, checked_at INTEGER, fail_code TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL) STRICT;
CREATE TABLE radio_favourites (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  station_id TEXT NOT NULL REFERENCES radio_stations(id) ON DELETE CASCADE, position INTEGER NOT NULL, added_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, station_id)) STRICT, WITHOUT ROWID;
CREATE TABLE radio_plays (user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  station_id TEXT NOT NULL REFERENCES radio_stations(id) ON DELETE CASCADE, last_played_at INTEGER NOT NULL,
  plays INTEGER NOT NULL DEFAULT 1, PRIMARY KEY (user_id, station_id)) STRICT, WITHOUT ROWID;
CREATE INDEX radio_plays_recent ON radio_plays(user_id, last_played_at DESC);
