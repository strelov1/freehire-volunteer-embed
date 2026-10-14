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

func TestReplace_SingleChunk_WritesChunkAndClearsOutboxRow(t *testing.T) {
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
	if err := Replace(ctx, pool, outboxID, jobID, []ChunkVector{{ChunkIndex: 0, Vector: vec}}); err != nil {
		t.Fatalf("Replace: %v", err)
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

// TestReplace_MultipleChunks_WritesEveryChunkRow guards the actual fix: a
// long description's full chunk set must all land in job_semantic_chunks,
// not just the first.
func TestReplace_MultipleChunks_WritesEveryChunkRow(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-multi-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	v0, v1, v2 := fixtureVector(), fixtureVector(), fixtureVector()
	v1[0], v2[0] = 0.5, 0.9
	chunks := []ChunkVector{{ChunkIndex: 0, Vector: v0}, {ChunkIndex: 1, Vector: v1}, {ChunkIndex: 2, Vector: v2}}
	if err := Replace(ctx, pool, outboxID, jobID, chunks); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT chunk_index, embedding::text FROM job_semantic_chunks WHERE job_id = $1 ORDER BY chunk_index`, jobID)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var gotIndices []int
	for rows.Next() {
		var idx int
		var text string
		if err := rows.Scan(&idx, &text); err != nil {
			t.Fatalf("scan: %v", err)
		}
		gotIndices = append(gotIndices, idx)
	}
	if len(gotIndices) != 3 {
		t.Fatalf("len(gotIndices) = %d, want 3: %v", len(gotIndices), gotIndices)
	}
	for i, idx := range gotIndices {
		if idx != i {
			t.Errorf("gotIndices[%d] = %d, want %d", i, idx, i)
		}
	}
}

// TestReplace_CalledAgainWithFewerChunks_RemovesTheStaleHigherIndices is the
// exact bug this fix exists for: a job re-embedded with a shorter chunk set
// (content shrank, or was previously embedded by a flow that only ever
// wrote chunk 0) must not leave old higher-index chunks behind forever.
func TestReplace_CalledAgainWithFewerChunks_RemovesTheStaleHigherIndices(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-shrink-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID1 int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID1); err != nil {
		t.Fatalf("seed outbox row 1: %v", err)
	}
	if err := Replace(ctx, pool, outboxID1, jobID, []ChunkVector{
		{ChunkIndex: 0, Vector: fixtureVector()},
		{ChunkIndex: 1, Vector: fixtureVector()},
		{ChunkIndex: 2, Vector: fixtureVector()},
	}); err != nil {
		t.Fatalf("first Replace: %v", err)
	}

	var outboxID2 int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID2); err != nil {
		t.Fatalf("seed outbox row 2: %v", err)
	}
	if err := Replace(ctx, pool, outboxID2, jobID, []ChunkVector{
		{ChunkIndex: 0, Vector: fixtureVector()},
	}); err != nil {
		t.Fatalf("second Replace: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("job_semantic_chunks rows for job = %d, want 1 (the stale chunk_index 1 and 2 from the shrunk re-embed must be gone)", count)
	}
}

// TestReplace_EmptyChunks_StampsTheJobAnyway matches hire's own "absent
// from the chunks map" outcome: a description that strips to nothing is a
// valid, final state (zero chunks), not an error -- stamping it is what
// stops it being re-enqueued forever as "not yet embedded".
func TestReplace_EmptyChunks_StampsTheJobAnyway(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description, content_hash) VALUES ('promote-empty-job', '', 'hash-abc') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	if err := Replace(ctx, pool, outboxID, jobID, nil); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	var model, hash string
	if err := pool.QueryRow(ctx,
		`SELECT semantic_embedded_model, semantic_embedded_hash FROM jobs WHERE id = $1`, jobID,
	).Scan(&model, &hash); err != nil {
		t.Fatalf("querying jobs: %v", err)
	}
	if model != "e5" {
		t.Errorf("semantic_embedded_model = %q, want %q", model, "e5")
	}
	if hash != "hash-abc" {
		t.Errorf("semantic_embedded_hash = %q, want %q", hash, "hash-abc")
	}
}

// TestReplace_StampsModelAndHashAndClearsSimilarComputedAt guards the real
// fix: without this stamp, hire's own cmd/embed never sees this job as
// embedded and keeps re-enqueuing + re-embedding work a volunteer already
// did (confirmed live on prod, 2026-10-14).
func TestReplace_StampsModelAndHashAndClearsSimilarComputedAt(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO jobs (public_slug, description, content_hash, similar_computed_at)
		VALUES ('promote-stamp-job', 'd', 'hash-xyz', now())
		RETURNING id
	`).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	var outboxID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO semantic_outbox (job_id, target_model) VALUES ($1, 'e5-chunked-v1') RETURNING id`, jobID,
	).Scan(&outboxID); err != nil {
		t.Fatalf("seed outbox row: %v", err)
	}

	if err := Replace(ctx, pool, outboxID, jobID, []ChunkVector{{ChunkIndex: 0, Vector: fixtureVector()}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	var model, hash string
	var similarComputedAt *string
	if err := pool.QueryRow(ctx,
		`SELECT semantic_embedded_model, semantic_embedded_hash, similar_computed_at::text FROM jobs WHERE id = $1`, jobID,
	).Scan(&model, &hash, &similarComputedAt); err != nil {
		t.Fatalf("querying jobs: %v", err)
	}
	if model != "e5-chunked-v1" {
		t.Errorf("semantic_embedded_model = %q, want the outbox row's own target_model %q", model, "e5-chunked-v1")
	}
	if hash != "hash-xyz" {
		t.Errorf("semantic_embedded_hash = %q, want the job's current content_hash %q", hash, "hash-xyz")
	}
	if similarComputedAt != nil {
		t.Errorf("similar_computed_at = %v, want NULL (cleared so /similar's backfill revisits this job)", *similarComputedAt)
	}
}

// TestReplace_ClearsOnlyItsOwnOutboxRow guards a real production incident
// (2026-10-13): a job mid-migration between two embedding model versions
// has two semantic_outbox rows (one per target_model). Replacing from one
// must not clear the other, still-unverified one.
func TestReplace_ClearsOnlyItsOwnOutboxRow(t *testing.T) {
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

	if err := Replace(ctx, pool, outboxID1, jobID, []ChunkVector{{ChunkIndex: 0, Vector: fixtureVector()}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	var sibling2Count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM semantic_outbox WHERE id = $1`, outboxID2).Scan(&sibling2Count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if sibling2Count != 1 {
		t.Errorf("replacing from outbox row %d deleted sibling row %d too (count=%d, want 1)", outboxID1, outboxID2, sibling2Count)
	}
}

// TestReplace_OutboxRowMissing_ErrorsAndDoesNotStampNullModel guards the
// model stamp's move from a separate SELECT into a subquery inside the
// UPDATE (done to pipeline every statement into one round trip): a
// vanished outboxID must still fail loudly, not silently stamp
// semantic_embedded_model = NULL (which would mark the job "done" with a
// garbage provenance value hire's own backfill could never match against).
func TestReplace_OutboxRowMissing_ErrorsAndDoesNotStampNullModel(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	var jobID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (public_slug, description) VALUES ('promote-missing-outbox-job', 'd') RETURNING id`,
	).Scan(&jobID); err != nil {
		t.Fatalf("seed job: %v", err)
	}

	const nonexistentOutboxID = 999999999
	if err := Replace(ctx, pool, nonexistentOutboxID, jobID, []ChunkVector{{ChunkIndex: 0, Vector: fixtureVector()}}); err == nil {
		t.Fatal("Replace with a nonexistent outbox row: want an error, got nil")
	}

	var model *string
	if err := pool.QueryRow(ctx, `SELECT semantic_embedded_model FROM jobs WHERE id = $1`, jobID).Scan(&model); err != nil {
		t.Fatalf("querying jobs: %v", err)
	}
	if model != nil {
		t.Errorf("semantic_embedded_model = %v, want untouched (NULL) after a failed Replace", *model)
	}

	var chunkCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM job_semantic_chunks WHERE job_id = $1`, jobID).Scan(&chunkCount); err != nil {
		t.Fatalf("count: %v", err)
	}
	if chunkCount != 0 {
		t.Errorf("job_semantic_chunks count = %d, want 0 (failed Replace must roll back its own DELETE+INSERT too)", chunkCount)
	}
}
