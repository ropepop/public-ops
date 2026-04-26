ALTER TABLE stop_sightings
ADD COLUMN is_hidden INTEGER NOT NULL DEFAULT 0;

ALTER TABLE vehicle_sightings
ADD COLUMN is_hidden INTEGER NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS stop_sightings_hidden_visible_idx
ON stop_sightings(is_hidden, stop_id, created_at DESC);

CREATE INDEX IF NOT EXISTS vehicle_sightings_hidden_visible_idx
ON vehicle_sightings(is_hidden, stop_id, created_at DESC);
