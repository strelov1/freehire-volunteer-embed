// Package testdb spins up the handful of freehire-owned tables this service
// reads and writes, mirroring their real production shape, so integration
// tests exercise realistic SQL instead of a ad-hoc schema. This service does
// not own or migrate these tables in production — they belong to `hire`.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/db"
)

// hireFixtureSQL mirrors the subset of hire's production schema this service
// depends on: users, api_keys, jobs, semantic_outbox, job_semantic_chunks.
const hireFixtureSQL = `
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email text NOT NULL
);

CREATE TABLE IF NOT EXISTS api_keys (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    token_hash text NOT NULL,
    token_prefix text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    expires_at timestamptz,
    scope text NOT NULL DEFAULT 'full' CHECK (scope IN ('full', 'cv', 'embed'))
);
CREATE UNIQUE INDEX IF NOT EXISTS api_keys_token_hash_idx ON api_keys (token_hash);

CREATE TABLE IF NOT EXISTS jobs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_slug text NOT NULL UNIQUE,
    title text NOT NULL DEFAULT '',
    company text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS semantic_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    job_id bigint NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    target_model text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    claimed_at timestamptz,
    failed_at timestamptz,
    last_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    job_posted_at timestamptz,
    UNIQUE (job_id, target_model)
);

CREATE TABLE IF NOT EXISTS job_semantic_chunks (
    job_id bigint NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    chunk_index smallint NOT NULL,
    embedding vector(768) NOT NULL,
    PRIMARY KEY (job_id, chunk_index)
);
`

// Setup connects to the Postgres instance named by TEST_DATABASE_URL, applies
// the hire fixture schema, and truncates it clean. Call it from integration
// tests; it skips the test if TEST_DATABASE_URL is unset so the suite stays
// runnable without a database.
func Setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database integration test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("testdb: open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, hireFixtureSQL); err != nil {
		t.Fatalf("testdb: apply hire fixture schema: %v", err)
	}
	if err := db.ApplyMigrations(ctx, pool); err != nil {
		t.Fatalf("testdb: apply this service's own migrations: %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			TRUNCATE
				semantic_outbox, job_semantic_chunks, jobs, api_keys, users,
				volunteer_submissions, volunteer_reputation, volunteer_leases
			CASCADE;
		`)
	})

	return pool
}
