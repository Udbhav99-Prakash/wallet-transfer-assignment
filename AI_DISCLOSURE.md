# AI Usage Disclosure

This document outlines how Artificial Intelligence was utilized during the development, testing, and refinement of the **Wallet Transfer Service**, in compliance with the assignment instructions.

---

## 1. Tool Used

- **Primary Tool**: **Google Antigravity** (Advanced Agentic AI Pair-Programming System).
- **Underlying Model**: Advanced Agentic Reasoning Model.
- **Environment**: Windows 11, Go 1.23+, PostgreSQL 16 (`jackc/pgx/v5`).

---

## 2. How the Tool Was Used

Antigravity was employed as an active **pair programmer and systems design sounding board**, rather than an autonomous code generator. The engineering workflow followed a structured, iterative methodology:

### A. Pre-Implementation Problem Formulation & Architectural Design
- Prior to writing code, Antigravity was prompted to analyze `assignment.md` and produce a comprehensive technical specification (`problem_understanding.md`).
- We debated architectural tradeoffs:
  - **Ledger Model**: Choosing stored wallet balances with strict transactional double-entry ledger logging (`CREDIT` and `DEBIT` pairs) for $O(1)$ balance lookups alongside an atomic audit trail.
  - **Concurrency & Deadlock Prevention**: Determining deterministic lock acquisition (`wallet_a < wallet_b`) to prevent cyclic waits during bidirectional concurrent transfers.
  - **Idempotency Lifecycle**: Formulating a two-phase protocol (short committed reservation with stale-owner recovery) to avoid holding transaction locks across client retries.

### B. Human-in-the-Loop Steering on Infrastructure & Persistence
- When selecting the database setup, the user explicitly decided to use a native, persistent PostgreSQL 16 service on Windows rather than an ephemeral container, ensuring local data persistence and realistic database connection pool behavior.
- In CI, containerized PostgreSQL 16 was configured via Docker Compose to guarantee mandatory, automated testing without silent test skips.

### C. Systematic PR Review Triage & Invariant Hardening
- Following a peer review of PR #169 containing 17 technical comments, Antigravity was used to methodically triage, diagnose, and remediate every reported issue:
  - Eliminating preimage delimiter collisions via length-delimited hashing (`len:val:len:val:amount`).
  - Preventing information leakage by sanitizing generic HTTP 500 error messages while logging details server-side.
  - Preserving original HTTP status codes (201 Created vs 422 Unprocessable Entity) on idempotency replays.
  - Distinguishing 404 Not Found vs 409 Conflict vs 500 Internal Error across handlers.
  - Protecting against arithmetic overflow via `CanCredit` checking `math.MaxInt64`.
  - Ensuring single-source schema migrations using Go 1.16+ `//go:embed`.
  - Eliminating false reconciliation mismatches under live concurrency via `SELECT ... FOR UPDATE` row locks.

### D. Test-Driven Concurrency & Stress Verification
- Antigravity was used to construct rigorous concurrency regression tests:
  - 20 concurrent bidirectional transfers in each direction (40 total) between two wallets to verify zero deadlocks.
  - 10 concurrent debits against a shared wallet to verify zero double-spending (matching `TestTransferService_ConcurrentDebits_NoDoubleSpend`).
  - Concurrent background transfers executed during active balance reconciliation to verify zero false mismatches.
- All tests were executed against a real PostgreSQL 16 instance with bounded contexts to eliminate hanging test suites.

---

## 3. Session Transcript & Prompt Records

The complete transcript of all 53 interaction turns—including exact prompt text, tool invocations, and AI responses—is preserved in this repository:
- **Readable Session Transcript**: [`AI_TRANSCRIPT.md`](./AI_TRANSCRIPT.md)
- **Raw Agent Interaction Log**: Persisted in the session metadata logs.

---

## 4. Chronological List of All Prompts

Below is the complete chronological log of all 53 explicit prompts provided during the development session:

