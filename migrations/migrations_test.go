package migrations_test

import (
	"context"
	"testing"
	"time"

	"wallet-transfer-assignment/migrations"
	"wallet-transfer-assignment/pkg/testutil"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrations_Apply(t *testing.T) {
	pool := testutil.SetupTestDB(t)

	// Verify migrations apply cleanly and idempotently on an existing pool
	if err := migrations.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("failed to apply migrations: %v", err)
	}
}

func TestMigrations_CanceledContextReleasesAdvisoryLock(t *testing.T) {
	pool := testutil.SetupTestDB(t)
	ctx := context.Background()

	const migrationAdvisoryLockID = 888999

	// 1. Acquire connection 1 and acquire advisory lock 888999, creating an intentional lock contention
	blockerConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("failed to acquire blocker connection: %v", err)
	}
	defer blockerConn.Release()

	if _, err := blockerConn.Exec(ctx, "SELECT pg_advisory_lock($1);", migrationAdvisoryLockID); err != nil {
		t.Fatalf("failed to acquire blocker advisory lock: %v", err)
	}

	// 2. Launch Migrate with a short timeout context.
	// Migrate acquires a connection from the pool, attempts pg_advisory_lock(888999),
	// blocks because blockerConn holds it, and times out while waiting.
	timeoutCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()

	migrateErr := migrations.Migrate(timeoutCtx, pool)
	if migrateErr == nil {
		t.Fatalf("expected Migrate to fail due to context deadline exceeded, got nil")
	}

	// 3. Release the blocker advisory lock on connection 1
	if _, err := blockerConn.Exec(context.Background(), "SELECT pg_advisory_unlock($1);", migrationAdvisoryLockID); err != nil {
		t.Fatalf("failed to release blocker advisory lock: %v", err)
	}

	// 4. Bound subsequent migration with a timeout.
	// If the timed-out Migrate leaked lock 888999 or its connection back to the pool in a locked state,
	// this call would block indefinitely until the timeout expires.
	followUpCtx, followUpCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer followUpCancel()

	if err := migrations.Migrate(followUpCtx, pool); err != nil {
		t.Fatalf("subsequent migration failed; advisory lock was likely leaked to the pool: %v", err)
	}
}

func TestMigrations_SucceedsOnSingleConnectionPool(t *testing.T) {
	basePool := testutil.SetupTestDB(t)
	cfg := basePool.Config().Copy()
	cfg.MaxConns = 1
	cfg.MinConns = 1

	singleConnPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create single-connection pool: %v", err)
	}
	defer singleConnPool.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := migrations.Migrate(ctx, singleConnPool); err != nil {
		t.Fatalf("Migrate failed on a pool with MaxConns=1: %v", err)
	}
}
