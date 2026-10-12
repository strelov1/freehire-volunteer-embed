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

func seedLeasedJob(t *testing.T, pool *pgxpool.Pool, keyID int64, expiresAt time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ($1, 'desc') RETURNING id`,
		"submit-job-"+time.Now().Format("150405.000000000"),
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3)`,
		jobID, keyID, expiresAt,
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}
	return jobID
}

func TestSubmit_ActiveLeaseValidVector_Accepted(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
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

	results := Submit(ctx, pool, 1, []Item{{JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
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
	jobID := seedLeasedJob(t, pool, 999, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, 1, []Item{{JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_ExpiredLease_RejectedNotLeased(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(-1*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_WrongVectorLength_RejectedInvalidNoReputationImpact(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: []float64{0.1, 0.2}}}, unreachableTEIURL, testCfg())
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
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	badVector := fixtureVector()
	badVector[10] = math.NaN()

	results := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: badVector}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusInvalidVector {
		t.Errorf("Status = %v, want StatusInvalidVector", results[0].Status)
	}
}

func TestSubmit_MixedBatch_EachItemHandledIndependently(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	goodJobID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	var unleashedJobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('mixed-unleashed', 'd') RETURNING id`,
	).Scan(&unleashedJobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	results := Submit(ctx, pool, keyID, []Item{
		{JobID: goodJobID, Vector: fixtureVector()},
		{JobID: unleashedJobID, Vector: fixtureVector()},
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3)`,
		jobID, keyID, time.Now().Add(5*time.Minute),
	); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	submitted := fixtureVector()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([][]float64{submitted}) // trusted re-embedding matches exactly
	}))
	defer server.Close()

	results := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: submitted}}, server.URL, testCfg())
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3)`,
		jobID, keyID, time.Now().Add(5*time.Minute),
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

	results := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: submitted}}, server.URL, testCfg())
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3)`,
		jobID, keyID, time.Now().Add(5*time.Minute),
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
	// checking" (consecutivePasses &lt; FullCheckThreshold) guarantees this
	// third submission is actually checked, not skipped.
	Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: fixtureVector()}}, server.URL, testCfg())

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
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulates a transient infra failure (e.g. client disconnect, pool exhaustion)

	results := Submit(ctx, pool, keyID, []Item{
		{JobID: jobID, Vector: fixtureVector()},
		{JobID: jobID, Vector: fixtureVector()},
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
