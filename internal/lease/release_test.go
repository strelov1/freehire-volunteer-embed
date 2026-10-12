package lease

import (
	"context"
	"testing"
	"time"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestRelease_LeasedJob_BecomesImmediatelyReclaimable(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	jobID := seedJob(t, pool, "release-job", "desc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, claimed_at) VALUES ($1, 'e5', now())
	`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at) VALUES ($1, 7, $2)
	`, jobID, time.Now().Add(5*time.Minute)); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	if err := Release(ctx, pool, jobID); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var leaseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_leases WHERE job_id = $1`, jobID).Scan(&leaseCount); err != nil {
		t.Fatalf("count after release: %v", err)
	}
	if leaseCount != 0 {
		t.Fatalf("expected the volunteer_leases row to be removed by Release, found %d", leaseCount)
	}

	claimed, err := Claim(ctx, pool, 999, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim after release: %v", err)
	}
	if len(claimed) != 1 || claimed[0].JobID != jobID {
		t.Fatalf("expected the released job to be immediately reclaimable, got %+v", claimed)
	}
}
