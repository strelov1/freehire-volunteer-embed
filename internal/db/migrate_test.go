package db_test

import (
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/db"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestApplyMigrations_CreatesOwnedTables(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}

	for _, table := range []string{"volunteer_submissions", "volunteer_reputation", "volunteer_leases"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("checking table %s: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %q to exist after ApplyMigrations", table)
		}
	}
}

func TestApplyMigrations_IsIdempotent(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("first ApplyMigrations: %v", err)
	}
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("second ApplyMigrations should be a no-op, got error: %v", err)
	}
}

// Production grants this service's role no CREATE privilege on the schema —
// only SELECT/INSERT/UPDATE/DELETE on the tables it owns. Once a fresh deploy
// has applied migrations once (as a privileged role, by hand), every later
// restart must still succeed under the narrower role.
func TestApplyMigrations_SucceedsWithoutCreatePrivilegeWhenAlreadyApplied(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("seeding ApplyMigrations: %v", err)
	}

	const lowPrivRole = "fve_test_lowpriv"
	_, err := pool.Exec(ctx, `
		DO $do$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '`+lowPrivRole+`') THEN
				CREATE ROLE `+lowPrivRole+` LOGIN PASSWORD 'test' NOSUPERUSER NOCREATEDB NOCREATEROLE;
			END IF;
		END
		$do$;
		GRANT USAGE ON SCHEMA public TO `+lowPrivRole+`;
		GRANT SELECT, INSERT ON volunteer_embed_schema_migrations TO `+lowPrivRole+`;
	`)
	if err != nil {
		t.Fatalf("creating low-privilege test role: %v", err)
	}

	lowPrivURL, err := withUser(os.Getenv("TEST_DATABASE_URL"), lowPrivRole, "test")
	if err != nil {
		t.Fatalf("building low-privilege connection URL: %v", err)
	}
	lowPool, err := pgxpool.New(ctx, lowPrivURL)
	if err != nil {
		t.Fatalf("opening low-privilege pool: %v", err)
	}
	defer lowPool.Close()

	if err := db.ApplyMigrations(ctx, lowPool); err != nil {
		t.Fatalf("ApplyMigrations with no CREATE privilege on an already-migrated schema should succeed, got: %v", err)
	}
}

func withUser(rawURL, user, password string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	u.User = url.UserPassword(user, password)
	return u.String(), nil
}
