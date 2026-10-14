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
	"golang.org/x/sync/errgroup"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/hiredata"
	"github.com/strelov1/freehire-volunteer-embed/internal/lease"
	"github.com/strelov1/freehire-volunteer-embed/internal/pgvec"
	"github.com/strelov1/freehire-volunteer-embed/internal/promote"
	"github.com/strelov1/freehire-volunteer-embed/internal/reputation"
	"github.com/strelov1/freehire-volunteer-embed/internal/tei"
	"github.com/strelov1/freehire-volunteer-embed/internal/verify"
)

const vectorDimension = 768

// submitConcurrency bounds how many items from one batch stage (DB-only:
// lease check, insert, job text lookup, reputation read) or finish
// (DB-only: record outcome, promote) at once. Bounded, not unlimited, so
// one huge batch can't flood the DB pool with every item at once.
const submitConcurrency = 8

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

// pendingCheck is an item staged in volunteer_submissions whose reputation
// says it needs an actual spot-check, carrying what finishing it later
// needs: which row to finalize, the submitted vector to compare, and the
// job text to re-embed.
type pendingCheck struct {
	submissionID int64
	item         Item
	jobText      string
}

// Submit processes each item independently: a rejection OR an
// infrastructure error on one item never affects the others, and
// submission never touches reputation (reputation only changes as a result
// of spot-check verification, not at submission time — see
// internal/reputation group 7). An accepted item is also spot-check
// verified synchronously, before this call returns: there is no separate
// background worker (see design.md).
//
// Every item selected for a check is re-embedded through the trusted TEI in
// ONE shared request for the whole batch, not one request per item — this
// is the fix for a real incident: with one TEI round trip per item, even
// bounded per-item concurrency couldn't keep a single shared trusted TEI
// ahead of several volunteers each submitting large batches at once, and
// batches routinely blew the client's request timeout.
//
// Staging (lease check, insert, check-vs-skip decision) and finishing
// (recording the outcome) each run with bounded concurrency
// (submitConcurrency), not strictly in order. For items sharing a key, this
// means the check-vs-skip decision (reputation.ShouldCheck) can read a
// stale, pre-increment snapshot of that key's counters — a batch can sample
// a slightly different set of items for checking than a fully sequential
// run would have. The counters themselves do not lose updates under this:
// each Record* call is one atomic SQL UPDATE, serialized by Postgres's own
// row lock regardless of Go-level concurrency.
func Submit(ctx context.Context, pool *pgxpool.Pool, keyID int64, items []Item, teiURL string, cfg config.Config) []Result {
	results := make([]Result, len(items))
	pendingSlots := make([]*pendingCheck, len(items))

	var stage errgroup.Group
	stage.SetLimit(submitConcurrency)
	for i, item := range items {
		i, item := i, item
		stage.Go(func() error {
			status, pc := stageOne(ctx, pool, keyID, item, cfg)
			results[i] = Result{JobID: item.JobID, Status: status}
			pendingSlots[i] = pc
			return nil
		})
	}
	_ = stage.Wait() // stageOne never returns an error; failure is a Status value

	var pending []pendingCheck
	for _, pc := range pendingSlots {
		if pc != nil {
			pending = append(pending, *pc)
		}
	}
	if len(pending) == 0 {
		return results
	}

	jobTexts := make([]string, len(pending))
	for i, pc := range pending {
		jobTexts[i] = pc.jobText
	}
	trustedVectors, err := tei.EmbedBatch(ctx, teiURL, cfg.TrustedTEIAPIKey, jobTexts)
	if err != nil {
		// Trusted TEI unreachable or erroring for the whole batch: every
		// pending item stays pending with no reputation change, exactly
		// like a single deferred check before this change — a later
		// submission gets another chance.
		return results
	}

	var finish errgroup.Group
	finish.SetLimit(submitConcurrency)
	for i, pc := range pending {
		i, pc := i, pc
		finish.Go(func() error {
			finishChecked(ctx, pool, keyID, pc, trustedVectors[i])
			return nil
		})
	}
	_ = finish.Wait()

	return results
}

