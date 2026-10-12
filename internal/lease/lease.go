// Package lease claims batches of pending semantic_outbox jobs on behalf of
// a volunteer API key, recording who holds each claim in volunteer_leases
// since semantic_outbox itself (owned by hire) has no claimant-identity
// column (see design.md Decision 6).
package lease

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/jobtext"
)

type ClaimedJob struct {
	JobID int64
	Text  string
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
			so.job_id,
			(SELECT title FROM jobs WHERE jobs.id = so.job_id),
			(SELECT company FROM jobs WHERE jobs.id = so.job_id),
			(SELECT description FROM jobs WHERE jobs.id = so.job_id)
	`, leaseDuration.Seconds(), batchSize)
	if err != nil {
		return nil, fmt.Errorf("lease: claim batch: %w", err)
	}

	var claimed []ClaimedJob
	for rows.Next() {
		var jobID int64
		var title, company, description string
		if err := rows.Scan(&jobID, &title, &company, &description); err != nil {
			rows.Close()
			return nil, fmt.Errorf("lease: scan claimed row: %w", err)
		}
		claimed = append(claimed, ClaimedJob{
			JobID: jobID,
			Text:  jobtext.FormatForEmbedding(title, company, description),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lease: iterate claimed rows: %w", err)
	}

	expiresAt := time.Now().Add(leaseDuration)
	for _, job := range claimed {
		if _, err := tx.Exec(ctx, `
			INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at)
			VALUES ($1, $2, $3)
			ON CONFLICT (job_id) DO UPDATE
			SET leased_by_key_id = EXCLUDED.leased_by_key_id, expires_at = EXCLUDED.expires_at
		`, job.JobID, keyID, expiresAt); err != nil {
			return nil, fmt.Errorf("lease: record leaseholder for job %d: %w", job.JobID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("lease: commit: %w", err)
	}
	return claimed, nil
}
