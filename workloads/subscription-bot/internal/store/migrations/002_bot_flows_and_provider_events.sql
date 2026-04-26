CREATE TABLE IF NOT EXISTS bot_conversation_states (
  user_id INTEGER PRIMARY KEY REFERENCES users(id),
  telegram_id INTEGER NOT NULL UNIQUE,
  flow TEXT NOT NULL,
  step TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  expires_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS bot_conversation_states_expires_idx ON bot_conversation_states(expires_at);

CREATE TABLE IF NOT EXISTS provider_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_name TEXT NOT NULL,
  external_event_id TEXT NOT NULL,
  event_type TEXT NOT NULL,
  provider_invoice_id TEXT NOT NULL DEFAULT '',
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL,
  UNIQUE(provider_name, external_event_id)
);

CREATE INDEX IF NOT EXISTS provider_events_provider_invoice_idx ON provider_events(provider_name, provider_invoice_id, created_at);

CREATE TABLE IF NOT EXISTS nowpayments_quotes (
  provider_invoice_id TEXT PRIMARY KEY,
  invoice_id TEXT NOT NULL REFERENCES invoices(id),
  anchor_total_minor INTEGER NOT NULL,
  pay_asset TEXT NOT NULL,
  network TEXT NOT NULL,
  quote_amount_atomic TEXT NOT NULL,
  payment_ref TEXT NOT NULL DEFAULT '',
  pay_address TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  expires_at TEXT
);
