CREATE TABLE IF NOT EXISTS incident_vote_events (
  id TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  nickname TEXT NOT NULL,
  vote_value TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_incident_vote_events_incident
  ON incident_vote_events(incident_id, created_at DESC);
