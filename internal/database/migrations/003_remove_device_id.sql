ALTER TABLE capture DROP COLUMN device_id;

---- create above / drop below ----

ALTER TABLE capture ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE capture ALTER COLUMN device_id DROP DEFAULT;
