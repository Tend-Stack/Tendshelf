-- 0003_probe_error: a settled-but-unreadable state for audio files. A file is settled when probe_json or
-- probe_error is set; without it a damaged file would stay unprobed forever and its book would never finalise.
-- probe_error holds a media error code (not_audio, file_unreadable, drm), NULL when the file read fine.
ALTER TABLE audio_files ADD COLUMN probe_error TEXT CHECK (probe_error IS NULL OR probe_error GLOB '[a-z]*');
