-- LiquidityPub SQLite Schema v0.1

CREATE TABLE IF NOT EXISTS node_config (
    key TEXT PRIMARY KEY,
    value TEXT
);
-- Keys: name, currency_name, currency_symbol, description,
--       public_key, private_key,
--       issuance_type (periodic|demurrage|manual),
--       issuance_amount, issuance_interval_hours,
--       admin_password_hash, installed_at, last_issuance_run

CREATE TABLE IF NOT EXISTS members (
    id INTEGER PRIMARY KEY,
    username TEXT UNIQUE NOT NULL,
    display_name TEXT,
    password_hash TEXT NOT NULL,
    joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_dividend_at DATETIME,
    active INTEGER DEFAULT 1
);

CREATE TABLE IF NOT EXISTS accounts (
    id INTEGER PRIMARY KEY,
    member_id INTEGER,          -- NULL for system accounts
    account_type TEXT NOT NULL, -- 'wallet', 'issuance_sink', 'exchange'
    label TEXT
);

CREATE TABLE IF NOT EXISTS transactions (
    id INTEGER PRIMARY KEY,
    description TEXT,
    type TEXT NOT NULL,         -- 'issuance', 'payment', 'exchange'
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    reference TEXT              -- optional external ref (federation)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id INTEGER PRIMARY KEY,
    transaction_id INTEGER NOT NULL REFERENCES transactions(id),
    account_id INTEGER NOT NULL REFERENCES accounts(id),
    -- Positive = money flowing INTO this account (credit to wallet)
    -- Negative = money flowing OUT of this account (debit from wallet)
    amount INTEGER NOT NULL     -- stored as micro-units (millicents) to avoid float
);

CREATE TABLE IF NOT EXISTS federated_nodes (
    id INTEGER PRIMARY KEY,
    node_url TEXT UNIQUE NOT NULL,
    node_name TEXT,
    currency_name TEXT,
    trust_level INTEGER DEFAULT 50, -- 0-100
    public_key TEXT,
    last_seen DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
