package migrations_test

import (
	"context"
	"testing"

	"wallet-transfer-assignment/migrations"
	"wallet-transfer-assignment/pkg/testutil"
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

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Migration with canceled context should fail immediately
	err := migrations.Migrate(canceledCtx, pool)
	if err == nil {
		t.Fatalf("expected error with canceled context, got nil")
	}

	// Subsequent migration must acquire lock and succeed, proving lock was not leaked to the pool
	if err := migrations.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("subsequent migration failed, advisory lock was likely leaked: %v", err)
	}
}
