# Wallet Transfer Service — Problem Understanding & Technical Approach

## 1. Executive Summary

The objective of this assignment is to design and implement a production-grade, highly reliable, and transactional **Wallet Transfer Service** in Go. 

Financial and payment systems operate in distributed and concurrent environments where partial failures, network retries, and simultaneous transactions are the norm rather than the exception. A simple "read balance, subtract, write balance" approach fails catastrophically under concurrent access (double spending) or network retries (duplicate debits). 

This document defines the comprehensive problem analysis, domain models, database schema, concurrency controls, idempotency guarantees, layered software architecture, and testing strategy prior to code implementation.

---

## 2. Core Requirements & Challenges

### 2.1 Functional Requirements

1. **Transfer Execution (`POST /transfers`)**:
   - Transfer funds atomically between a source wallet (`fromWalletId`) and a destination wallet (`toWalletId`).
   - Validate input parameters: positive non-zero amount, distinct source and destination wallets, existence of both wallets.
   - Reject transfers if the source wallet has insufficient funds.

2. **Idempotent Request Handling**:
   - Client supplies an `idempotencyKey` in the request.
   - Re-submitting a request with the same `idempotencyKey` must return the original result without re-executing the transfer or duplicating side effects.
   - Supplying the same `idempotencyKey` with different transfer parameters must be detected as a conflict (`409 Conflict`).

3. **Double-Entry Ledger Recording**:
   - Every executed transfer must create exactly **two** immutable ledger entries within the same database transaction:
     - `DEBIT` entry for the source wallet.
     - `CREDIT` entry for the destination wallet.
   - The ledger must strictly balance: for every transfer, $\sum \text{Debit} = \sum \text{Credit}$.

4. **Wallet Balance Consistency**:
   - Balances must remain strictly correct and non-negative ($balance \ge 0$) under high concurrency.
   - Strategy: Stored balance in `wallets` table updated within the transaction for $O(1)$ balance queries and fast validation, backed by immutable `ledger_entries` for auditing and ledger reconciliation.

5. **Transfer State Machine**:
   - Allowed states: `PENDING`, `PROCESSED`, `FAILED`.
   - Transitions:
     - `PENDING -> PROCESSED` (on successful debit, credit, ledger write, and commit).
     - `PENDING -> FAILED` (on business validation failure such as insufficient funds).
   - State transitions must be guarded and deterministic.

---

## 3. Deep-Dive Design Strategies

### 3.1 Concurrency Control & Deadlock Prevention

#### The Race Condition (Double Spend)
If Wallet A has a balance of $100 and two concurrent requests attempt to transfer $80:
- Without concurrency control, both transactions read $balance = 100$, verify $100 \ge 80$, subtract $80$, and write $balance = 20$.
- Result: $160 transferred from a $100 account (double spend, invalid negative balance).

#### The Deadlock Hazard (Circular Wait)
If User 1 transfers from Wallet A to Wallet B while User 2 transfers from Wallet B to Wallet A concurrently:
- Transaction 1 locks Wallet A, then requests a lock on Wallet B.
- Transaction 2 locks Wallet B, then requests a lock on Wallet A.
- Result: **Deadlock** (circular dependency). Database aborts one or both transactions.

#### The Solution: Deterministic Row Lock Ordering
To eliminate both race conditions and deadlocks:
1. **Pessimistic Row-Level Locking**: Acquire row locks on both wallets using `SELECT ... FOR UPDATE` within a `READ COMMITTED` PostgreSQL transaction. This guarantees that modifications to the same wallets are serialized, preventing lost updates and race conditions without relying on optimistic retry loops.
2. **Deterministic Lock Ordering**: Always acquire locks on `min(fromWalletId, toWalletId)` first, followed by `max(fromWalletId, toWalletId)`.
   - Even if transfers occur in opposite directions ($A \to B$ and $B \to A$), both transactions lock in the identical order ($\min(A, B)$ then $\max(A, B)$).
   - By eliminating circular wait conditions (Coffman conditions), deadlocks become mathematically impossible.
3. **Database Check Constraint**: Add `CHECK (balance >= 0)` on the `wallets` table as a defense-in-depth guarantee against software bugs.

```
       Transfer 1 (A -> B)                 Transfer 2 (B -> A)
               │                                   │
               ▼                                   ▼
       Order: min(A, B) -> A               Order: min(A, B) -> A
               │                                   │
       Acquires Lock on A                  Waits for Lock on A
               │                                   │
       Acquires Lock on B                          ┆
               │                                   ┆
        Executes & Commits                         ┆
               │                                   ▼
        Releases Locks ───────────────>    Acquires Lock on A
                                                   │
                                           Acquires Lock on B
                                                   │
                                            Executes & Commits
```

---

### 3.2 Idempotency & Exactly-Once Semantics

