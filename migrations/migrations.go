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

// Migrate executes all embedded migration scripts in lexical order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
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
		content, err := FS.ReadFile(file)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", file, err)
		}
		if _, err := pool.Exec(ctx, string(content)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", file, err)
		}
	}

	return nil
}
