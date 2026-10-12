package lease

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Release returns a job to the pool immediately, instead of waiting out
// its lease TTL — used when a spot-check fails and the job needs a fresh
// attempt from someone else right away.
//
// It is scoped to keyID's own lease: if the job's lease has since gone
// stale and been reclaimed by a different key (a slow spot-check can
// finish well after a batch's other items advance the clock), Release is a
// no-op rather than clearing semantic_outbox.claimed_at and deleting
// volunteer_leases out from under that new, legitimate holder.
func Release(ctx context.Context, pool *pgxpool.Pool, jobID, keyID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lease: release begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`DELETE FROM volunteer_leases WHERE job_id = $1 AND leased_by_key_id = $2`, jobID, keyID,
	)
	if err != nil {
		return fmt.Errorf("lease: release delete lease for job %d: %w", jobID, err)
	}
	if tag.RowsAffected() == 0 {
		// Already reclaimed by someone else; nothing of ours left to release.
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE semantic_outbox SET claimed_at = NULL WHERE job_id = $1`, jobID,
	); err != nil {
		return fmt.Errorf("lease: release clear claim for job %d: %w", jobID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("lease: release commit: %w", err)
	}
	return nil
}
