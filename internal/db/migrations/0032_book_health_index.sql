-- book_health_index: Library health asks, for every book it lists, whether any book of the same folder is still
-- present (the "folder gone" flag), and the Libraries page asks it on every load. During an outage every book
-- is missing and each of those questions scanned the whole folder. This index answers it from the first entry.
CREATE INDEX books_folder_present ON books(folder_id, missing_since);
