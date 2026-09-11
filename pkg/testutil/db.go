package testutil

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"wallet-transfer-assignment/internal/repository/postgres"
)

// SetupTestDB initializes connection to the PostgreSQL test instance, runs migrations, and registers cleanup.
func SetupTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgrespassword@localhost:5432/wallet_db?sslmode=disable"
	}

	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, dbURL)
	if err != nil {
		t.Fatalf("failed to connect to PostgreSQL at %s: %v. Integration tests require a running PostgreSQL instance.", dbURL, err)
		return nil
	}

	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}

	// Acquire PostgreSQL advisory lock on a dedicated connection to safely serialize
	// test packages when 'go test ./...' is executed without -p 1.
	lockConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire connection for test advisory lock: %v", err)
	}
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock(777888);"); err != nil {
		lockConn.Release()
		t.Fatalf("failed to acquire pg_advisory_lock: %v", err)
	}

	// Truncate all tables to guarantee test isolation
	cleanupQuery := `
		TRUNCATE TABLE ledger_entries, transfers, idempotency_records, wallets CASCADE;
		INSERT INTO wallets (id, name, balance, currency)
		VALUES ('system_treasury', 'System Treasury', 100000000000000, 'USD')
		ON CONFLICT (id) DO NOTHING;
		INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount)
		VALUES ('entry_system_treasury_opening', NULL, 'system_treasury', 'CREDIT', 100000000000000)
		ON CONFLICT (id) DO NOTHING;
	`
	if _, err := pool.Exec(ctx, cleanupQuery); err != nil {
		_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(777888);")
		lockConn.Release()
		t.Fatalf("failed to clean test tables: %v", err)
	}

	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.Background(), "SELECT pg_advisory_unlock(777888);")
		lockConn.Release()
		pool.Close()
	})

	return pool
}
