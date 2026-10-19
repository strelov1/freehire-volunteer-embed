package submit

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/jobtext"
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

// longDescription reliably chunks into more than one piece under
// internal/jobtext's real chunking (see its own tests for the exact
// split) — used by every multi-chunk test here so they exercise the real
// algorithm instead of a hand-picked chunk count.
func longDescription() string {
	var paras []string
	for i := 0; i < 40; i++ {
		paras = append(paras, strings.Repeat("word ", 20)+"end of paragraph.")
	}
	return strings.Join(paras, "\n")
}

// teiServerEchoing stands in for the trusted TEI: Submit now sends every
// pending-check item's job text in ONE request, so a fixture that always
// returns a single hardcoded vector would make tei.EmbedBatch itself fail
// (it validates the response has one vector per requested text). This
// decodes the request and returns vec once per text requested, whatever
// the batch size.
func teiServerEchoing(vec []float64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			vectors[i] = vec
		}
		_ = json.NewEncoder(w).Encode(vectors)
	}))
}

// seedLeasedJob seeds a single-chunk job with one semantic_outbox row and
// a matching lease on it, returning both ids.
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

// seedLeasedJobWithDescription is seedLeasedJob for a caller-chosen
// description, returning the REAL chunk texts internal/jobtext would
// compute for it — tests build their Items from these texts rather than
// hardcoding an assumed chunk count.
func seedLeasedJobWithDescription(t *testing.T, pool *pgxpool.Pool, keyID int64, expiresAt time.Time, description string) (jobID, outboxID int64, chunks []string) {
	t.Helper()
	ctx := context.Background()
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ($1, $2) RETURNING id`,
		"submit-job-"+time.Now().Format("150405.000000000"), description,
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
	return jobID, outboxID, jobtext.FormatChunksForEmbedding("", "", description)
}

func TestSubmit_ActiveLeaseValidVector_Accepted(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
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

	results := Submit(ctx, pool, 1, []Item{{OutboxID: 999999, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
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

	results := Submit(ctx, pool, 1, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_ExpiredLease_RejectedNotLeased(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(-1*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_WrongVectorLength_RejectedInvalidNoReputationImpact(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: []float64{0.1, 0.2}}}, unreachableTEIURL, testCfg())
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

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: badVector}}, unreachableTEIURL, testCfg())
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
		{OutboxID: goodOutboxID, JobID: goodJobID, ChunkIndex: 0, Vector: fixtureVector()},
		{OutboxID: 999999, JobID: unleashedJobID, ChunkIndex: 0, Vector: fixtureVector()},
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
	server := teiServerEchoing(submitted) // trusted re-embedding matches exactly
	defer server.Close()

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: submitted}}, server.URL, testCfg())
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

	var model string
	if err := pool.QueryRow(ctx, `SELECT semantic_embedded_model FROM jobs WHERE id = $1`, jobID).Scan(&model); err != nil {
		t.Fatalf("querying jobs: %v", err)
	}
	if model != "e5" {
		t.Errorf("semantic_embedded_model = %q, want %q (the completion stamp hire's own worker checks)", model, "e5")
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
	server := teiServerEchoing(submitted)
	defer server.Close()

	// Only submit for outboxID1 — outboxID2 is left as a still-outstanding,
	// unverified claim, just like a slow-to-arrive second submission.
	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID1, JobID: jobID, ChunkIndex: 0, Vector: submitted}}, server.URL, testCfg())
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
	server := teiServerEchoing(trusted)
	defer server.Close()

	results := Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: submitted}}, server.URL, testCfg())
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
	server := teiServerEchoing(mismatched)
	defer server.Close()

	// This key is already past the full-check phase with consecutive_fails=2
	// and consecutive_passes=0, so the fix for "failure restores full
	// checking" (consecutivePasses < FullCheckThreshold) guarantees this
	// third submission is actually checked, not skipped.
	Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}}, server.URL, testCfg())

	banned, err := reputation.IsBanned(ctx, pool, keyID)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if !banned {
		t.Error("expected the key to be banned after its 3rd consecutive failure")
	}
}

// TestSubmit_ManyItemsNeedingVerification_OneSharedTEICall guards the fix
// for a real production bottleneck: a key still in its full-check window
// (every item re-embedded through the trusted TEI) took roughly 1s per item
// when checked one request at a time, so any batch above ~25-30 items blew
// past the volunteer client's own request timeout -- and that didn't fully
// go away even with per-item concurrency, once several volunteers were each
// submitting large batches against the one shared trusted TEI at once.
// Submit now makes exactly one TEI request per Submit call, covering every
// pending-check item in that batch, regardless of item count. A
// slow-but-healthy TEI stand-in here (200ms/call, call-counted) proves
// that: 20 items would cost 20 calls (4s) the old way; one call (~200ms)
// proves the fix.
func TestSubmit_ManyItemsNeedingVerification_OneSharedTEICall(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	const itemCount = 20
	const perCallDelay = 200 * time.Millisecond

	submitted := fixtureVector()
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		time.Sleep(perCallDelay)
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			vectors[i] = submitted
		}
		_ = json.NewEncoder(w).Encode(vectors)
	}))
	defer server.Close()

	items := make([]Item, itemCount)
	for i := 0; i < itemCount; i++ {
		jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: submitted}
	}

	start := time.Now()
	results := Submit(ctx, pool, keyID, items, server.URL, testCfg())
	elapsed := time.Since(start)

	for i, r := range results {
		if r.Status != StatusAccepted {
			t.Errorf("results[%d].Status = %v, want StatusAccepted", i, r.Status)
		}
	}

	if calls != 1 {
		t.Errorf("TEI received %d requests for a batch of %d items, want exactly 1", calls, itemCount)
	}
	if elapsed >= itemCount*perCallDelay {
		t.Errorf("Submit of %d items took %s, want well under the old per-item-call floor of %s",
			itemCount, elapsed, itemCount*perCallDelay)
	}
}

// TestSubmit_ConcurrentItemsSameKey_ReputationCounterHasNoLostUpdates guards
// against the obvious risk of making Submit concurrent: multiple goroutines
// incrementing the same key's reputation row must not lose updates to a
// naive, non-atomic read-modify-write race. TakeTurns is a single atomic
// SQL upsert, so Postgres's row lock should serialize these
// correctly regardless of Go-level concurrency — this proves it.
func TestSubmit_ConcurrentItemsSameKey_ReputationCounterHasNoLostUpdates(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	const itemCount = 20

	// Both total_evaluated and consecutive_passes must individually clear
	// FullCheckThreshold (20) before the divisor rule applies at all, and
	// divisor=30 once consecutive_passes clears 2*threshold. total_evaluated
	// climbs by exactly itemCount over this test (no lost updates is what
	// it's proving), and the whole climbed range -- seed..seed+itemCount --
	// avoids any multiple of 30 so no item needs a real check against the
	// deliberately unreachable TEI (31..51 contains none) — not that a
	// deferred check would stop the count any more: TakeTurns advances it
	// whatever happens to the check.
	const seedTotalEvaluated = 31
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes)
		VALUES ($1, $2, 100)
	`, keyID, seedTotalEvaluated); err != nil {
		t.Fatalf("seed reputation: %v", err)
	}

	items := make([]Item, itemCount)
	for i := 0; i < itemCount; i++ {
		jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}
	}

	results := Submit(ctx, pool, keyID, items, unreachableTEIURL, testCfg())
	for i, r := range results {
		if r.Status != StatusAccepted {
			t.Errorf("results[%d].Status = %v, want StatusAccepted", i, r.Status)
		}
	}

	var totalEvaluated int
	if err := pool.QueryRow(ctx, `SELECT total_evaluated FROM volunteer_reputation WHERE key_id = $1`, keyID).Scan(&totalEvaluated); err != nil {
		t.Fatalf("querying volunteer_reputation: %v", err)
	}
	if want := seedTotalEvaluated + itemCount; totalEvaluated != want {
		t.Errorf("total_evaluated = %d, want %d (every one of %d concurrent items must still land its own +1, with none lost)",
			totalEvaluated, want, itemCount)
	}
}

