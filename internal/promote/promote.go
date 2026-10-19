// Package promote writes a verified volunteer embedding into freehire's
// live job_semantic_chunks table and clears the corresponding
// semantic_outbox row — the only place this service touches hire's search
// index, and only after verification (see design.md Decision 3).
package promote

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
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
// model comes from the outbox row itself (semantic_outbox.target_model) —
// read via a subquery in the same UPDATE statement below, rather than a
// separate round trip, since nothing else in this function needs it in Go.
//
// Every statement below is independent of every other statement's RESULT
// (the model subquery removes the one dependency that used to force a
// round trip before the rest could be queued), so they are all sent to
// Postgres in a single pipelined batch instead of one round trip per
// statement — cutting a single-chunk job's promotion from 5 sequential
// round trips to 1. This matters because each round trip pays this
// service's full network+scheduling latency, and under this service's
// real production load (several volunteers submitting large batches at
// once against an already I/O-loaded host) that latency, not any single
// query's execution time, was the actual throughput ceiling (see
// freehire-ops' 2026-10-14 throughput-regression writeup).
func Replace(ctx context.Context, pool *pgxpool.Pool, outboxID, jobID int64, chunks []ChunkVector) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("promote: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	batch := &pgx.Batch{}
	batch.Queue(`DELETE FROM job_semantic_chunks WHERE job_id = $1`, jobID)
	for _, c := range chunks {
		batch.Queue(`
			INSERT INTO job_semantic_chunks (job_id, chunk_index, embedding)
			VALUES ($1, $2, $3)
		`, jobID, c.ChunkIndex, pgvec.Format(c.Vector))
	}
	stampIndex := batch.Len()
	// The WHERE clause's EXISTS guard makes this a no-op (0 rows affected)
	// rather than silently stamping NULL as the model if outboxID's row is
	// gone by the time this runs — checked below, same as the old
	// separate-SELECT version failing outright in that case.
	batch.Queue(`
		UPDATE jobs
		SET semantic_embedded_model = (SELECT target_model FROM semantic_outbox WHERE id = $1),
		    semantic_embedded_hash  = content_hash,
		    similar_computed_at     = NULL
		WHERE id = $2
		  AND EXISTS (SELECT 1 FROM semantic_outbox WHERE id = $1)
	`, outboxID, jobID)
	batch.Queue(`DELETE FROM semantic_outbox WHERE id = $1`, outboxID)

	results := tx.SendBatch(ctx, batch)
	for i := 0; i < batch.Len(); i++ {
		tag, err := results.Exec()
		if err != nil {
			results.Close() //nolint:errcheck
			return fmt.Errorf("promote: batch statement %d for job %d: %w", i, jobID, err)
		}
		if i == stampIndex && tag.RowsAffected() == 0 {
			results.Close() //nolint:errcheck
			return fmt.Errorf("promote: stamp job %d: outbox row %d not found", jobID, outboxID)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("promote: close batch for job %d: %w", jobID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("promote: commit: %w", err)
	}
	return nil
}
