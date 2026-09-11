-- Migration 000001: Initial schema for Wallet Transfer Service

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
    transfer_id VARCHAR(64) REFERENCES transfers(id) ON DELETE RESTRICT,
    wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    type VARCHAR(16) NOT NULL CHECK (type IN ('DEBIT', 'CREDIT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE ledger_entries ALTER COLUMN transfer_id DROP NOT NULL;

CREATE TABLE IF NOT EXISTS idempotency_records (
    idempotency_key VARCHAR(128) PRIMARY KEY,
    request_hash VARCHAR(64) NOT NULL,
    owner_token VARCHAR(64) NOT NULL DEFAULT '',
    transfer_id VARCHAR(64) REFERENCES transfers(id),
    status VARCHAR(32) NOT NULL CHECK (status IN ('IN_PROGRESS', 'COMPLETED', 'FAILED')),
    response_code INT,
    response_body TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
ALTER TABLE idempotency_records ADD COLUMN IF NOT EXISTS owner_token VARCHAR(64) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_transfers_from_wallet ON transfers(from_wallet_id);
CREATE INDEX IF NOT EXISTS idx_transfers_to_wallet ON transfers(to_wallet_id);
CREATE INDEX IF NOT EXISTS idx_ledger_wallet ON ledger_entries(wallet_id);
CREATE INDEX IF NOT EXISTS idx_ledger_transfer ON ledger_entries(transfer_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_entries_transfer_type ON ledger_entries (transfer_id, type) WHERE transfer_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_ledger_entries_transfer_wallet ON ledger_entries (transfer_id, wallet_id) WHERE transfer_id IS NOT NULL;

-- System Treasury wallet for double-entry initial deposits
INSERT INTO wallets (id, name, balance, currency)
VALUES ('system_treasury', 'System Treasury', 100000000000000, 'USD')
ON CONFLICT (id) DO NOTHING;

-- Ledger opening funding record for system_treasury so stored balance reconciles with ledger
INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount)
VALUES ('entry_system_treasury_opening', NULL, 'system_treasury', 'CREDIT', 100000000000000)
ON CONFLICT (id) DO NOTHING;

-- Database-level constraint trigger enforcing that every transfer has exactly two balanced entries:
-- exactly one DEBIT on from_wallet_id and one CREDIT on to_wallet_id, matching transfer amount.
CREATE OR REPLACE FUNCTION check_ledger_pair_integrity()
RETURNS TRIGGER AS $$
DECLARE
    transfer_rec RECORD;
    debit_count INT;
    credit_count INT;
    debit_sum BIGINT;
    credit_sum BIGINT;
BEGIN
    IF NEW.transfer_id IS NOT NULL THEN
        SELECT * INTO transfer_rec FROM transfers WHERE id = NEW.transfer_id;
        IF FOUND THEN
            SELECT COUNT(*), COALESCE(SUM(amount), 0) INTO debit_count, debit_sum
            FROM ledger_entries
            WHERE transfer_id = NEW.transfer_id AND type = 'DEBIT' AND wallet_id = transfer_rec.from_wallet_id;

            SELECT COUNT(*), COALESCE(SUM(amount), 0) INTO credit_count, credit_sum
            FROM ledger_entries
            WHERE transfer_id = NEW.transfer_id AND type = 'CREDIT' AND wallet_id = transfer_rec.to_wallet_id;

            IF debit_count <> 1 OR credit_count <> 1 OR debit_sum <> transfer_rec.amount OR credit_sum <> transfer_rec.amount THEN
                RAISE EXCEPTION 'invalid ledger pair for transfer %: must have exactly 1 DEBIT on from_wallet and 1 CREDIT on to_wallet matching transfer amount', NEW.transfer_id;
            END IF;
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_check_ledger_pair ON ledger_entries;
CREATE CONSTRAINT TRIGGER trg_check_ledger_pair
AFTER INSERT OR UPDATE ON ledger_entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW
EXECUTE FUNCTION check_ledger_pair_integrity();



