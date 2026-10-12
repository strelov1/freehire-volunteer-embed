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

	if err := Release(ctx, pool, jobID, 7); err != nil {
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

// TestRelease_LeaseAlreadyReclaimedByAnotherKey_DoesNotStealTheirClaim is the
// race flagged in review: if a lease went stale and was re-claimed by a
// second volunteer before the first volunteer's (slow) spot-check comes
// back, Release must be a no-op for job ownership it no longer holds — not
// wipe out the second volunteer's fresh, legitimate claim.
func TestRelease_LeaseAlreadyReclaimedByAnotherKey_DoesNotStealTheirClaim(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	jobID := seedJob(t, pool, "race-release-job", "desc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, claimed_at) VALUES ($1, 'e5', now())
	`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	// Key 42 holds the current, legitimate lease (e.g. just re-claimed it
	// after key 7's original lease went stale).
	const currentHolder = int64(42)
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_leases (job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3)
	`, jobID, currentHolder, time.Now().Add(5*time.Minute)); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	// Key 7's stale spot-check finally resolves and tries to release a lease
	// it no longer actually holds.
	if err := Release(ctx, pool, jobID, 7); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var leasedBy int64
	if err := pool.QueryRow(ctx, `SELECT leased_by_key_id FROM volunteer_leases WHERE job_id = $1`, jobID).Scan(&leasedBy); err != nil {
		t.Fatalf("expected key 42's lease to survive key 7's stale release, but it's gone: %v", err)
	}
	if leasedBy != currentHolder {
		t.Errorf("leased_by_key_id = %d, want %d (key 42's claim must survive)", leasedBy, currentHolder)
	}

	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT claimed_at FROM semantic_outbox WHERE job_id = $1`, jobID).Scan(&claimedAt); err != nil {
		t.Fatalf("querying semantic_outbox: %v", err)
	}
	if claimedAt == nil {
		t.Error("expected semantic_outbox.claimed_at to remain set — key 7's stale release must not null it out from under key 42")
	}
}