To guarantee safe retries across network drops and client retransmissions:

1. **Idempotency Record Storage**:
   - An `idempotency_records` table tracks every request by `idempotency_key`.
   - Columns include:
     - `idempotency_key` (VARCHAR, PRIMARY KEY)
     - `request_hash` (SHA-256 digest of the canonical length-delimited representation: `len(from) + ":" + from + ":" + len(to) + ":" + to + ":" + amount`, eliminating delimiter-injection collisions such as `("a", "b:c")` vs `("a:b", "c")`)
     - `transfer_id` (UUID/TEXT, NULLABLE)
     - `status` (`IN_PROGRESS`, `COMPLETED`, `FAILED`)
     - `response_code` (INT)
     - `response_body` (TEXT)
     - `created_at`, `updated_at`

2. **Handling Scenarios & Short Committed Reservation Protocol**:
   - **Why Reservations Cannot Be Inside the Transfer Transaction**:
     - In PostgreSQL, if an `IN_PROGRESS` reservation is inserted inside the same transaction as the transfer, concurrent duplicate requests executing `INSERT ... ON CONFLICT` block on the unique-index tuple until the first transaction commits or rolls back.
     - When the first transaction commits, concurrent requests wake up and observe `COMPLETED`, completely bypassing the documented `IN_PROGRESS` / `409 Conflict` behavior and holding connection pool connections for the full transfer duration.
   - **The Short Committed Reservation Architecture**:
     1. **Key Validation & Resolution Precedence**:
        - Validate `idempotency_key` is not empty (returns `400 Bad Request` if missing).
        - Compute canonical `request_hash`.
        - Resolve the existing key against the fingerprint **before** running business-field validation. Reusing an existing key with altered parameters (even if invalid, e.g. `amount <= 0` or missing wallet) returns `409 Conflict` (`ErrIdempotencyConflict`), ensuring that idempotency conflict detection takes precedence over body validation.
     2. **Short Committed Reservation**:
        - Execute a standalone atomic `INSERT INTO idempotency_records ... ON CONFLICT DO NOTHING` committed immediately *outside* the transfer transaction.
        - Overlapping concurrent requests immediately observe the committed `IN_PROGRESS` status and return `409 Conflict` (`ErrIdempotencyInProgress`) without waiting or exhausting pool connections.
     3. **Atomic Stale-Owner Recovery**:
        - If an `IN_PROGRESS` record's `updated_at` is older than `stale_timeout` (e.g. 30 seconds, indicating a crashed node or abandoned request), a new request atomically reclaims ownership via compare-and-swap:
          ```sql
          UPDATE idempotency_records
          SET request_hash = $1, status = 'IN_PROGRESS', updated_at = $2
          WHERE idempotency_key = $3 AND status = 'IN_PROGRESS' AND updated_at <= $4
          RETURNING idempotency_key;
          ```
     4. **Deferred Cleanup on Unexpected Abort**:
        - If execution fails unexpectedly before reaching a terminal state (`COMPLETED` or `FAILED`), a deferred hook calls `DELETE FROM idempotency_records WHERE idempotency_key = $1 AND status = 'IN_PROGRESS'`, freeing the key for immediate retry.
     5. **Atomic Finalization**:
        - The transfer transaction updates the idempotency record to `COMPLETED` (HTTP 200/201) or `FAILED` (HTTP 422) atomically with wallet balances and ledger entries.
   - **Duplicate Arrival (Existing Key, Identical Payload)**:
     - If record status is `COMPLETED` or `FAILED`: immediately return the cached `response_code` and `response_body` with `is_replay: true`. Zero side effects triggered.
     - If record status is `IN_PROGRESS`: another request with the same key is currently running. Return `409 Conflict` (`ErrIdempotencyInProgress`) immediately.
   - **Key Collision (Existing Key, Different Payload)**:
     - If `request_hash` does not match the stored hash: return `409 Conflict` (`ErrIdempotencyConflict`) with message: `"idempotency key reused with different request payload"`.

---

### 3.3 Double-Entry Ledger Principles

The system adheres to standard financial accounting principles:
1. **Immutability**: Ledger entries are append-only. They are never updated or deleted.
2. **Balanced Transaction**: Every transfer produces exactly two records:
   - Debit: Source Wallet ID, Transfer ID, Type `DEBIT`, Amount.
   - Credit: Destination Wallet ID, Transfer ID, Type `CREDIT`, Amount.
3. **Auditability**:
   $$\text{Wallet Balance} = \sum \text{Credits} - \sum \text{Debits}$$
   A ledger verification utility ensures that the stored wallet balance strictly matches the calculated balance derived from all historic ledger entries.

---

## 4. Proposed Database Schema

The database design uses **PostgreSQL** as the primary relational database, leveraging native row-level locks (`SELECT ... FOR UPDATE`), transaction isolation, and ACID guarantees.