// stageOne validates item, confirms its lease, and stages it in
// volunteer_submissions. If the key's reputation says this one should be
// spot-checked, it returns a *pendingCheck for the batched TEI call;
// otherwise it finishes the item immediately (trusted on track record, no
// TEI call needed at all).
func stageOne(ctx context.Context, pool *pgxpool.Pool, keyID int64, item Item, cfg config.Config) (Status, *pendingCheck) {
	if !validVector(item.Vector) {
		return StatusInvalidVector, nil
	}

	var leased bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM volunteer_leases
			WHERE outbox_id = $1 AND leased_by_key_id = $2 AND expires_at > now()
		)
	`, item.OutboxID, keyID).Scan(&leased)
	if err != nil {
		return StatusError, nil
	}
	if !leased {
		return StatusNotLeased, nil
	}

	var submissionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO volunteer_submissions (job_id, submitted_by_key_id, embedding, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id
	`, item.JobID, keyID, pgvec.Format(item.Vector)).Scan(&submissionID); err != nil {
		return StatusError, nil
	}

	// From here on, any failure leaves the row pending for a later pass
	// rather than failing the request — a verification hiccup must not
	// change what the volunteer's HTTP call saw.
	jobText, err := hiredata.JobText(ctx, pool, item.JobID)
	if err != nil {
		return StatusAccepted, nil
	}
	totalEvaluated, consecutivePasses, err := reputation.GetCounts(ctx, pool, keyID)
	if err != nil {
		return StatusAccepted, nil
	}

	if !reputation.ShouldCheck(totalEvaluated, consecutivePasses, cfg) {
		finishSkipped(ctx, pool, keyID, submissionID, item)
		return StatusAccepted, nil
	}

	return StatusAccepted, &pendingCheck{submissionID: submissionID, item: item, jobText: jobText}
}

// finishSkipped records a submission trusted on track record, with no TEI
// call at all.
func finishSkipped(ctx context.Context, pool *pgxpool.Pool, keyID, submissionID int64, item Item) {
	if err := reputation.RecordSkipped(ctx, pool, keyID); err != nil {
		log.Printf("submit: record skipped for key %d, submission %d: %v", keyID, submissionID, err)
	}
	if err := promote.Promote(ctx, pool, item.OutboxID, item.JobID, item.Vector); err != nil {
		log.Printf("submit: promote job %d, submission %d: %v", item.JobID, submissionID, err)
		return // do not finalize: nothing actually reached job_semantic_chunks
	}
	finalize(ctx, pool, keyID, submissionID, item.JobID, "verified")
}

// finishChecked records the outcome of a submission that was re-embedded
// through the trusted TEI as part of this batch's single shared call.
func finishChecked(ctx context.Context, pool *pgxpool.Pool, keyID int64, pc pendingCheck, trustedVector []float64) {
	if verify.Cosine(pc.item.Vector, trustedVector) >= verify.MatchThreshold {
		if err := reputation.RecordPassed(ctx, pool, keyID); err != nil {
			log.Printf("submit: record passed for key %d, submission %d: %v", keyID, pc.submissionID, err)
		}
		if err := promote.Promote(ctx, pool, pc.item.OutboxID, pc.item.JobID, pc.item.Vector); err != nil {
			log.Printf("submit: promote job %d, submission %d: %v", pc.item.JobID, pc.submissionID, err)
			return // do not finalize: nothing actually reached job_semantic_chunks
		}
		finalize(ctx, pool, keyID, pc.submissionID, pc.item.JobID, "verified")
		return
	}

	justBanned, err := reputation.RecordFailed(ctx, pool, keyID)
	if err != nil {
		log.Printf("submit: record failed for key %d, submission %d: %v", keyID, pc.submissionID, err)
	}
	if justBanned {
		log.Printf("AUDIT ban key=%d reason=3_consecutive_spot_check_failures last_submission=%d job=%d",
			keyID, pc.submissionID, pc.item.JobID)
	}
	if err := lease.Release(ctx, pool, pc.item.OutboxID, keyID); err != nil {
		log.Printf("submit: release lease for outbox row %d (job %d), key %d: %v", pc.item.OutboxID, pc.item.JobID, keyID, err)
	}
	finalize(ctx, pool, keyID, pc.submissionID, pc.item.JobID, "rejected")
}

func finalize(ctx context.Context, pool *pgxpool.Pool, keyID, submissionID, jobID int64, status string) {
	log.Printf("AUDIT verify key=%d job=%d submission=%d outcome=%s", keyID, jobID, submissionID, status)
	if _, err := pool.Exec(ctx,
		`UPDATE volunteer_submissions SET status = $1, decided_at = now() WHERE id = $2`,
		status, submissionID,
	); err != nil {
		log.Printf("submit: finalize submission %d as %s: %v", submissionID, status, err)
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
