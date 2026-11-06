-- listening_recent: the listening timeline (L6) pages one person's sessions newest first, keyset on (ended_at, id).
CREATE INDEX listening_sessions_recent ON listening_sessions(user_id, ended_at DESC, id);