A `docker-compose.yml` configuration is provided with a persistent volume (`pgdata`), ensuring all database files are permanently stored on host storage across container stops, restarts, and machine reboots.

```sql
-- Wallets table
CREATE TABLE wallets (
    id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    balance BIGINT NOT NULL DEFAULT 0 CHECK (balance >= 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Transfers table
CREATE TABLE transfers (
    id VARCHAR(64) PRIMARY KEY,
    idempotency_key VARCHAR(128) NOT NULL UNIQUE,
    from_wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id),
    to_wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id),
    amount BIGINT NOT NULL CHECK (amount > 0),
    status VARCHAR(32) NOT NULL CHECK (status IN ('PENDING', 'PROCESSED', 'FAILED')),
    failure_reason TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Double-entry ledger entries table
CREATE TABLE ledger_entries (
    id VARCHAR(64) PRIMARY KEY,
    transfer_id VARCHAR(64) REFERENCES transfers(id) ON DELETE RESTRICT, -- Nullable for genesis/opening balance entries without an associated transfer
    wallet_id VARCHAR(64) NOT NULL REFERENCES wallets(id) ON DELETE RESTRICT,
    type VARCHAR(16) NOT NULL CHECK (type IN ('DEBIT', 'CREDIT')),
    amount BIGINT NOT NULL CHECK (amount > 0),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Idempotency tracking table
CREATE TABLE idempotency_records (
    idempotency_key VARCHAR(128) PRIMARY KEY,
    request_hash VARCHAR(64) NOT NULL,
    owner_token VARCHAR(64) NOT NULL DEFAULT '',
    transfer_id VARCHAR(64) REFERENCES transfers(id),
    status VARCHAR(32) NOT NULL CHECK (status IN ('IN_PROGRESS', 'COMPLETED', 'FAILED')),
    response_code INT,
    response_body TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for performance and integrity
CREATE INDEX idx_transfers_from_wallet ON transfers(from_wallet_id);
CREATE INDEX idx_transfers_to_wallet ON transfers(to_wallet_id);
CREATE INDEX idx_ledger_wallet ON ledger_entries(wallet_id);
CREATE INDEX idx_ledger_transfer ON ledger_entries(transfer_id);
CREATE UNIQUE INDEX idx_ledger_entries_transfer_type ON ledger_entries (transfer_id, type) WHERE transfer_id IS NOT NULL;
CREATE UNIQUE INDEX idx_ledger_entries_transfer_wallet ON ledger_entries (transfer_id, wallet_id) WHERE transfer_id IS NOT NULL;
```

> **Note on Money Representation**: All monetary values are represented as `BIGINT` representing minor currency units (e.g. cents) to avoid IEEE 754 floating-point rounding errors.
>
> **Note on Schema Naming & Consistency**: Table names (`wallets`, `transfers`, `ledger_entries`, `idempotency_records`) and foreign keys strictly align across DDL, domain models, and repositories. The primary key of `idempotency_records` is explicitly `idempotency_key` (avoiding reserved SQL keywords). In `ledger_entries`, `id` represents the unique entry identifier (`entry_id`).
>
> **Note on `updated_at` Semantics**: In PostgreSQL, `DEFAULT CURRENT_TIMESTAMP` sets the initial timestamp on `INSERT`, but PostgreSQL does not automatically update this column upon `UPDATE` statements without a trigger. To maintain explicit, auditable control and avoid database trigger side effects, the application layer explicitly updates `updated_at = time.Now().UTC()` in all `UPDATE` queries across repositories. Note that `ledger_entries` is append-only/immutable by accounting design, and therefore intentionally contains only `created_at` with no `updated_at`.
---

## 5. Clean Layered Architecture

The project will follow clean architecture principles, separating concerns cleanly across 4 layers:

```
┌─────────────────────────────────────────────────────────┐
│                      HTTP Handlers                      │
│   (Request decoding, JSON validation, HTTP responses)   │
└────────────────────────────┬────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────┐
│                     Service Layer                       │
│    (Business orchestration, idempotency logic, locks,   │
│       state transitions, balance checks, transactions)   │
└────────────────────────────┬────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────┐
│                    Repository Layer                     │
│  (Database access, SQL queries, transaction management) │
└────────────────────────────┬────────────────────────────┘
                             │
┌────────────────────────────▼────────────────────────────┐
│                      Domain Models                      │
│     (Core entities, Value Objects, Domain Errors)       │
└─────────────────────────────────────────────────────────┘
```

