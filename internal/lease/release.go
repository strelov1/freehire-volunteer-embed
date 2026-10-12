package lease

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Release returns a job to the pool immediately, instead of waiting out
// its lease TTL — used when a spot-check fails and the job needs a fresh
// attempt from someone else right away.
func Release(ctx context.Context, pool *pgxpool.Pool, jobID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lease: release begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		`UPDATE semantic_outbox SET claimed_at = NULL WHERE job_id = $1`, jobID,
	); err != nil {
		return fmt.Errorf("lease: release clear claim for job %d: %w", jobID, err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM volunteer_leases WHERE job_id = $1`, jobID,
	); err != nil {
		return fmt.Errorf("lease: release delete lease for job %d: %w", jobID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("lease: release commit: %w", err)
	}
	return nil
}
