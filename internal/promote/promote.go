// Package promote writes a verified volunteer embedding into freehire's
// live job_semantic_chunks table and clears the corresponding
// semantic_outbox row — the only place this service touches hire's search
// index, and only after verification (see design.md Decision 3).
package promote

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/pgvec"
)

// chunkIndex is always 0: this service's lease/submit protocol carries one
// text and one vector per job, never multiple chunks (design.md Non-Goals).
const chunkIndex = 0

// Promote writes vec as job_id's chunk 0 and clears the SPECIFIC
// semantic_outbox row (outboxID) this submission came from, in one
// transaction. It is idempotent: a job already promoted is left untouched
// (its first promotion wins) rather than overwritten by a later, possibly
// stale, re-submission — a destructive overwrite here would silently
// corrupt freehire's live search index (design.md Context).
//
// The outbox clear is scoped to outboxID, not every row for jobID: a job
// can have more than one outstanding semantic_outbox row at once (e.g. mid
// migration between two embedding model versions, one row per
// target_model), and clearing all of them because ONE got promoted would
// silently abandon the other's still-unverified submission — a real
// incident this service hit on 2026-10-13 (see freehire-ops' writeup).
func Promote(ctx context.Context, pool *pgxpool.Pool, outboxID, jobID int64, vec []float64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("promote: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `
		INSERT INTO job_semantic_chunks (job_id, chunk_index, embedding)
		VALUES ($1, $2, $3)
		ON CONFLICT (job_id, chunk_index) DO NOTHING
	`, jobID, chunkIndex, pgvec.Format(vec)); err != nil {
		return fmt.Errorf("promote: insert chunk for job %d: %w", jobID, err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM semantic_outbox WHERE id = $1`, outboxID,
	); err != nil {
		return fmt.Errorf("promote: clear outbox row %d for job %d: %w", outboxID, jobID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("promote: commit: %w", err)
	}
	return nil
}
