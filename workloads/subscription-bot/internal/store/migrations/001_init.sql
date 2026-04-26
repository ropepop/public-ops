PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  telegram_id INTEGER NOT NULL UNIQUE,
  username TEXT NOT NULL DEFAULT '',
  role TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS service_catalog (
  service_code TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  category TEXT NOT NULL,
  sharing_policy_note TEXT NOT NULL,
  access_mode TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plans (
  id TEXT PRIMARY KEY,
  owner_user_id INTEGER NOT NULL REFERENCES users(id),
  service_code TEXT NOT NULL REFERENCES service_catalog(service_code),
  total_price_minor INTEGER NOT NULL,
  per_seat_base_minor INTEGER NOT NULL,
  platform_fee_bps INTEGER NOT NULL,
  stable_asset TEXT NOT NULL,
  billing_period TEXT NOT NULL,
  renewal_date TEXT NOT NULL,
  seat_limit INTEGER NOT NULL,
  access_mode TEXT NOT NULL,
  sharing_policy_note TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plan_invites (
  id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL REFERENCES plans(id),
  invite_code TEXT NOT NULL UNIQUE,
  created_by_user_id INTEGER NOT NULL REFERENCES users(id),
  status TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS memberships (
  id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL REFERENCES plans(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  seat_status TEXT NOT NULL,
  joined_at TEXT NOT NULL,
  grace_until TEXT,
  removed_at TEXT,
  latest_invoice_id TEXT,
  UNIQUE(plan_id, user_id)
);

CREATE TABLE IF NOT EXISTS invoices (
  id TEXT PRIMARY KEY,
  membership_id TEXT NOT NULL REFERENCES memberships(id),
  plan_id TEXT NOT NULL REFERENCES plans(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  cycle_start TEXT NOT NULL,
  cycle_end TEXT NOT NULL,
  due_at TEXT NOT NULL,
  base_minor INTEGER NOT NULL,
  fee_minor INTEGER NOT NULL,
  total_minor INTEGER NOT NULL,
  credit_applied_minor INTEGER NOT NULL DEFAULT 0,
  paid_minor INTEGER NOT NULL DEFAULT 0,
  anchor_asset TEXT NOT NULL,
  pay_asset TEXT NOT NULL DEFAULT '',
  network TEXT NOT NULL DEFAULT '',
  quoted_pay_amount TEXT NOT NULL DEFAULT '',
  quote_rate_label TEXT NOT NULL DEFAULT '',
  quote_expires_at TEXT,
  payment_ref TEXT NOT NULL DEFAULT '',
  provider_invoice_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  tx_hash TEXT NOT NULL DEFAULT '',
  reminder_mask INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS invoices_plan_user_idx ON invoices(plan_id, user_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS invoices_cycle_idx ON invoices(membership_id, cycle_start, cycle_end);

CREATE TABLE IF NOT EXISTS payments (
  id TEXT PRIMARY KEY,
  invoice_id TEXT NOT NULL REFERENCES invoices(id),
  external_payment_id TEXT NOT NULL UNIQUE,
  amount_received INTEGER NOT NULL,
  asset TEXT NOT NULL,
  network TEXT NOT NULL,
  tx_hash TEXT NOT NULL,
  confirmations INTEGER NOT NULL DEFAULT 0,
  received_at TEXT NOT NULL,
  settlement_status TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS invoice_credits (
  id TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  plan_id TEXT NOT NULL REFERENCES plans(id),
  invoice_id TEXT NOT NULL DEFAULT '',
  amount_minor INTEGER NOT NULL,
  remaining_minor INTEGER NOT NULL,
  status TEXT NOT NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  applied_invoice_id TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS support_tickets (
  id TEXT PRIMARY KEY,
  plan_id TEXT NOT NULL DEFAULT '',
  user_id INTEGER NOT NULL REFERENCES users(id),
  subject TEXT NOT NULL,
  body TEXT NOT NULL,
  status TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  event_name TEXT NOT NULL,
  payload_json TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_quotes (
  provider_invoice_id TEXT PRIMARY KEY,
  invoice_id TEXT NOT NULL REFERENCES invoices(id),
  anchor_total_minor INTEGER NOT NULL,
  pay_asset TEXT NOT NULL,
  network TEXT NOT NULL,
  quote_amount_atomic TEXT NOT NULL,
  asset_price_minor INTEGER NOT NULL,
  atomic_factor TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sandbox_payments (
  id TEXT PRIMARY KEY,
  provider_invoice_id TEXT NOT NULL REFERENCES sandbox_quotes(provider_invoice_id),
  external_payment_id TEXT NOT NULL UNIQUE,
  amount_atomic TEXT NOT NULL,
  asset TEXT NOT NULL,
  network TEXT NOT NULL,
  tx_hash TEXT NOT NULL,
  confirmations INTEGER NOT NULL DEFAULT 1,
  settlement_status TEXT NOT NULL,
  received_at TEXT NOT NULL
);
