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

// ChunkVector is one verified chunk's index and embedding, ready to write.
type ChunkVector struct {
	ChunkIndex int
	Vector     []float64
}

// Replace atomically replaces jobID's ENTIRE job_semantic_chunks set,
// stamps its embed provenance (model + its current content_hash), clears
// its similar-jobs staleness marker, and clears the SPECIFIC
// semantic_outbox row this completion came from — mirrors hire's own
// cmd/embed completion (DeleteJobSemanticChunks + InsertJobSemanticChunks +
// StampSemanticEmbeddedBatch + ClearSimilarComputedAtBatch, all one
// transaction, see internal/ai/embed.Store.CompleteOpen in the hire repo).
//
// chunks may be empty — a description that strips to nothing is hire's own
// "absent from the chunks map" outcome, a valid final state (zero chunks),
// not an error. Stamping it anyway is what stops it from being
// re-enqueued forever as "not yet embedded".
//
// Unlike the old chunk_index=0-only, ON CONFLICT DO NOTHING version this
// replaces: calling Replace again for the same job with a DIFFERENT chunk
// set (a legitimate re-embed after a content edit) now actually replaces —
// a partial single-chunk write used to leave stale higher-index chunks
// from a PRIOR, longer embed behind forever, since nothing ever deleted
// them (see freehire-ops' 2026-10-14 writeup). A race between two
// concurrent Replace calls for the SAME job (distinct outbox rows, e.g.
// mid model migration) is not specially guarded against here — a
// duplicate-key error on the losing call's INSERT just leaves its row
// pending for a later pass, the same graceful-degradation shape every
// other promotion failure already has.
//
// model comes from the outbox row itself (semantic_outbox.target_model),
// read inside this same transaction, not threaded through the wire
// protocol — the server already has it the moment it has outboxID.
func Replace(ctx context.Context, pool *pgxpool.Pool, outboxID, jobID int64, chunks []ChunkVector) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("promote: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var model string
	if err := tx.QueryRow(ctx,
		`SELECT target_model FROM semantic_outbox WHERE id = $1`, outboxID,
	).Scan(&model); err != nil {
		return fmt.Errorf("promote: read target_model for outbox row %d: %w", outboxID, err)
	}

	if _, err := tx.Exec(ctx,
		`DELETE FROM job_semantic_chunks WHERE job_id = $1`, jobID,
	); err != nil {
		return fmt.Errorf("promote: clear existing chunks for job %d: %w", jobID, err)
	}

	for _, c := range chunks {
		if _, err := tx.Exec(ctx, `
			INSERT INTO job_semantic_chunks (job_id, chunk_index, embedding)
			VALUES ($1, $2, $3)
		`, jobID, c.ChunkIndex, pgvec.Format(c.Vector)); err != nil {
			return fmt.Errorf("promote: insert chunk %d for job %d: %w", c.ChunkIndex, jobID, err)
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE jobs
		SET semantic_embedded_model = $1,
		    semantic_embedded_hash  = content_hash,
		    similar_computed_at     = NULL
		WHERE id = $2
	`, model, jobID); err != nil {
		return fmt.Errorf("promote: stamp job %d: %w", jobID, err)
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
