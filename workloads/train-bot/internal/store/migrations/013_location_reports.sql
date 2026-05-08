CREATE TABLE IF NOT EXISTS location_report_events (
  id TEXT PRIMARY KEY,
  scope TEXT NOT NULL,
  subject_id TEXT NOT NULL,
  subject_name TEXT NOT NULL,
  latitude REAL,
  longitude REAL,
  radius_meters INTEGER NOT NULL DEFAULT 0,
  description TEXT NOT NULL DEFAULT '',
  user_id INTEGER NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_location_report_created_at
  ON location_report_events(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_location_report_scope_time
  ON location_report_events(scope, subject_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_location_report_user_scope_time
  ON location_report_events(user_id, scope, subject_id, created_at DESC);
