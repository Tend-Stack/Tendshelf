-- 0042_radio_hls: a station is either a continuous Icecast or Shoutcast stream ('icy') or an HLS
-- playlist the server turns into one stream ('hls', decision 0039). Existing rows are 'icy'. The relay
-- reads this column and never sniffs the address.
ALTER TABLE radio_stations ADD COLUMN format TEXT NOT NULL DEFAULT 'icy' CHECK (format IN ('icy','hls'));
