package lease

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func seedOutboxRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, jobID int64, targetModel string) int64 {
	t.Helper()
	var outboxID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, claimed_at) VALUES ($1, $2, now()) RETURNING id
	`, jobID, targetModel).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}
	return outboxID
}

func TestRelease_LeasedJob_BecomesImmediatelyReclaimable(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	jobID := seedJob(t, pool, "release-job", "desc")
	outboxID := seedOutboxRow(t, ctx, pool, jobID, "e5")
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, 7, $3)
	`, outboxID, jobID, time.Now().Add(5*time.Minute)); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	if err := Release(ctx, pool, outboxID, 7); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var leaseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_leases WHERE outbox_id = $1`, outboxID).Scan(&leaseCount); err != nil {
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
// back, Release must be a no-op for ownership it no longer holds — not
// wipe out the second volunteer's fresh, legitimate claim.
func TestRelease_LeaseAlreadyReclaimedByAnotherKey_DoesNotStealTheirClaim(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	jobID := seedJob(t, pool, "race-release-job", "desc")
	outboxID := seedOutboxRow(t, ctx, pool, jobID, "e5")
	// Key 42 holds the current, legitimate lease (e.g. just re-claimed it
	// after key 7's original lease went stale).
	const currentHolder = int64(42)
	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)
	`, outboxID, jobID, currentHolder, time.Now().Add(5*time.Minute)); err != nil {
		t.Fatalf("seed lease: %v", err)
	}

	// Key 7's stale spot-check finally resolves and tries to release a lease
	// it no longer actually holds.
	if err := Release(ctx, pool, outboxID, 7); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var leasedBy int64
	if err := pool.QueryRow(ctx, `SELECT leased_by_key_id FROM volunteer_leases WHERE outbox_id = $1`, outboxID).Scan(&leasedBy); err != nil {
		t.Fatalf("expected key 42's lease to survive key 7's stale release, but it's gone: %v", err)
	}
	if leasedBy != currentHolder {
		t.Errorf("leased_by_key_id = %d, want %d (key 42's claim must survive)", leasedBy, currentHolder)
	}

	var claimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT claimed_at FROM semantic_outbox WHERE id = $1`, outboxID).Scan(&claimedAt); err != nil {
		t.Fatalf("querying semantic_outbox: %v", err)
	}
	if claimedAt == nil {
		t.Error("expected semantic_outbox.claimed_at to remain set — key 7's stale release must not null it out from under key 42")
	}
}

// TestRelease_OnlyReleasesItsOwnOutboxRow_SiblingRowForSameJobIsUntouched
// guards the production incident this whole change fixes: a job with two
// outstanding semantic_outbox rows (e.g. mid model-version migration) must
// have each row's claim released independently — releasing one must not
// clear the other's claimed_at or delete its lease.
func TestRelease_OnlyReleasesItsOwnOutboxRow_SiblingRowForSameJobIsUntouched(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	jobID := seedJob(t, pool, "sibling-row-job", "desc")
	outboxID1 := seedOutboxRow(t, ctx, pool, jobID, "e5-old")
	outboxID2 := seedOutboxRow(t, ctx, pool, jobID, "e5-chunked-v1")
	const keyID = int64(7)
	for _, id := range []int64{outboxID1, outboxID2} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO volunteer_leases (outbox_id, job_id, leased_by_key_id, expires_at) VALUES ($1, $2, $3, $4)
		`, id, jobID, keyID, time.Now().Add(5*time.Minute)); err != nil {
			t.Fatalf("seed lease for outbox row %d: %v", id, err)
		}
	}

	if err := Release(ctx, pool, outboxID1, keyID); err != nil {
		t.Fatalf("Release: %v", err)
	}

	var sibling2LeaseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_leases WHERE outbox_id = $1`, outboxID2).Scan(&sibling2LeaseCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sibling2LeaseCount != 1 {
		t.Errorf("releasing outbox row %d deleted the lease on sibling row %d too (count=%d, want 1)", outboxID1, outboxID2, sibling2LeaseCount)
	}

	var sibling2ClaimedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT claimed_at FROM semantic_outbox WHERE id = $1`, outboxID2).Scan(&sibling2ClaimedAt); err != nil {
		t.Fatalf("querying semantic_outbox: %v", err)
	}
	if sibling2ClaimedAt == nil {
		t.Errorf("releasing outbox row %d cleared claimed_at on sibling row %d too", outboxID1, outboxID2)
	}
}