func TestSubmit_DatabaseErrorOnOneItem_DoesNotAbortWholeBatch(t *testing.T) {
	pool := testdb.Setup(t)
	const keyID = int64(1)
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulates a transient infra failure (e.g. client disconnect, pool exhaustion)

	results := Submit(ctx, pool, keyID, []Item{
		{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()},
		{OutboxID: outboxID, JobID: jobID, ChunkIndex: 1, Vector: fixtureVector()},
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

// TestSubmit_ManyPendingChecks_TEISubBatchesRunConcurrently guards a second
// real production incident found right after the max-batch-size fix
// (2026-10-14): chunking a large pending-check set into sequential
// 32-item sub-requests to the trusted TEI could add up to MORE wall-clock
// time than the client's own request timeout once several sub-batches
// were needed, so the client gave up and the request's context was
// cancelled mid-flight, deferring everything anyway. A slow-but-healthy
// TEI stand-in here (150ms/sub-batch) proves sub-batches run
// concurrently: 4 sub-batches run sequentially would cost 600ms;
// concurrent dispatch should finish in a small multiple of 150ms, not a
// sum of all of it.
func TestSubmit_ManyPendingChecks_TEISubBatchesRunConcurrently(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	const itemCount = 4 * trustedTEIMaxBatch // 4 sub-batches
	const perCallDelay = 150 * time.Millisecond

	submitted := fixtureVector()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(perCallDelay)
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			vectors[i] = submitted
		}
		_ = json.NewEncoder(w).Encode(vectors)
	}))
	defer server.Close()

	items := make([]Item, itemCount)
	for i := 0; i < itemCount; i++ {
		jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: submitted}
	}

	start := time.Now()
	results := Submit(ctx, pool, keyID, items, server.URL, testCfg())
	elapsed := time.Since(start)

	for i, r := range results {
		if r.Status != StatusAccepted {
			t.Errorf("results[%d].Status = %v, want StatusAccepted", i, r.Status)
		}
	}
	if elapsed >= 4*perCallDelay {
		t.Errorf("Submit across 4 TEI sub-batches took %s, want well under the fully-sequential floor of %s",
			elapsed, 4*perCallDelay)
	}
}

