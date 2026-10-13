// Package submit accepts volunteer-computed embedding vectors and stages
// them in volunteer_submissions for later verification. It never writes to
// freehire's live job_semantic_chunks table — that happens only after
// verification (see the promotion package, task group 8).
package submit

import (
	"context"
	"log"
	"math"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/hiredata"
	"github.com/strelov1/freehire-volunteer-embed/internal/lease"
	"github.com/strelov1/freehire-volunteer-embed/internal/pgvec"
	"github.com/strelov1/freehire-volunteer-embed/internal/promote"
	"github.com/strelov1/freehire-volunteer-embed/internal/reputation"
	"github.com/strelov1/freehire-volunteer-embed/internal/verify"
)

const vectorDimension = 768

type Status int

const (
	StatusAccepted Status = iota
	StatusNotLeased
	StatusInvalidVector
	// StatusError means an infrastructure problem (DB unavailable, context
	// cancelled, etc.) prevented resolving this item. Unlike the other
	// statuses it is not a final answer — the caller should retry this item.
	StatusError
)

type Item struct {
	OutboxID int64
	JobID    int64
	Vector   []float64
}

type Result struct {
	JobID  int64
	Status Status
}

// Submit processes each item independently: a rejection OR an infrastructure
// error on one item never affects the others, and submission never touches
// reputation (reputation only changes as a result of spot-check
// verification, not at submission time — see internal/reputation group 7).
// An accepted item is also spot-check verified synchronously, before this
// call returns: there is no separate background worker (see design.md).
func Submit(ctx context.Context, pool *pgxpool.Pool, keyID int64, items []Item, teiURL string, cfg config.Config) []Result {
	results := make([]Result, len(items))
	for i, item := range items {
		results[i] = Result{JobID: item.JobID, Status: submitOne(ctx, pool, keyID, item, teiURL, cfg)}
	}
	return results
}

func submitOne(ctx context.Context, pool *pgxpool.Pool, keyID int64, item Item, teiURL string, cfg config.Config) Status {
	if !validVector(item.Vector) {
		return StatusInvalidVector
	}

	var leased bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM volunteer_leases
			WHERE outbox_id = $1 AND leased_by_key_id = $2 AND expires_at > now()
		)
	`, item.OutboxID, keyID).Scan(&leased)
	if err != nil {
		return StatusError
	}
	if !leased {
		return StatusNotLeased
	}

	var submissionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO volunteer_submissions (job_id, submitted_by_key_id, embedding, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id
	`, item.JobID, keyID, pgvec.Format(item.Vector)).Scan(&submissionID); err != nil {
		return StatusError
	}

	verifySubmission(ctx, pool, teiURL, cfg, keyID, submissionID, item)

	return StatusAccepted
}

// verifySubmission runs the spot-check decision for a just-staged submission
// and records the outcome on its volunteer_submissions row. It deliberately
// swallows its own errors (job text lookup, TEI call) rather than failing
// the submission request: a verification hiccup leaves the row pending for
// a later pass, it does not change what the volunteer's HTTP call saw.
func verifySubmission(ctx context.Context, pool *pgxpool.Pool, teiURL string, cfg config.Config, keyID, submissionID int64, item Item) {
	jobText, err := hiredata.JobText(ctx, pool, item.JobID)
	if err != nil {
		return
	}
	totalEvaluated, consecutivePasses, err := reputation.GetCounts(ctx, pool, keyID)
	if err != nil {
		return
	}

	outcome := verify.Evaluate(ctx, teiURL, cfg, totalEvaluated, consecutivePasses, jobText, item.Vector)

	var newStatus string
	switch outcome {
	case verify.OutcomeVerifiedWithoutCheck:
		if err := reputation.RecordSkipped(ctx, pool, keyID); err != nil {
			log.Printf("submit: record skipped for key %d, submission %d: %v", keyID, submissionID, err)
		}
		if err := promote.Promote(ctx, pool, item.OutboxID, item.JobID, item.Vector); err != nil {
			log.Printf("submit: promote job %d, submission %d: %v", item.JobID, submissionID, err)
			return // do not mark verified: nothing actually reached job_semantic_chunks
		}
		newStatus = "verified"
	case verify.OutcomeVerifiedPassedCheck:
		if err := reputation.RecordPassed(ctx, pool, keyID); err != nil {
			log.Printf("submit: record passed for key %d, submission %d: %v", keyID, submissionID, err)
		}
		if err := promote.Promote(ctx, pool, item.OutboxID, item.JobID, item.Vector); err != nil {
			log.Printf("submit: promote job %d, submission %d: %v", item.JobID, submissionID, err)
			return // do not mark verified: nothing actually reached job_semantic_chunks
		}
		newStatus = "verified"
	case verify.OutcomeRejectedFailedCheck:
		newStatus = "rejected"
		justBanned, err := reputation.RecordFailed(ctx, pool, keyID)
		if err != nil {
			log.Printf("submit: record failed for key %d, submission %d: %v", keyID, submissionID, err)
		}
		if justBanned {
			log.Printf("AUDIT ban key=%d reason=3_consecutive_spot_check_failures last_submission=%d job=%d",
				keyID, submissionID, item.JobID)
		}
		if err := lease.Release(ctx, pool, item.OutboxID, keyID); err != nil {
			log.Printf("submit: release lease for outbox row %d (job %d), key %d: %v", item.OutboxID, item.JobID, keyID, err)
		}
	default: // OutcomeDeferredTEIUnavailable: leave pending, no reputation change
		return
	}

	log.Printf("AUDIT verify key=%d job=%d submission=%d outcome=%s",
		keyID, item.JobID, submissionID, newStatus)

	if _, err := pool.Exec(ctx,
		`UPDATE volunteer_submissions SET status = $1, decided_at = now() WHERE id = $2`,
		newStatus, submissionID,
	); err != nil {
		log.Printf("submit: finalize submission %d as %s: %v", submissionID, newStatus, err)
	}
}

func validVector(v []float64) bool {
	if len(v) != vectorDimension {
		return false
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
