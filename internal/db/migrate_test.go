package db

import (
	"context"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestApplyMigrations_CreatesOwnedTables(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if err := ApplyMigrations(ctx, pool); err != nil {
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

	if err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("first ApplyMigrations: %v", err)
	}
	if err := ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("second ApplyMigrations should be a no-op, got error: %v", err)
	}
}