### Directory Structure
```text
wallet-transfer-assignment/
├── cmd/
│   └── api/
│       └── main.go                 # Application bootstrap & dependency injection
├── internal/
│   ├── domain/                     # Pure domain models and domain errors
│   │   ├── wallet.go
│   │   ├── transfer.go
│   │   ├── ledger.go
│   │   ├── idempotency.go
│   │   └── errors.go
│   ├── repository/                 # Data access interfaces and implementations
│   │   ├── repository.go           # Repository interfaces & TxManager
│   │   └── postgres/               # PostgreSQL implementation
│   │       ├── db.go
│   │       ├── wallet_repo.go
│   │       ├── transfer_repo.go
│   │       ├── ledger_repo.go
│   │       └── idempotency_repo.go
│   ├── service/                    # Application service & business logic
│   │   ├── transfer_service.go
│   │   ├── wallet_service.go
│   │   └── transfer_service_test.go
│   └── handler/                    # HTTP REST transport
│       ├── handler.go
│       ├── transfer_handler.go
│       ├── wallet_handler.go
│       └── router.go
├── pkg/
│   └── testutil/                   # Test fixtures, database helpers
├── go.mod
├── go.sum
└── problem_understanding.md
```

---

## 6. API Specifications

### 6.1 Create Transfer
- **Endpoint**: `POST /transfers`
- **Headers**: `Content-Type: application/json`
- **Request Body**:
```json
{
  "idempotencyKey": "tx-uuid-12345",
  "fromWalletId": "wallet_1",
  "toWalletId": "wallet_2",
  "amount": 100
}
```

- **Successful Response (`201 Created` or `200 OK`)**:
```json
{
  "transferId": "tr-abc-987",
  "idempotencyKey": "tx-uuid-12345",
  "fromWalletId": "wallet_1",
  "toWalletId": "wallet_2",
  "amount": 100,
  "status": "PROCESSED",
  "createdAt": "2026-09-10T17:30:00Z"
}
```

- **Error Responses**:
  - `400 Bad Request`: Invalid payload, missing fields, non-positive amount, identical from/to wallet.
  - `404 Not Found`: Source or destination wallet does not exist.
  - `409 Conflict`: Idempotency key currently in-flight, or idempotency key reused with mismatched request parameters.
  - `422 Unprocessable Entity`: Insufficient funds in source wallet.
  - `500 Internal Server Error`: Unhandled database or operational error.

### 6.2 Supplementary Endpoints (For Inspection & Testing)
- `POST /wallets`: Create wallet with initial balance.
- `GET /wallets/{id}`: Retrieve wallet info and current balance.
- `GET /transfers/{id}`: Retrieve transfer details.
- `GET /wallets/{id}/ledger`: Retrieve ledger history for audit verification.

---

## 7. Testing Discipline & Verification Strategy

Following the Red-Green-Refactor (TDD) development discipline and comprehensive testing requirements:
1. **Unit Testing**:
   - Domain entity validation (transfer rules, amount validations, state machine transitions).
   - Mock-based service testing for error branches and boundary conditions.

2. **Integration Testing**:
   - Real database operations testing repositories and transactional boundaries.
   - Idempotency replay testing:
     - Replaying exact same request -> identical response, no duplicate debit.
     - In-flight concurrent duplicate request -> rejected / 409 Conflict.
     - Payload mismatch with same key -> 409 Conflict.
   - Ledger integrity testing:
     - Verify two ledger rows exist for every transfer.
     - Verify sum of debits == sum of credits.
     - Verify wallet balance == sum of historical ledger entries.

3. **Concurrency & Race Condition Testing**:
   - **Double-Spend Stress Test**:
     - Wallet seeded with $100.
     - 10 concurrent goroutines attempt to transfer $20 each.
     - Exactly 5 must succeed and 5 must fail with insufficient funds. Final balance must be exactly $0.
   - **Deadlock Stress Test**:
     - 40 concurrent bidirectional transfers (20 from Wallet A to Wallet B and 20 from Wallet B to Wallet A simultaneously).
     - All must complete without deadlock errors, and net balances must match arithmetic expectations.

---

## 8. Summary of Engineering Tradeoffs

| Decision | Chosen Approach | Rationale |
| :--- | :--- | :--- |
| **Balance Strategy** | Stored balance + Immutable Double-Entry Ledger | $O(1)$ fast balance checks for transfers while retaining full non-repudiation and auditability through double-entry ledger. |
| **Concurrency Control** | Deterministic row locking (`min/max` ID order) | Eliminates race conditions and prevents deadlocks mathematically without resorting to coarse global mutexes. |
| **Money Representation**| Integer minor units (`BIGINT`) | Completely eliminates IEEE-754 floating point inaccuracies. |
| **Idempotency Strategy**| Dedicated idempotency table + payload hash | Durable across restarts, detects payload tampering/collisions, and preserves original response body. |
| **Database Engine** | PostgreSQL (with `jackc/pgx/v5`) | Industry-standard transactional engine supporting true row-level locks (`SELECT FOR UPDATE`), strict constraints, and connection pooling. Docker Compose provided for 1-command startup. |
