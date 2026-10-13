package submit

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/pgvec"
	"github.com/strelov1/freehire-volunteer-embed/internal/reputation"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

// unreachableTEIURL lets these tests exercise acceptance/validation/lease
// logic without a real TEI server; the verification step they trigger
// simply defers (leaves the row pending), which is already the expected
// outcome asserted below. Verification itself is tested in
// TestSubmit_SynchronousVerification_* below and in package verify.
const unreachableTEIURL = "http://127.0.0.1:1"

func testCfg() config.Config {
	return config.Config{FullCheckThreshold: 20, CheckDivisorStage1: 10, CheckDivisorStage2: 30}
}

func fixtureVector() []float64 {
	v := make([]float64, 768)
	for i := range v {
		v[i] = 0.01
	}
	return v
}

// seedLeasedJob seeds a job with one semantic_outbox row and a matching
// lease on it, returning both ids — submissions are identified by job_id,
// but a lease (and the promote path's DELETE) is scoped to the specific
// outbox row, since a job can have more than one outstanding row at once.
func seedLeasedJob(t *testing.T, pool *pgxpool.Pool, keyID int64, expiresAt time.Time) (jobID, outboxID int64) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ($1, 'desc') RETURNING id`,
		"submit-job-"+time.Now().Format("150405.000000000"),
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`,
		jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)`,
		outboxID, jobID, keyID, expiresAt,
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	return jobID, outboxID
}

func TestSubmit_ActiveLeaseValidVector_Accepted(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Status != StatusAccepted {
		t.Errorf("Status = %v, want StatusAccepted", results[0].Status)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM volunteer_submissions WHERE job_id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("querying volunteer_submissions: %v", err)
	}
	if status != "pending" {
		t.Errorf("stored status = %q, want pending", status)
	}
}

func TestSubmit_NoLeaseHeld_RejectedNotLeased(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('unleased-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	results := Submit(ctx, pool, 1, []Item{{OutboxID: 999999, JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_submissions WHERE job_id = $1`, jobID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no row written for an unleased job, got %d", count)
	}
}

