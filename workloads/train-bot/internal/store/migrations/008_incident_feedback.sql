CREATE TABLE IF NOT EXISTS incident_votes (
  incident_id TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  nickname TEXT NOT NULL,
  vote_value TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY(incident_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_incident_votes_incident
  ON incident_votes(incident_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS incident_comments (
  id TEXT PRIMARY KEY,
  incident_id TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  nickname TEXT NOT NULL,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_incident_comments_incident
  ON incident_comments(incident_id, created_at DESC);
