// Package hiredata holds this service's read-only queries against tables it
// does not own: freehire's api_keys, jobs, and semantic_outbox. This service
// never migrates or writes to these tables except via the narrow, explicit
// promote path defined elsewhere.
package hiredata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type APIKey struct {
	ID        int64
	Scope     string
	ExpiresAt *time.Time
}

// LookupAPIKeyByHash finds the api_keys row with the given token hash.
// found is false (with a nil error) when no such key exists.
func LookupAPIKeyByHash(ctx context.Context, pool *pgxpool.Pool, tokenHash string) (APIKey, bool, error) {
	var key APIKey
	err := pool.QueryRow(ctx,
		`SELECT id, scope, expires_at FROM api_keys WHERE token_hash = $1`,
		tokenHash,
	).Scan(&key.ID, &key.Scope, &key.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return APIKey{}, false, nil
	}
	if err != nil {
		return APIKey{}, false, fmt.Errorf("hiredata: lookup api key: %w", err)
	}
	return key, true, nil
}

// JobText returns the job description backing a semantic_outbox entry.
func JobText(ctx context.Context, pool *pgxpool.Pool, jobID int64) (string, error) {
	var description string
	err := pool.QueryRow(ctx,
		`SELECT description FROM jobs WHERE id = $1`,
		jobID,
	).Scan(&description)
	if err != nil {
		return "", fmt.Errorf("hiredata: job text for job %d: %w", jobID, err)
	}
	return description, nil
}
