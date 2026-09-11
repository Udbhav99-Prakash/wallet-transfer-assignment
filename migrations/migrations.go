package migrations

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FS embeds all SQL migration files in this directory.
//
//go:embed *.sql
var FS embed.FS

// Migrate executes all embedded migration scripts in lexical order with atomic version tracking.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Create schema_migrations table if not exists to track applied versions
	createTableQuery := `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := pool.Exec(ctx, createTableQuery); err != nil {
		return fmt.Errorf("failed to initialize schema_migrations table: %w", err)
	}

	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	var sqlFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			sqlFiles = append(sqlFiles, entry.Name())
		}
	}
	sort.Strings(sqlFiles)

	for _, file := range sqlFiles {
		// Check if this migration has already been applied
		var appliedVersion string
		err := pool.QueryRow(ctx, "SELECT version FROM schema_migrations WHERE version = $1", file).Scan(&appliedVersion)
		if err == nil {
			// Already applied; skip safely
			continue
		}

		content, err := FS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}

		// Execute migration and record version inside an atomic transaction
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("failed to begin transaction for migration %s: %w", file, err)
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("failed to execute migration %s: %w", file, err)
		}

		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", file); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("failed to record applied migration %s: %w", file, err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit migration %s: %w", file, err)
		}
	}

	return nil
}
