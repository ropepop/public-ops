CREATE TABLE IF NOT EXISTS report_dedupe_claims (
  report_kind TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  scope_key TEXT NOT NULL,
  last_report_at TEXT NOT NULL,
  PRIMARY KEY(report_kind, user_id, scope_key)
);

CREATE INDEX IF NOT EXISTS report_dedupe_claims_last_report_idx
ON report_dedupe_claims(last_report_at);
