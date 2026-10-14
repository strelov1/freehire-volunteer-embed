package lease

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func seedJob(t *testing.T, pool *pgxpool.Pool, slug, description string) int64 {
	t.Helper()
	var jobID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO jobs (public_slug, description) VALUES ($1, $2) RETURNING id`,
		slug, description,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job %s: %v", slug, err)
	}
	return jobID
}

func TestClaim_PendingJob_IsClaimedWithText(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, title, company, description) VALUES ($1, $2, $3, $4) RETURNING id`,
		"acme-eng", "Backend Engineer", "Acme", "Build great APIs",
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')
	`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	if claimed[0].JobID != jobID {
		t.Errorf("JobID = %d, want %d", claimed[0].JobID, jobID)
	}
	if claimed[0].ChunkIndex != 0 {
		t.Errorf("ChunkIndex = %d, want 0", claimed[0].ChunkIndex)
	}
	want := "passage: Backend Engineer at Acme. Build great APIs"
	if claimed[0].Text != want {
		t.Errorf("Text = %q, want %q", claimed[0].Text, want)
	}
}

// TestClaim_LongDescription_ClaimsOneEntryPerChunk guards the actual fix:
// a job whose description needs more than one chunk must hand out one
// ClaimedJob per chunk (same OutboxID/JobID, increasing ChunkIndex) so a
// volunteer embeds — and later submits — the FULL description, not just
// its first ~2000 runes.
func TestClaim_LongDescription_ClaimsOneEntryPerChunk(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var paras []string
	for i := 0; i < 40; i++ {
		paras = append(paras, strings.Repeat("word ", 20)+"end of paragraph.")
	}
	description := strings.Join(paras, "\n")

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, title, company, description)
		VALUES ('acme-long-job', 'Engineer', 'Acme', $1) RETURNING id
	`, description).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) <= 1 {
		t.Fatalf("len(claimed) = %d, want > 1 for a description well past the chunk budget", len(claimed))
	}
	for i, c := range claimed {
		if c.OutboxID != outboxID {
			t.Errorf("claimed[%d].OutboxID = %d, want %d (all chunks share the one outbox row)", i, c.OutboxID, outboxID)
		}
		if c.JobID != jobID {
			t.Errorf("claimed[%d].JobID = %d, want %d", i, c.JobID, jobID)
		}
		if c.ChunkIndex != i {
			t.Errorf("claimed[%d].ChunkIndex = %d, want %d", i, c.ChunkIndex, i)
		}
	}

	// Still exactly ONE lease row for the one outbox row, not one per chunk.
	var leaseCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM volunteer_leases WHERE outbox_id = $1`, outboxID).Scan(&leaseCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if leaseCount != 1 {
		t.Errorf("volunteer_leases rows for outbox %d = %d, want 1 (one lease per outbox row, not per chunk)", outboxID, leaseCount)
	}
}

// TestClaim_EmptyDescriptionJob_CompletesImmediatelyWithoutLeasing matches
// hire's own "absent from the chunks map" outcome: a job with nothing to
// embed is stamped and cleared from the queue right away, never handed to
// a volunteer (there is nothing for them to compute).
func TestClaim_EmptyDescriptionJob_CompletesImmediatelyWithoutLeasing(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, title, company, description, content_hash)
		VALUES ('acme-empty-job', 'Engineer', 'Acme', '', 'hash-1') RETURNING id
	`).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')`, jobID,
	); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("len(claimed) = %d, want 0 (nothing to embed, completed immediately)", len(claimed))
	}

	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM semantic_outbox WHERE job_id = $1`, jobID).Scan(&outboxCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if outboxCount != 0 {
		t.Errorf("expected the outbox row to be cleared immediately, found %d", outboxCount)
	}

	var model string
	if err := pool.QueryRow(ctx, `SELECT semantic_embedded_model FROM jobs WHERE id = $1`, jobID).Scan(&model); err != nil {
		t.Fatalf("querying jobs: %v", err)
	}
	if model != "e5" {
		t.Errorf("semantic_embedded_model = %q, want %q (stamped even with zero chunks)", model, "e5")
	}
}

// TestClaim_MixedEmptyAndNonEmptyInSameBatch_NonEmptyStillClaimed guards a
// regression caught in review: completing an empty-description job happens
// OUTSIDE the claim transaction, in a loop that must not let a problem
// with one such job take down the ALREADY-committed, legitimate claim for
// every other job in the same batch.
func TestClaim_MixedEmptyAndNonEmptyInSameBatch_NonEmptyStillClaimed(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	emptyJobID := seedJob(t, pool, "mixed-empty-job", "")
	if _, err := pool.Exec(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')`, emptyJobID,
	); err != nil {
		t.Fatalf("seed empty job's outbox row: %v", err)
	}

	jobID := seedJob(t, pool, "mixed-nonempty-job", "desc")
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed non-empty job's outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1 (the non-empty job, regardless of the empty one's own completion)", len(claimed))
	}
	if claimed[0].JobID != jobID || claimed[0].OutboxID != outboxID {
		t.Errorf("claimed[0] = %+v, want the non-empty job %d / outbox %d", claimed[0], jobID, outboxID)
	}
}

