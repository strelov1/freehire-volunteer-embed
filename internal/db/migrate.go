package db

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// ApplyMigrations applies this service's own schema migrations — the tables
// it owns (volunteer_submissions, volunteer_reputation, volunteer_leases) —
// tracking progress in its own version table so it never touches hire's
// migration bookkeeping. Safe to call repeatedly; already-applied files are
// skipped.
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	// Postgres checks CREATE privilege on the schema before checking IF NOT
	// EXISTS, so a role granted only SELECT/INSERT on this table (production's
	// least-privilege role, once the table has been created by hand) would
	// fail here on every restart even though there is nothing to create.
	var migrationsTableExists bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass('public.volunteer_embed_schema_migrations') IS NOT NULL`,
	).Scan(&migrationsTableExists); err != nil {
		return fmt.Errorf("db: check migrations table: %w", err)
	}
	if !migrationsTableExists {
		if _, err := pool.Exec(ctx, `
			CREATE TABLE volunteer_embed_schema_migrations (
				filename text PRIMARY KEY,
				applied_at timestamptz NOT NULL DEFAULT now()
			)
		`); err != nil {
			return fmt.Errorf("db: ensure migrations table: %w", err)
		}
	}

	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("db: read migrations dir: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		var alreadyApplied bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM volunteer_embed_schema_migrations WHERE filename = $1)`,
			name,
		).Scan(&alreadyApplied)
		if err != nil {
			return fmt.Errorf("db: check migration %s: %w", name, err)
		}
		if alreadyApplied {
			continue
		}

		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("db: read migration %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("db: begin migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO volunteer_embed_schema_migrations (filename) VALUES ($1)`, name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: record migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("db: commit migration %s: %w", name, err)
		}
	}
	return nil
}
