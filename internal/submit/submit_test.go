package submit

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

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

	results, err := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: fixtureVector()}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
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

	results, err := Submit(ctx, pool, 1, []Item{{JobID: jobID, Vector: fixtureVector()}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
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

	results, err := Submit(ctx, pool, 1, []Item{{JobID: jobID, Vector: fixtureVector()}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_ExpiredLease_RejectedNotLeased(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(-1*time.Minute))

	results, err := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: fixtureVector()}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if results[0].Status != StatusNotLeased {
		t.Errorf("Status = %v, want StatusNotLeased", results[0].Status)
	}
}

func TestSubmit_WrongVectorLength_RejectedInvalidNoReputationImpact(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const keyID = int64(1)
	jobID := seedLeasedJob(t, pool, keyID, time.Now().Add(5*time.Minute))

	results, err := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: []float64{0.1, 0.2}}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
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

	results, err := Submit(ctx, pool, keyID, []Item{{JobID: jobID, Vector: badVector}})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
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

	results, err := Submit(ctx, pool, keyID, []Item{
		{JobID: goodJobID, Vector: fixtureVector()},
		{JobID: unleashedJobID, Vector: fixtureVector()},
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
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
