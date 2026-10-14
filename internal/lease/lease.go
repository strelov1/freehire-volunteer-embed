// Package lease claims batches of pending semantic_outbox jobs on behalf of
// a volunteer API key, recording who holds each claim in volunteer_leases
// since semantic_outbox itself (owned by hire) has no claimant-identity
// column (see design.md Decision 6).
package lease

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/jobtext"
	"github.com/strelov1/freehire-volunteer-embed/internal/promote"
)

// ClaimedJob is one chunk of one claimed outbox row. A job needing more
// than one chunk (internal/jobtext's chunkText) produces several
// ClaimedJob entries sharing the same OutboxID/JobID with increasing
// ChunkIndex — the lease itself is still one row per outbox entry
// (volunteer_leases), not per chunk.
type ClaimedJob struct {
	OutboxID   int64
	JobID      int64
	ChunkIndex int
	Text       string
}

// Claim atomically claims up to batchSize pending jobs for keyID, oldest
// job_posted_at first — this service exists to drain the backlog, not to
// race hire's own worker for freshly posted jobs. It skips rows already
// claimed within leaseDuration (by this service or hire's own cmd/embed
// worker, since both read/write semantic_outbox.claimed_at) and rows
// permanently failed. Returns an empty slice, not an error, when nothing is
// claimable.
func Claim(ctx context.Context, pool *pgxpool.Pool, keyID int64, batchSize int, leaseDuration time.Duration) ([]ClaimedJob, error) {
	if batchSize <= 0 {
		return nil, fmt.Errorf("lease: batchSize must be positive, got %d", batchSize)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lease: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	rows, err := tx.Query(ctx, `
		WITH claimable AS (
			SELECT id, job_id FROM semantic_outbox
			WHERE failed_at IS NULL
			  AND (claimed_at IS NULL OR claimed_at < now() - make_interval(secs => $1))
			ORDER BY job_posted_at ASC NULLS FIRST, job_id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE semantic_outbox so
		SET claimed_at = now()
		FROM claimable c
		WHERE so.id = c.id
		RETURNING
			so.id,
			so.job_id,
			(SELECT title FROM jobs WHERE jobs.id = so.job_id),
			(SELECT company FROM jobs WHERE jobs.id = so.job_id),
			(SELECT description FROM jobs WHERE jobs.id = so.job_id)
	`, leaseDuration.Seconds(), batchSize)
	if err != nil {
		return nil, fmt.Errorf("lease: claim batch: %w", err)
	}

	type rawClaim struct {
		outboxID int64
		jobID    int64
		chunks   []string
	}
	var raw []rawClaim
	type emptyClaim struct{ outboxID, jobID int64 }
	var empty []emptyClaim // nothing to embed: complete these immediately, never lease them
	for rows.Next() {
		var outboxID, jobID int64
		var title, company, description string
		if err := rows.Scan(&outboxID, &jobID, &title, &company, &description); err != nil {
			rows.Close()
			return nil, fmt.Errorf("lease: scan claimed row: %w", err)
		}
		chunks := jobtext.FormatChunksForEmbedding(title, company, description)
		if len(chunks) == 0 {
			empty = append(empty, emptyClaim{outboxID: outboxID, jobID: jobID})
			continue
		}
		raw = append(raw, rawClaim{outboxID: outboxID, jobID: jobID, chunks: chunks})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lease: iterate claimed rows: %w", err)
	}

	var claimed []ClaimedJob
	expiresAt := time.Now().Add(leaseDuration)
	for _, job := range raw {
		if _, err := tx.Exec(ctx, `
			INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (outbox_id) DO UPDATE
			SET leased_by_key_id = EXCLUDED.leased_by_key_id, expires_at = EXCLUDED.expires_at
		`, job.outboxID, job.jobID, keyID, expiresAt); err != nil {
			return nil, fmt.Errorf("lease: record leaseholder for outbox row %d (job %d): %w", job.outboxID, job.jobID, err)
		}
		for i, text := range job.chunks {
			claimed = append(claimed, ClaimedJob{OutboxID: job.outboxID, JobID: job.jobID, ChunkIndex: i, Text: text})
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("lease: commit: %w", err)
	}

	// Outside the claim transaction: each of these is its own atomic
	// replace+stamp (promote.Replace), mirroring hire's own "absent from
	// the chunks map" outcome for an empty description. A failure here
	// must not abort the whole call — claimed already holds real,
	// successfully-leased work for OTHER jobs in this same batch (its own
	// transaction already committed above), and discarding it here would
	// lose that legitimate claim to the one unrelated failure. It just
	// leaves that one row claimed for its lease to expire and retry later
	// — it never touched volunteer_leases, so there is no lease to clean
	// up either.
	for _, e := range empty {
		if err := promote.Replace(ctx, pool, e.outboxID, e.jobID, nil); err != nil {
			log.Printf("lease: complete empty-description outbox row %d (job %d): %v", e.outboxID, e.jobID, err)
		}
	}

	return claimed, nil
}
