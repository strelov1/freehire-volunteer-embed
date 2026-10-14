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
	"github.com/strelov1/freehire-volunteer-embed/internal/jobtext"
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

// JobChunks returns the exact texts to embed for a job — the same
// "passage: {title} at {company}. {chunk}" (HTML-stripped, chunked) texts
// hire's own cmd/embed would feed to TEI (see internal/jobtext), one per
// chunk of the description, in order. This is what's handed to volunteers
// to embed and what this service re-embeds itself during spot-check
// verification. A description that strips to nothing returns a nil slice
// with no error — a valid, final state (see jobtext.FormatChunksForEmbedding).
func JobChunks(ctx context.Context, pool *pgxpool.Pool, jobID int64) ([]string, error) {
	var title, company, description string
	err := pool.QueryRow(ctx,
		`SELECT title, company, description FROM jobs WHERE id = $1`,
		jobID,
	).Scan(&title, &company, &description)
	if err != nil {
		return nil, fmt.Errorf("hiredata: job chunks for job %d: %w", jobID, err)
	}
	return jobtext.FormatChunksForEmbedding(title, company, description), nil
}