| # | Timestamp (UTC) | Phase | User Prompt |
|---|---|---|---|
| **1** | `2026-09-10 17:20:44` | Architecture | *from assignment.md file outline your understanding and approach before implementing the full solution in problem_understanding.md file* |
| **2** | `2026-09-10 18:46:53` | Architecture | *i will use postgresql* |
| **3** | `2026-09-10 18:50:16` | Architecture | *can you explain the changes first* |
| **4** | `2026-09-10 18:50:53` | Architecture | *\| \*\*Database Engine\*\* \| PostgreSQL (with `jackc/pgx/v5`) \| Industry-standard transactional engine supporting true row-level locks (`SELECT FOR UPDATE`), strict constraints, and connection pooling. Docker Compose provided for 1-command startup. explain this* |
| **5** | `2026-09-10 18:52:53` | Infrastructure | *i don't want to use docker for database because data will be not persistent* |
| **6** | `2026-09-10 18:57:05` | Infrastructure | *how to check its installed or not* |
| **7** | `2026-09-10 18:58:33` | Infrastructure | *2nd option* |
| **8** | `2026-09-10 19:00:45` | Git & Commit | *give me an appropriate commit message for this* |
| **9** | `2026-09-10 19:05:36` | Scaffolding | *now set up the project as given in assignment.md* |
| **10** | `2026-09-10 20:28:47` | Review Fixes | *problem_understanding.md:176 Using a column named key in the proposed idempotency_records schema is inconsistent with the rest of the document (which uses idempotency_key) and tends to require awkward quoting in SQL; naming it idempotency_key improves clarity and consistency.* |
| **11** | `2026-09-10 20:43:46` | Review Fixes | *The documentation includes a few concrete technical inaccuracies/inconsistencies (PostgreSQL transaction wording, idempotency hash definition, schema naming, and updated_at semantics) that should be corrected to avoid guiding the implementation incorrectly.* |
| **12** | `2026-09-11 17:40:46` | Review Fixes | *PostgreSQL doesn't have an "immediate" transaction mode; describing a "serializable/immediate transaction" is inaccurate and could mislead the implementation approach.* |
| **13** | `2026-09-11 18:19:10` | Review Fixes | *PostgreSQL doesn't have an "immediate" transaction mode; describing a "serializable/immediate transaction" is inaccurate and could mislead the implementation approach.* |
| **14** | `2026-09-11 18:35:54` | Review Fixes | *internal/domain/transfer.go:42 The domain validation reports a missing idempotency key as ErrInvalidAmount, whose message says the amount is invalid. Any caller using Transfer.Validate would return the wrong failure reason for a missing key; add/use a dedicated invalid-idempotency-key error instead.* |
| **15** | `2026-09-11 18:42:35` | Review Fixes | *internal/handler/wallet_handler.go:51 This 500 response forwards the repository error text directly to clients, potentially exposing database details. Keep the error in server-side logs and return a generic internal-error response.* |
| **16** | `2026-09-11 18:44:08` | Review Fixes | *internal/handler/wallet_handler.go:72 & :93 This 500 response forwards the repository error text directly to clients, potentially exposing database details. Keep the error in server-side logs and return a generic internal-error response.* |
| **17** | `2026-09-11 18:46:12` | Review Fixes | *internal/repository/postgres/idempotency_repo.go:33 The idempotency reservation is inserted in the same transaction as the entire transfer, so an overlapping request cannot see IN_PROGRESS: it waits on the unique-index conflict until the first transaction commits, then replays... use a short committed reservation/lock protocol with explicit stale-owner recovery if immediate rejection is required.* |
| **18** | `2026-09-11 18:52:03` | Review Fixes | *internal/service/transfer_service.go:45 The idempotency record is consulted only after validating the full request. Reusing an existing key with a changed but invalid amount (or missing wallet ID) returns 400/404 instead of the documented 409 payload-conflict response... Resolve an existing key against a canonical request fingerprint before business-field validation...* |
| **19** | `2026-09-11 18:55:03` | Review Fixes | *internal/service/transfer_service.go:82 The documented IN_PROGRESS/409 path is not observable here: the idempotency row is inserted and finalized inside the same transaction... either separate/commit the reservation with crash recovery or document blocking/replay semantics.* |
| **20** | `2026-09-11 18:57:09` | Review Fixes | *internal/service/transfer_service.go:36 These validation errors map to incorrect HTTP results: an empty idempotency key is an untyped error that the handler returns as 500, while missing wallet IDs are returned as ErrWalletNotFound and become 404... use dedicated validation errors and map them accordingly.* |
| **21** | `2026-09-11 19:00:00` | Review Fixes | *internal/service/transfer_service_test.go:329 This concurrency test also waits on all database calls with context.Background() and no deadline. A regression that blocks a wallet row lock will make the test process hang indefinitely rather than report a failed double-spend test; bound the request context and wait with a timeout.* |
| **22** | `2026-09-11 19:02:18` | Review Fixes | *migrations/000001_init_schema.sql:1 postgres.Migrate embeds and executes internal/repository/postgres/schema.sql; this migration file is never applied by the application... Use one migration source or make startup execute this migration set.* |
| **23** | `2026-09-11 19:06:17` | Review Fixes | *In [cmd/api/main.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031441): databaseUrl logged containing credentials in plaintext.* |
| **24** | `2026-09-11 19:08:33` | Review Fixes | *In [internal/domain/wallet.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031464): Credit arithmetic can overflow MaxInt64 without boundary check.* |
| **25** | `2026-09-11 19:11:43` | Review Fixes | *In [internal/repository/postgres/schema.sql](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031495): Seeded system treasury lacks corresponding double-entry opening ledger record.* |
| **26** | `2026-09-11 19:18:42` | Review Fixes | *In [internal/service/transfer_service.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031518): UpdateTransferStatus error ignored on failure branch.* |
| **27** | `2026-09-11 19:22:37` | Review Fixes | *In [internal/service/transfer_service.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031535): UpdateIdempotency error unhandled during completion path.* |
| **28** | `2026-09-11 19:27:21` | Review Fixes | *In [internal/service/transfer_service.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031551): Currency compatibility between wallets is unvalidated.* |
| **29** | `2026-09-11 19:33:22` | Review Fixes | *In [internal/service/wallet_service.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031562): Wallet creation and initial funding transfer are split across separate transactions.* |
| **30** | `2026-09-11 19:37:17` | Review Fixes | *In [internal/service/wallet_service.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031572): system_treasury stored balance is not debited during funded wallet creation.* |
| **31** | `2026-09-11 19:39:11` | Review Fixes | *In [internal/domain/idempotency.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031576): Preimage hash format ambiguous when wallet IDs contain ":" delimiter.* |
| **32** | `2026-09-11 19:42:33` | Review Fixes | *In [internal/handler/transfer_handler.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031619): Fallback error exposes raw database details to clients.* |
| **33** | `2026-09-11 19:45:36` | Review Fixes | *In [internal/handler/transfer_handler.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031651): Replayed idempotent requests force 200 OK instead of preserving original HTTP status code.* |
| **34** | `2026-09-11 19:49:16` | Review Fixes | *In [internal/handler/transfer_handler.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031678): GetTransfer maps all database errors to 404 Not Found.* |
| **35** | `2026-09-11 19:51:37` | Review Fixes | *In [internal/handler/wallet_handler.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031706): CreateWallet maps unique-constraint failures and DB errors to 400 Bad Request.* |
| **36** | `2026-09-11 19:55:06` | Review Fixes | *In [internal/service/wallet_service.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031730): Balance reconciliation reads wallet and ledger in separate uncommitted queries, causing false mismatches.* |
| **37** | `2026-09-11 19:57:45` | Review Fixes | *In [pkg/testutil/db.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031758): Test helper skips integration tests when PostgreSQL is missing.* |
| **38** | `2026-09-11 19:59:45` | Review Fixes | *In [internal/repository/postgres/db.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031795): Dual schema sources (embedded schema.sql vs migrations directory).* |
| **39** | `2026-09-11 20:01:21` | Review Fixes | *In [internal/service/transfer_service_test.go](https://github.com/Robustrade/wallet-transfer-assignment/pull/169#discussion_r3992031836): Deadlock regression test waits indefinitely without timeout.* |
| **40** | `2026-09-11 21:09:56` | Verification | *have we coveed everthing?* |
| **41** | `2026-09-11 21:14:11` | Verification | *before there were so many errors so now is it all ok ?* |
| **42** | `2026-09-11 21:15:55` | AI Disclosure | *AI disclosure: Detail how you used AI to help with your submission...* |
| **43** | `2026-09-12 02:52:54` | Git & Commit | *give me proper commit message* |
| **44** | `2026-09-12 02:54:56` | Git & Commit | *sure* |
| **45** | `2026-09-12 03:16:08` | Review Fixes | *again so many comments* |
| **46** | `2026-09-12 03:16:28` | Review Fixes | *[Follow-up Review Feedback on PR #169 covering CI fallback, owner-token lease fencing, key length validation, barrier race test, funding key namespace, test advisory lock timing, and documentation alignment]* |
| **47** | `2026-09-11 21:55:00` | Review Fixes | *verification and test validation* |
| **48** | `2026-09-11 21:56:04` | Git & Commit | *commit* |
| **49** | `2026-09-11 22:09:51` | Review Fixes | *[Follow-up Review Feedback on PR #169 covering lease heartbeats, database-level ledger constraints, migration version tracking, isolated test databases, terminal failure persistence, and test timeouts]* |
| **50** | `2026-09-11 22:10:42` | Review Fixes | *execution, verification, and disclosure update* |
| **51** | `2026-09-11 22:30:15` | Git & Commit | *push* |
| **52** | `2026-09-11 22:35:10` | Review Fixes | *[Follow-up Review Feedback on PR #169 covering atomic multi-row ledger inserts, in-flight heartbeat lease cancellation, strict migration version lookup error handling, migration runner advisory locks, transfers table constraint triggers, and ledger immutability triggers]* |
| **53** | `2026-09-11 22:41:41` | Git & Commit | *push the changes* |

---

## 5. Candidate Preparedness for PR Discussion

The author is fully prepared to explain and defend every design decision and line of code in the implementation during the technical discussion:

1. **Short-Committed Reservation Protocol & Owner-Token Lease Fencing**:
   - Why we insert `IN_PROGRESS` in an immediate short transaction rather than keeping an uncommitted lock open during the entire transfer: under burst retries, overlapping requests immediately receive `409 Conflict` without tying up database connection pool workers.
   - Stale-owner recovery & fencing: if a worker process crashes while holding an `IN_PROGRESS` reservation, reservations older than 30 seconds are reclaimed safely. To prevent slow/paused workers from corrupting or deleting state after a lease reclaim, every reservation and reclaim generates an `owner_token`. Updates and deferred cleanups are fenced by `owner_token`.
   - Lease Heartbeats & Active In-Flight Cancellation: while a transfer is actively running, a background heartbeat periodically refreshes `updated_at` every 5 seconds so live requests are never reclaimed prematurely. If heartbeat encounters `ErrIdempotencyLeaseLost`, it actively cancels the in-flight context (`cancelTx()`), instantly releasing row locks to prevent duplicate work or contention with the replacement owner.
2. **Deterministic Deadlock Prevention**:
   - Why locking ordering is sorted lexicographically (`from_id < to_id ? (from, to) : (to, from)`): breaks the circular wait condition (Coffman condition) across concurrent bidirectional transfers.
   - Initial wallet funding locks `system_treasury` before inserting the user wallet to adhere strictly to the global lock hierarchy.
3. **Double-Entry Ledger Integrity & Database-Level Constraints**:
   - Stored wallet balances are updated atomically alongside two ledger entries (`DEBIT` and `CREDIT`) within a single ACID transaction.
   - Multi-row atomic SQL inserts (`INSERT INTO ledger_entries (...) VALUES (...), (...)`) guarantee statement-level atomicity even when invoked against connection pools without an explicit outer transaction.
   - Database-level composite unique indexes (`(transfer_id, type)` and `(transfer_id, wallet_id)`), together with commit-deferred PostgreSQL constraint triggers (`trg_check_ledger_pair` on `ledger_entries` and `trg_check_transfer_processed` on `transfers`), guarantee that no transfer can commit with status `PROCESSED` without exactly one matching DEBIT on `from_wallet_id` and one CREDIT on `to_wallet_id` equal to the transfer amount.
   - Immutability trigger (`trg_prevent_ledger_mutation`) strictly forbids any `UPDATE` or `DELETE` on `ledger_entries`, preserving the immutable financial audit log.
   - `ReconcileBalance` performs `SELECT ... FOR UPDATE` row locking on the wallet inside a transaction while aggregating ledger records, eliminating false concurrency mismatches.
4. **Length-Delimited Preimage Hashing**:
   - Why `%d:%s:%d:%s:%d` prevents delimiter injection attacks across colon characters.
5. **HTTP Status Code Fidelity on Replays**:
   - Caching `response_code` in `idempotency_records` so that a replayed successful transfer returns `201 Created` while a replayed overdraft returns `422 Unprocessable Entity`.
6. **Applied Migration Version Tracking & Advisory Locking**:
   - Schema migrations are recorded in a `schema_migrations (version, applied_at)` table inside an atomic transaction, protected by a dedicated PostgreSQL advisory lock (`SELECT pg_advisory_lock(888999)`), guaranteeing forward-only, idempotent migration execution across concurrent service instances. Strict `pgx.ErrNoRows` inspection ensures transient DB failures are never treated as unapplied migrations.
7. **Test Database Isolation**:
   - Automated tests run against a dedicated `wallet_test_db`, isolated from application development databases.


