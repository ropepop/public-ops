CREATE TABLE IF NOT EXISTS undo_checkouts (
    user_id INTEGER PRIMARY KEY,
    train_instance_id TEXT NOT NULL,
    boarding_station_id TEXT,
    checked_in_at TEXT NOT NULL,
    auto_checkout_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_undo_checkouts_expires_at
    ON undo_checkouts(expires_at);