// TestSubmit_ManyPendingChecks_ChunksTEICallsToItsOwnMaxBatchSize guards a
// real production incident (2026-10-14): the trusted TEI server enforces
// its own --max-client-batch-size (32, matching hire's teiMaxBatch) and
// rejects a single request carrying more inputs than that with a plain
// error -- which, before this fix, meant combining every pending-check
// item in a batch into ONE EmbedBatch call made the WHOLE call fail
// outright the moment a batch's items needing a check exceeded 32,
// silently leaving every one of them "pending" forever (confirmed live:
// several active volunteers' submissions sat unverified for hours because
// of exactly this). The fake server below rejects any request over 32
// inputs, the same way the real TEI does -- Submit must split its one
// logical "verify everything pending" step into sub-requests that respect
// that ceiling, not send it all in one shot.
func TestSubmit_ManyPendingChecks_ChunksTEICallsToItsOwnMaxBatchSize(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	const itemCount = 71 // > 32, matching the real incident's batch size

	submitted := fixtureVector()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body.Inputs) > 32 {
			http.Error(w, "batch size exceeds maximum allowed batch size 32", http.StatusBadRequest)
			return
		}
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			vectors[i] = submitted
		}
		_ = json.NewEncoder(w).Encode(vectors)
	}))
	defer server.Close()

	items := make([]Item, itemCount)
	for i := 0; i < itemCount; i++ {
		jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: submitted}
	}

	results := Submit(ctx, pool, keyID, items, server.URL, testCfg())
	for i, r := range results {
		if r.Status != StatusAccepted {
			t.Errorf("results[%d].Status = %v, want StatusAccepted", i, r.Status)
		}
	}

	var verifiedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_submissions WHERE status = 'verified'`).Scan(&verifiedCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if verifiedCount != itemCount {
		t.Errorf("verified submissions = %d, want %d (a real TEI's own batch-size limit must not leave everything pending forever)", verifiedCount, itemCount)
	}
}

// TestSubmit_MultiChunkJob_AllVerified_PromotesEveryChunk is the core proof
// of the actual fix: a job needing more than one chunk is promoted with
// ALL of its chunks, and reputation is only touched ONCE for the whole
// job, not once per chunk.
func TestSubmit_MultiChunkJob_AllVerified_PromotesEveryChunk(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	jobID, outboxID, chunks := seedLeasedJobWithDescription(t, pool, keyID, time.Now().Add(5*time.Minute), longDescription())
	if len(chunks) <= 1 {
		t.Fatalf("len(chunks) = %d, want > 1 for this fixture's description", len(chunks))
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			vectors[i] = fixtureVector() // matches whatever was submitted
		}
		_ = json.NewEncoder(w).Encode(vectors)
	}))
	defer server.Close()

	items := make([]Item, len(chunks))
	for i := range chunks {
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: i, Vector: fixtureVector()}
	}

	results := Submit(ctx, pool, keyID, items, server.URL, testCfg())
	for i, r := range results {
		if r.Status != StatusAccepted {
			t.Errorf("results[%d].Status = %v, want StatusAccepted", i, r.Status)
		}
	}

	var chunkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&chunkCount); err != nil {
		t.Fatalf("querying job_semantic_chunks: %v", err)
	}
	if chunkCount != len(chunks) {
		t.Errorf("job_semantic_chunks rows = %d, want %d (every chunk must be promoted)", chunkCount, len(chunks))
	}

	var consecutivePasses int
	if err := pool.QueryRow(ctx, `SELECT consecutive_passes FROM volunteer_reputation WHERE key_id = $1`, keyID).Scan(&consecutivePasses); err != nil {
		t.Fatalf("querying volunteer_reputation: %v", err)
	}
	if consecutivePasses != 1 {
		t.Errorf("consecutive_passes = %d, want 1 (one reputation increment per JOB, not per chunk)", consecutivePasses)
	}
}

// TestSubmit_MultiChunkJob_OneChunkMismatched_RejectsWholeJobNoPromote
// proves the all-or-nothing contract: one bad chunk among several rejects
// the whole job's submission, and nothing is promoted (not even the
// chunks that matched).
func TestSubmit_MultiChunkJob_OneChunkMismatched_RejectsWholeJobNoPromote(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	jobID, outboxID, chunks := seedLeasedJobWithDescription(t, pool, keyID, time.Now().Add(5*time.Minute), longDescription())
	if len(chunks) <= 1 {
		t.Fatalf("len(chunks) = %d, want > 1 for this fixture's description", len(chunks))
	}

	callIndex := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			if callIndex == 0 && i == 0 {
				mismatched := make([]float64, 768)
				mismatched[0] = 1 // orthogonal-ish to fixtureVector(): low cosine similarity
				vectors[i] = mismatched
			} else {
				vectors[i] = fixtureVector()
			}
		}
		callIndex++
		_ = json.NewEncoder(w).Encode(vectors)
	}))
	defer server.Close()

	items := make([]Item, len(chunks))
	for i := range chunks {
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: i, Vector: fixtureVector()}
	}

	results := Submit(ctx, pool, keyID, items, server.URL, testCfg())
	for i, r := range results {
		if r.Status != StatusAccepted {
			t.Errorf("results[%d].Status = %v, want StatusAccepted", i, r.Status)
		}
	}

	var chunkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&chunkCount); err != nil {
		t.Fatalf("querying job_semantic_chunks: %v", err)
	}
	if chunkCount != 0 {
		t.Errorf("job_semantic_chunks rows = %d, want 0 (one mismatched chunk must block the whole job's promotion)", chunkCount)
	}

	var rejectedCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_submissions WHERE job_id = $1 AND status = 'rejected'`, jobID).Scan(&rejectedCount); err != nil {
		t.Fatalf("querying volunteer_submissions: %v", err)
	}
	if rejectedCount != len(chunks) {
		t.Errorf("rejected submission rows = %d, want %d (every chunk in the job, not just the mismatched one)", rejectedCount, len(chunks))
	}
}

