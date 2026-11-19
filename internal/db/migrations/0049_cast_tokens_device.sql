-- 0049_cast_tokens_device: a live address for the iPhone app is a station cast token bound to one device token (decision 0049).
-- device_id is NULL for every casting token; deleting the device deletes its address.
ALTER TABLE cast_tokens ADD COLUMN device_id TEXT REFERENCES devices(id) ON DELETE CASCADE CHECK (device_id IS NULL OR kind = 'station');
CREATE INDEX cast_tokens_device ON cast_tokens(device_id) WHERE device_id IS NOT NULL;
