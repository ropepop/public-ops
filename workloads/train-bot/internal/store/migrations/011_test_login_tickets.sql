CREATE TABLE IF NOT EXISTS test_login_tickets (
    nonce_hash TEXT PRIMARY KEY,
    user_id INTEGER NOT NULL,
    expires_at TEXT NOT NULL,
    consumed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_test_login_tickets_expires_at
    ON test_login_tickets(expires_at);