// TestSubmit_MultiChunkJob_MissingAChunk_LeavesPendingNoPromote proves an
// incomplete chunk set (fewer than the job's description currently
// requires) is never promoted — promoting a partial set is exactly the
// bug this whole change exists to fix.
func TestSubmit_MultiChunkJob_MissingAChunk_LeavesPendingNoPromote(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	jobID, outboxID, chunks := seedLeasedJobWithDescription(t, pool, keyID, time.Now().Add(5*time.Minute), longDescription())
	if len(chunks) <= 1 {
		t.Fatalf("len(chunks) = %d, want > 1 for this fixture's description", len(chunks))
	}

	// Submit only the FIRST chunk, simulating a client that dropped the rest.
	results := Submit(ctx, pool, keyID, []Item{
		{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()},
	}, unreachableTEIURL, testCfg())
	if results[0].Status != StatusAccepted {
		t.Fatalf("Status = %v, want StatusAccepted (the row still stages; it just never finalizes)", results[0].Status)
	}

	var chunkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&chunkCount); err != nil {
		t.Fatalf("querying job_semantic_chunks: %v", err)
	}
	if chunkCount != 0 {
		t.Errorf("job_semantic_chunks rows = %d, want 0 (an incomplete chunk set must never be promoted)", chunkCount)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM volunteer_submissions WHERE job_id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("querying volunteer_submissions: %v", err)
	}
	if status != "pending" {
		t.Errorf("stored status = %q, want pending (left for a later, complete submission)", status)
	}
}

