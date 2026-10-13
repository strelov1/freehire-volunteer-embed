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
// It is scoped to keyID's own lease on this specific outbox row — not the
// job as a whole, since a job can have more than one outstanding
// semantic_outbox row (e.g. mid migration between two embedding model
// versions) each independently leased. If this lease has since gone stale
// and been reclaimed by a different key (a slow spot-check can finish well
// after a batch's other items advance the clock), Release is a no-op
// rather than clearing semantic_outbox.claimed_at and deleting
// volunteer_leases out from under that new, legitimate holder.
func Release(ctx context.Context, pool *pgxpool.Pool, outboxID, keyID int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("lease: release begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx,
		`DELETE FROM volunteer_leases WHERE outbox_id = $1 AND leased_by_key_id = $2`, outboxID, keyID,
	)
	if err != nil {
		return fmt.Errorf("lease: release delete lease for outbox row %d: %w", outboxID, err)
	}
	if tag.RowsAffected() == 0 {
		// Already reclaimed by someone else; nothing of ours left to release.
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE semantic_outbox SET claimed_at = NULL WHERE id = $1`, outboxID,
	); err != nil {
		return fmt.Errorf("lease: release clear claim for outbox row %d: %w", outboxID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("lease: release commit: %w", err)
	}
	return nil
}
