-- PostgreSQL Schema for Wallet Transfer Service

CREATE TABLE IF NOT EXISTS wallets (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS transfers (
    id VARCHAR(64) PRIMARY KEY,
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    from_wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id),
    to_wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id),
    amount BIGINT NOT NULL CHECK (amount > 0),
    status VARCHAR(32) NOT NULL CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED')),
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT check_distinct_wallets CHECK (from_wallet_id <> to_wallet_id)
);

CREATE TABLE IF NOT EXISTS ledger_entries (
    id VARCHAR(64) PRIMARY KEY,
    transfer_id VARCHAR(64) NOT NULL REFERENCES transfers(id) ON DELETE RESTRICT,
    wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    type VARCHAR(16) NOT NULL CHECK (type IN ('DEBIT', 'CREDIT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS idempotency_records (
    idempotency_key VARCHAR(128) PRIMARY KEY,
    request_hash VARCHAR(64) NOT NULL,
    transfer_id VARCHAR(64) REFERENCES transfers(id),
    status VARCHAR(32) NOT NULL CHECK (status IN ('IN_PROGRESS', 'COMPLETED', 'FAILED')),
    response_code INT,
    response_body TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_transfers_from_wallet ON transfers(from_wallet_id);
CREATE INDEX IF NOT EXISTS idx_transfers_to_wallet ON transfers(to_wallet_id);
CREATE INDEX IF NOT EXISTS idx_ledger_wallet ON ledger_entries(wallet_id);
CREATE INDEX IF NOT EXISTS idx_ledger_transfer ON ledger_entries(transfer_id);

-- System Treasury wallet for double-entry initial deposits
INSERT INTO wallets (id, name, balance, currency)
VALUES ('system_treasury', 'System Treasury', 100000000000000, 'USD')
ON CONFLICT (id) DO NOTHING;