// TestFinishChecked_PromoteFails_LeavesRowPending guards against a specific
// regression caught in review: a Promote failure must not still finalize
// the submission as "verified" — that would claim a vector reached
// job_semantic_chunks when it never did, with no worker anywhere to ever
// revisit it.
//
// It calls finishChecked directly (this test is in package submit) with a
// vector whose dimension doesn't match job_semantic_chunks.embedding's
// vector(768) column, forcing Replace's INSERT to fail. The "trusted"
// vector passed in is identical, so the cosine comparison still matches —
// Submit's own public validVector gate would normally reject a
// 768-length mismatch before this point, so this test deliberately
// bypasses it to exercise finishChecked in isolation.
func TestFinishChecked_PromoteFails_LeavesRowPending(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-fails-job', 'the real job text') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	// volunteer_submissions.embedding is ALSO vector(768), so this row's own
	// placeholder must be a valid 768-length vector — the dimension
	// mismatch this test exercises is specific to job_semantic_chunks,
	// triggered below via a short vector passed directly to finishChecked,
	// independent of what's stored on this seed row.
	wrongDimensionVector := []float64{0.1, 0.2, 0.3}
	var submissionID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO volunteer_submissions (job_id, submitted_by_key_id, embedding, chunk_index, status)
		VALUES ($1, $2, $3, 0, 'pending')
		RETURNING id
	`, jobID, keyID, pgvec.Format(fixtureVector())).Scan(&submissionID); err != nil {
		t.Fatalf("seed submission: %v", err)
	}

	pg := &pendingGroup{
		outboxID:       outboxID,
		jobID:          jobID,
		submissionIDs:  []int64{submissionID},
		submittedVecs:  [][]float64{wrongDimensionVector},
		expectedTexts:  []string{"whatever text"},
		trustedVectors: [][]float64{wrongDimensionVector}, // identical -> cosine matches
	}
	finishChecked(ctx, pool, keyID, pg)

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

// TestSubmit_TrustedKeyBatch_ChecksOnlyItsShareOfJobs is the 2026-10-18
// incident: every group in a batch read the key's counters before any of
// them advanced it, so all of them landed on the same total_evaluated and
// made the same decision. Once in every divisor batches that decision was
// "check", and the WHOLE batch was re-embedded through the trusted TEI —
// a ~90-chunk call on a shared CPU TEI, longer than the client's 55s
// timeout. A trusted key must have exactly one job in divisor checked,
// however the jobs are batched.
func TestSubmit_TrustedKeyBatch_ChecksOnlyItsShareOfJobs(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	const itemCount = 30 // == CheckDivisorStage2

	// A multiple of 30, past both thresholds: the batch starts on a "check" turn.
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes)
		VALUES ($1, 300, 300)
	`, keyID); err != nil {
		t.Fatalf("seed reputation: %v", err)
	}

	submitted := fixtureVector()
	var checkedTexts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Inputs []string `json:"inputs"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		checkedTexts.Add(int64(len(body.Inputs)))
		vectors := make([][]float64, len(body.Inputs))
		for i := range vectors {
			vectors[i] = submitted
		}
		_ = json.NewEncoder(w).Encode(vectors)
	}))
	defer server.Close()

	items := make([]Item, itemCount)
	for i := 0; i < itemCount; i++ {
		jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))
		items[i] = Item{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: submitted}
	}

	Submit(ctx, pool, keyID, items, server.URL, testCfg())

	if got := checkedTexts.Load(); got != 1 {
		t.Errorf("trusted TEI re-embedded %d of %d jobs, want exactly 1 (one in CheckDivisorStage2)", got, itemCount)
	}
}

// TestSubmit_DeferredCheck_StillAdvancesTheCounter: a check the trusted TEI
// could not finish used to leave total_evaluated where it was, so the next
// submission landed on the very same "check" turn — and under load that
// one deferred too, forever. Taking the turn is what advances the counter,
// whatever happens to the check afterwards.
func TestSubmit_DeferredCheck_StillAdvancesTheCounter(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes)
		VALUES ($1, 300, 300)
	`, keyID); err != nil {
		t.Fatalf("seed reputation: %v", err)
	}
	jobID, outboxID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	Submit(ctx, pool, keyID, []Item{{OutboxID: outboxID, JobID: jobID, ChunkIndex: 0, Vector: fixtureVector()}}, unreachableTEIURL, testCfg())

	var totalEvaluated int
	if err := pool.QueryRow(ctx, `SELECT total_evaluated FROM volunteer_reputation WHERE key_id = $1`, keyID).Scan(&totalEvaluated); err != nil {
		t.Fatalf("querying volunteer_reputation: %v", err)
	}
	if totalEvaluated != 301 {
		t.Errorf("total_evaluated = %d after a deferred check, want 301", totalEvaluated)
	}
}
