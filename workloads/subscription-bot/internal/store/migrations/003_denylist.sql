CREATE TABLE IF NOT EXISTS denylist_entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entry_type TEXT NOT NULL,
  entry_value TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  created_by_user_id INTEGER REFERENCES users(id),
  created_at TEXT NOT NULL,
  UNIQUE(entry_type, entry_value)
);

CREATE INDEX IF NOT EXISTS denylist_entries_lookup_idx ON denylist_entries(entry_type, entry_value, created_at);
