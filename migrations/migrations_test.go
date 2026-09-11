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
