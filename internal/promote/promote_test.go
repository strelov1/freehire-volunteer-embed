package promote

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func fixtureVector() []float64 {
	v := make([]float64, 768)
	for i := range v {
		v[i] = 0.03
	}
	return v
}

// parseVectorText parses pgvector's text output format, e.g. "[0.1,0.2,0.3]".
func parseVectorText(t *testing.T, s string) []float64 {
	t.Helper()
	s = strings.Trim(s, "[]")
	parts := strings.Split(s, ",")
	v := make([]float64, len(parts))
	for i, p := range parts {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			t.Fatalf("parsing vector element %q: %v", p, err)
		}
		v[i] = f
	}
	return v
}

func TestPromote_WritesChunkAndClearsOutboxRow(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	vec := fixtureVector()
	if err := Promote(ctx, pool, outboxID, jobID, vec); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	var chunkIndex int
	var storedText string
	if err := pool.QueryRow(ctx,
		`SELECT chunk_index, embedding::text FROM job_semantic_chunks WHERE job_id = $1`, jobID,
	).Scan(&chunkIndex, &storedText); err != nil {
		t.Fatalf("querying job_semantic_chunks: %v", err)
	}
	stored := parseVectorText(t, storedText)
	if chunkIndex != 0 {
		t.Errorf("chunk_index = %d, want 0", chunkIndex)
	}
	if len(stored) != 768 || stored[0] != vec[0] {
		t.Errorf("stored embedding looks wrong: len=%d first=%v", len(stored), stored[:1])
	}

	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM semantic_outbox WHERE job_id = $1`, jobID).Scan(&outboxCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if outboxCount != 0 {
		t.Errorf("expected the semantic_outbox row to be cleared after promotion, found %d", outboxCount)
	}
}

func TestPromote_CalledTwiceForSameJob_IsIdempotent(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-twice-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	first := fixtureVector()
	if err := Promote(ctx, pool, outboxID, jobID, first); err != nil {
		t.Fatalf("first Promote: %v", err)
	}

	second := fixtureVector()
	second[0] = 0.99 // a different volunteer's (or stale re-submission's) vector
	if err := Promote(ctx, pool, outboxID, jobID, second); err != nil {
		t.Fatalf("second Promote: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one job_semantic_chunks row after promoting twice, got %d", count)
	}

	var storedText string
	if err := pool.QueryRow(ctx,
		`SELECT embedding::text FROM job_semantic_chunks WHERE job_id = $1`, jobID,
	).Scan(&storedText); err != nil {
		t.Fatalf("querying stored embedding: %v", err)
	}
	stored := parseVectorText(t, storedText)
	if stored[0] != first[0] {
		t.Errorf("second Promote must not overwrite the first promotion's vector; got first element %v, want %v (the original)", stored[0], first[0])
	}
}

// TestPromote_ClearsOnlyItsOwnOutboxRow guards a real production incident
// (2026-10-13): a job mid-migration between two embedding model versions
// has two semantic_outbox rows (one per target_model). Promoting one must
// not clear the other, still-unverified one — Promote's DELETE is scoped
// to the specific outbox row id it was given, not every row for the job.
func TestPromote_ClearsOnlyItsOwnOutboxRow(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-sibling-job', 'd') RETURNING id`,
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

	if err := Promote(ctx, pool, outboxID1, jobID, fixtureVector()); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	var sibling2Count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM semantic_outbox WHERE id = $1`, outboxID2).Scan(&sibling2Count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sibling2Count != 1 {
		t.Errorf("promoting outbox row %d deleted sibling row %d too (count=%d, want 1)", outboxID1, outboxID2, sibling2Count)
	}
}