func TestClaim_EmptyQueue_ReturnsEmptyNotError(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("len(claimed) = %d, want 0", len(claimed))
	}
}

func TestClaim_RespectsBatchSizeLimit(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		jobID := seedJob(t, pool, "job-"+string(rune('a'+i)), "desc")
		if _, err := pool.Exec(ctx, `
			INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')
		`, jobID); err != nil {
			t.Fatalf("seed outbox row: %v", err)
		}
	}

	claimed, err := Claim(ctx, pool, 1, 3, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 3 {
		t.Errorf("len(claimed) = %d, want 3", len(claimed))
	}
}

func TestClaim_AlreadyClaimedRecently_IsNotReclaimed(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	jobID := seedJob(t, pool, "already-claimed", "desc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, claimed_at) VALUES ($1, 'e5', now())
	`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("len(claimed) = %d, want 0 (recently claimed, not stale)", len(claimed))
	}
}

func TestClaim_StaleClaim_IsReclaimed(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	jobID := seedJob(t, pool, "stale-claim", "desc")
	staleClaim := time.Now().Add(-10 * time.Minute)
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, claimed_at) VALUES ($1, 'e5', $2)
	`, jobID, staleClaim); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1 (claim is stale, should be reclaimable)", len(claimed))
	}
	if claimed[0].JobID != jobID {
		t.Errorf("JobID = %d, want %d", claimed[0].JobID, jobID)
	}
}

func TestClaim_FailedJob_IsNeverClaimed(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	jobID := seedJob(t, pool, "failed-job", "desc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, failed_at) VALUES ($1, 'e5', now())
	`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("len(claimed) = %d, want 0 (permanently failed job)", len(claimed))
	}
}

func TestClaim_PrefersOldestPostedJobFirst(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	olderJobID := seedJob(t, pool, "old-backlog-job", "old")
	newerJobID := seedJob(t, pool, "fresh-job", "new")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, job_posted_at) VALUES ($1, 'e5', now() - interval '30 days')
	`, olderJobID); err != nil {
		t.Fatalf("seed older outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model, job_posted_at) VALUES ($1, 'e5', now())
	`, newerJobID); err != nil {
		t.Fatalf("seed newer outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 1, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 1 {
		t.Fatalf("len(claimed) = %d, want 1", len(claimed))
	}
	if claimed[0].JobID != olderJobID {
		t.Errorf("claimed job %d, want the older backlog job %d (this service exists to drain the old backlog, not prioritize fresh postings)", claimed[0].JobID, olderJobID)
	}
}

// TestClaim_SameJobTwoOutboxRows_BothClaimedWithDistinctOutboxIDs guards
// against a real production incident (2026-10-13): a job mid-migration
// between two embedding model versions has TWO semantic_outbox rows (one
// per target_model). Both are claimable in the same batch, and each must
// keep its own identity — collapsing them to one indistinguishable "job 42"
// claim is what let promoting one submission silently delete the other's
// still-pending outbox row (see freehire-ops' incident writeup).
func TestClaim_SameJobTwoOutboxRows_BothClaimedWithDistinctOutboxIDs(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	jobID := seedJob(t, pool, "mid-migration-job", "desc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5-old')
	`, jobID); err != nil {
		t.Fatalf("seed old-model outbox row: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5-chunked-v1')
	`, jobID); err != nil {
		t.Fatalf("seed chunked-v1 outbox row: %v", err)
	}

	claimed, err := Claim(ctx, pool, 1, 10, 5*time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("len(claimed) = %d, want 2 (both outbox rows for this job)", len(claimed))
	}
	if claimed[0].JobID != jobID || claimed[1].JobID != jobID {
		t.Fatalf("expected both claims for job %d, got %+v", jobID, claimed)
	}
	if claimed[0].OutboxID == 0 || claimed[1].OutboxID == 0 {
		t.Fatalf("expected non-zero OutboxID on both claims, got %+v", claimed)
	}
	if claimed[0].OutboxID == claimed[1].OutboxID {
		t.Errorf("expected distinct OutboxID per outbox row, got the same value %d twice", claimed[0].OutboxID)
	}

	var leaseCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM volunteer_leases WHERE job_id = $1`, jobID,
	).Scan(&leaseCount); err != nil {
		t.Fatalf("counting volunteer_leases: %v", err)
	}
	if leaseCount != 2 {
		t.Errorf("volunteer_leases rows for job %d = %d, want 2 (one per outbox row, not collapsed to one per job)", jobID, leaseCount)
	}
}

func TestClaim_RecordsLeaseholderInVolunteerLeases(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	jobID := seedJob(t, pool, "leaseholder-check", "desc")
	if _, err := pool.Exec(ctx, `
		INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5')
	`, jobID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	const keyID = int64(42)
	if _, err := Claim(ctx, pool, keyID, 10, 5*time.Minute); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	var leasedBy int64
	if err := pool.QueryRow(ctx,
		`SELECT leased_by_key_id FROM volunteer_leases WHERE job_id = $1`, jobID,
	).Scan(&leasedBy); err != nil {
		t.Fatalf("querying volunteer_leases: %v", err)
	}
	if leasedBy != keyID {
		t.Errorf("leased_by_key_id = %d, want %d", leasedBy, keyID)
	}
}