func TestSubmit_LeaseHeldByDifferentKey_RejectedNotLeased(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	jobID, outboxID := seedLeasedJob(t, pool, 999, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, 1, []Item{{OutboxID: outboxID, JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_ExpiredLease_RejectedNotLeased(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(-1*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_WrongVectorLength_RejectedInvalidNoReputationImpact(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: []float64{0.1, 0.2}}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusInvalidVector {
		t.Errorf("Status = %v, want StatusInvalidVector", results[0].Status)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_submissions WHERE job_id = $1`, jobID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no row written for an invalid vector, got %d", count)
	}
}

func TestSubmit_NonFiniteVectorValue_RejectedInvalid(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	badVector := fixtureVector()
	badVector[10] = math.NaN()

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: badVector}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusInvalidVector {
		t.Errorf("Status = %v, want StatusInvalidVector", results[0].Status)
	}
}

func TestSubmit_MixedBatch_EachItemHandledIndependently(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	goodJobID, goodOutboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	var unleashedJobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('mixed-unleashed', 'd') RETURNING id`,
	).Scan(&unleashedJobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	results := Submit(ctx, pool, keyID, []Item{
		{OutboxID: goodOutboxID, JobID: goodJobID, Vector: fixtureVector()},
		{OutboxID: 999999, JobID: unleashedJobID, Vector: fixtureVector()},
	}, unreachableTEIURL, testCfg())
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	if results[0].Status != StatusAccepted {
		t.Errorf("results[0].Status = %v, want StatusAccepted", results[0].Status)
	}
	if results[1].Status != StatusNotLeased {
		t.Errorf("results[1].Status = %v, want StatusNotLeased", results[1].Status)
	}
}

func TestSubmit_SynchronousVerification_MatchingVector_EndsVerified(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('sync-verify-match', 'the real job text') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)`,
		outboxID, jobID, keyID, time.Now().Add(5*time.Minute),
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	submitted := fixtureVector()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{submitted}) // trusted re-embedding matches exactly
	}))
	defer server.Close()

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: submitted}}, server.URL, testCfg())
	if results[0].Status != StatusAccepted {
		t.Fatalf("Status = %v, want StatusAccepted", results[0].Status)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM volunteer_submissions WHERE job_id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("querying volunteer_submissions: %v", err)
	}
	if status != "verified" {
		t.Errorf("stored status = %q, want verified", status)
	}

	var consecutivePasses int
	if err := pool.QueryRow(ctx, `SELECT consecutive_passes FROM volunteer_reputation WHERE key_id = $1`, keyID).Scan(&consecutivePasses); err != nil {
		t.Fatalf("querying volunteer_reputation: %v", err)
	}
	if consecutivePasses != 1 {
		t.Errorf("consecutive_passes = %d, want 1 (a passed check must be recorded in reputation)", consecutivePasses)
	}

	// End-to-end proof this actually reaches freehire's live search table,
	// not just the staging table.
	var chunkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&chunkCount); err != nil {
		t.Fatalf("querying job_semantic_chunks: %v", err)
	}
	if chunkCount != 1 {
		t.Errorf("job_semantic_chunks rows for job = %d, want 1 (verified submission must be promoted)", chunkCount)
	}
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM semantic_outbox WHERE job_id = $1`, jobID).Scan(&outboxCount); err != nil {
		t.Fatalf("querying semantic_outbox: %v", err)
	}
	if outboxCount != 0 {
		t.Errorf("semantic_outbox rows for job = %d, want 0 (promotion must clear the outbox entry)", outboxCount)
	}
}

// TestSubmit_SynchronousVerification_SiblingOutboxRowForSameJob_IsUntouched
// guards the production incident this whole change fixes: promoting one
// outbox row for a job must not clear or otherwise affect a SECOND,
// still-unverified outbox row for that same job (e.g. mid migration between
// two embedding model versions, one row per target_model).
func TestSubmit_SynchronousVerification_SiblingOutboxRowForSameJob_IsUntouched(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('sibling-outbox-job', 'the real job text') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID1, outboxID2 int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5-old') RETURNING id`, jobID,
	).Scan(&outboxID1); err != nil {
		t.Fatalf("seed outbox row 1: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5-chunked-v1') RETURNING id`, jobID,
	).Scan(&outboxID2); err != nil {
		t.Fatalf("seed outbox row 2: %v", err)
	}
	for _, id := range []int64{outboxID1, outboxID2} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)`,
			id, jobID, keyID, time.Now().Add(5*time.Minute),
		); err != nil {
			t.Fatalf("seed lease for outbox row %d: %v", id, err)
		}
	}

	submitted := fixtureVector()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{submitted})
	}))
	defer server.Close()

	// Only submit for outboxID1 — outboxID2 is left as a still-outstanding,
	// unverified claim, just like a slow-to-arrive second submission.
	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID1, JobID: jobID, Vector: submitted}}, server.URL, testCfg())
	if results[0].Status != StatusAccepted {
		t.Fatalf("Status = %v, want StatusAccepted", results[0].Status)
	}

	var outboxID2ClaimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT claimed_at FROM semantic_outbox WHERE id = $1`, outboxID2).Scan(&outboxID2ClaimedAt); err != nil {
		t.Fatalf("expected sibling outbox row %d to still exist, but it's gone: %v", outboxID2, err)
	}

	var outboxID2LeaseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_leases WHERE outbox_id = $1`, outboxID2).Scan(&outboxID2LeaseCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if outboxID2LeaseCount != 1 {
		t.Errorf("promoting outbox row %d deleted the lease on sibling row %d too (count=%d, want 1)", outboxID1, outboxID2, outboxID2LeaseCount)
	}
}

func TestSubmit_SynchronousVerification_MismatchedVector_EndsRejected(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('sync-verify-mismatch', 'the real job text') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)`,
		outboxID, jobID, keyID, time.Now().Add(5*time.Minute),
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	submitted := fixtureVector()
	trusted := make([]float64, 768)
	trusted[0] = 1 // orthogonal-ish to submitted's uniform small values: low cosine similarity
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{trusted})
	}))
	defer server.Close()

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: submitted}}, server.URL, testCfg())
	if results[0].Status != StatusAccepted {
		t.Fatalf("Status = %v, want StatusAccepted", results[0].Status)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM volunteer_submissions WHERE job_id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("querying volunteer_submissions: %v", err)
	}
	if status != "rejected" {
		t.Errorf("stored status = %q, want rejected", status)
	}

	var consecutiveFails int
	if err := pool.QueryRow(ctx, `SELECT consecutive_fails FROM volunteer_reputation WHERE key_id = $1`, keyID).Scan(&consecutiveFails); err != nil {
		t.Fatalf("querying volunteer_reputation: %v", err)
	}
	if consecutiveFails != 1 {
		t.Errorf("consecutive_fails = %d, want 1 (a failed check must be recorded in reputation)", consecutiveFails)
	}

	var leaseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_leases WHERE job_id = $1`, jobID).Scan(&leaseCount); err != nil {
		t.Fatalf("querying volunteer_leases: %v", err)
	}
	if leaseCount != 0 {
		t.Errorf("expected the lease to be released back to the pool on a failed check, found %d leases still held", leaseCount)
	}
}

func TestSubmit_ThirdConsecutiveFailure_BansTheKey(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, consecutive_fails) VALUES ($1, 2)
	`, keyID); err != nil {
		t.Fatalf("seed reputation: %v", err)
	}

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('third-strike-job', 'the real job text') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)`,
		outboxID, jobID, keyID, time.Now().Add(5*time.Minute),
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	mismatched := make([]float64, 768)
	mismatched[0] = 1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{mismatched})
	}))
	defer server.Close()

	// This key is already past the full-check phase with consecutive_fails=2
	// and consecutive_passes=0, so the fix for "failure restores full
	// checking" (consecutivePasses < FullCheckThreshold) guarantees this
	// third submission is actually checked, not skipped.
	Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, Vector: fixtureVector()}}, server.URL, testCfg())

	banned, err := reputation.IsBanned(ctx, pool, keyID)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if !banned {
		t.Error("expected the key to be banned after its 3rd consecutive failure")
	}
}

func TestSubmit_DatabaseErrorOnOneItem_DoesNotAbortWholeBatch(t *testing.T) {
	pool := testdb.Setup(t)
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulates a transient infra failure (e.g. client disconnect, pool exhaustion)

	results := Submit(ctx, pool, keyID, []Item{
		{OutboxID: outboxID, JobID: jobID, Vector: fixtureVector()},
		{OutboxID: outboxID, JobID: jobID, Vector: fixtureVector()},
	}, unreachableTEIURL, testCfg())

	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2 (a per-item outcome even when the DB call fails, not a nil/abort)", len(results))
	}
	for i, r := range results {
		if r.Status != StatusError {
			t.Errorf("results[%d].Status = %v, want StatusError", i, r.Status)
		}
	}
}

// TestVerifySubmission_PromoteFailsIndependentlyOfContext_LeavesRowPending
// guards against a specific regression caught in review: a Promote failure
// that has nothing to do with the request's own context being cancelled
// (e.g. a transient DB error) must not still finalize the submission as
// "verified" — that would claim a vector reached job_semantic_chunks when
// it never did, with no worker anywhere to ever revisit it.
//
// It calls verifySubmission directly (this test is in package submit) with
// a vector whose dimension doesn't match job_semantic_chunks.embedding's
// vector(768) column, forcing Promote's INSERT to fail for a reason wholly
// unrelated to ctx. The trusted server returns a vector of the same
// (wrong) length so the cosine comparison still matches and reaches
// OutcomeVerifiedPassedCheck — Submit's own public validVector gate would
// normally reject a 768-length mismatch before this point, so this test
// deliberately bypasses it to exercise verifySubmission in isolation.
func TestVerifySubmission_PromoteFailsIndependentlyOfContext_LeavesRowPending(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-fails-job', 'the real job text') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	// volunteer_submissions.embedding is ALSO vector(768), so this row's own
	// placeholder must be a valid 768-length vector — the dimension
	// mismatch this test exercises is specific to job_semantic_chunks,
	// triggered below via a short vector passed directly to
	// verifySubmission, independent of what's stored on this seed row.
	wrongDimensionVector := []float64{0.1, 0.2, 0.3}
	var submissionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO volunteer_submissions (job_id, submitted_by_key_id, embedding, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id
	`, jobID, keyID, pgvec.Format(fixtureVector())).Scan(&submissionID); err != nil {
		t.Fatalf("seed submission: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{wrongDimensionVector}) // matches exactly -> OutcomeVerifiedPassedCheck
	}))
	defer server.Close()

	item := Item{OutboxID: 1, JobID: jobID, Vector: wrongDimensionVector}
	verifySubmission(ctx, pool, server.URL, testCfg(), keyID, submissionID, item)

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM volunteer_submissions WHERE id = $1`, submissionID).Scan(&status); err != nil {
		t.Fatalf("querying volunteer_submissions: %v", err)
	}
	if status != "pending" {
		t.Errorf("stored status = %q, want pending (Promote failed, so this must not be finalized as verified)", status)
	}

	var chunkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&chunkCount); err != nil {
		t.Fatalf("querying job_semantic_chunks: %v", err)
	}
	if chunkCount != 0 {
		t.Errorf("job_semantic_chunks rows = %d, want 0 (the dimension-mismatched insert must have failed)", chunkCount)
	}
}
