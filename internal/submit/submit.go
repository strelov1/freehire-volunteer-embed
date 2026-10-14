// Package submit accepts volunteer-computed embedding vectors and stages
// them in volunteer_submissions for later verification. It never writes to
// freehire's live job_semantic_chunks table — that happens only after
// EVERY chunk of a job has been verified (see the promote package).
package submit

import (
	"context"
	"errors"
	"log"
	"math"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"

	"github.com/strelov1/freehire-volunteer-embed/internal/config"
	"github.com/strelov1/freehire-volunteer-embed/internal/hiredata"
	"github.com/strelov1/freehire-volunteer-embed/internal/lease"
	"github.com/strelov1/freehire-volunteer-embed/internal/pgvec"
	"github.com/strelov1/freehire-volunteer-embed/internal/promote"
	"github.com/strelov1/freehire-volunteer-embed/internal/reputation"
	"github.com/strelov1/freehire-volunteer-embed/internal/tei"
	"github.com/strelov1/freehire-volunteer-embed/internal/verify"
)

const vectorDimension = 768

// submitConcurrency bounds how many job-groups from one batch stage or
// finish at once. Bounded, not unlimited, so one huge batch can't flood
// the DB pool with every group at once.
const submitConcurrency = 8

// trustedTEIMaxBatch mirrors hire's own teiMaxBatch
// (internal/search/search/embed.go): the trusted TEI server's own
// --max-client-batch-size rejects a single request carrying more inputs
// than this outright. A real incident (2026-10-14): combining every
// pending-check text across a whole Submit call into ONE EmbedBatch call
// routinely exceeded this once several volunteers were active at once,
// failing the TEI call every time and leaving everything "pending"
// forever -- several keys' submissions sat unverified for hours because
// of exactly this.
const trustedTEIMaxBatch = 32

type Status int

const (
	StatusAccepted Status = iota
	StatusNotLeased
	StatusInvalidVector
	// StatusError means an infrastructure problem (DB unavailable, context
	// cancelled, etc.) prevented resolving this item. Unlike the other
	// statuses it is not a final answer — the caller should retry this item.
	StatusError
)

// Item is one chunk of one job's submission — a job needing more than one
// chunk (see internal/jobtext) arrives as several Items sharing the same
// OutboxID/JobID with distinct ChunkIndex values.
type Item struct {
	OutboxID   int64
	JobID      int64
	ChunkIndex int
	Vector     []float64
}

type Result struct {
	JobID  int64
	Status Status
}

// stagedItem is one chunk that passed validation, lease-check, and its
// own volunteer_submissions insert — everything needed to finalize it
// once its job's whole-group outcome is known.
type stagedItem struct {
	chunkIndex   int
	submissionID int64
	vector       []float64
}

// pendingGroup is a job (one outbox row) whose complete, staged chunk set
// needs an actual spot-check before this submit call can decide whether
// to promote it. expectedTexts and trustedVectors are indexed by
// chunk_index (0..len-1); submissionIDs/submittedVectors are built from
// the staged items in the same index order.
type pendingGroup struct {
	outboxID       int64
	jobID          int64
	submissionIDs  []int64
	submittedVecs  [][]float64
	expectedTexts  []string
	trustedVectors [][]float64
}

// Submit processes each job's chunk set as one all-or-nothing unit: a job
// is promoted into job_semantic_chunks only once EVERY chunk it currently
// expects (internal/jobtext's chunking, re-checked fresh at submission
// time, not trusted from whatever the client claims) has been staged and
// verified in this one call. A job submitted with a missing, extra, or
// invalid chunk is left exactly as it was — nothing promoted, no
// reputation impact — rather than silently promoting a partial chunk set,
// which used to leave stale higher-index chunks from a prior, longer
// embed behind forever (see freehire-ops' 2026-10-14 writeup).
//
// Every job selected for a check has its WHOLE batch's worth of pending
// chunks re-embedded through the trusted TEI in ONE shared request, not
// one request per chunk — this is the fix for a real incident: even
// bounded per-item concurrency couldn't keep a single shared trusted TEI
// ahead of several volunteers each submitting large batches at once.
//
// Staging and finishing each run with bounded concurrency
// (submitConcurrency) across job-groups, not strictly in order. For
// groups sharing a key, this means the check-vs-skip decision
// (reputation.ShouldCheck) can read a stale, pre-increment snapshot of
// that key's counters — a batch can sample a slightly different set of
// jobs for checking than a fully sequential run would have. The counters
// themselves do not lose updates under this: each Record* call is one
// atomic SQL UPDATE, serialized by Postgres's own row lock regardless of
// Go-level concurrency.
func Submit(ctx context.Context, pool *pgxpool.Pool, keyID int64, items []Item, teiURL string, cfg config.Config) []Result {
	results := make([]Result, len(items))

	var outboxOrder []int64
	indicesByOutbox := map[int64][]int{}
	for i, item := range items {
		if _, seen := indicesByOutbox[item.OutboxID]; !seen {
			outboxOrder = append(outboxOrder, item.OutboxID)
		}
		indicesByOutbox[item.OutboxID] = append(indicesByOutbox[item.OutboxID], i)
	}

	pendingSlots := make([]*pendingGroup, len(outboxOrder))
	var stage errgroup.Group
	stage.SetLimit(submitConcurrency)
	for gi, outboxID := range outboxOrder {
		gi, outboxID := gi, outboxID
		indices := indicesByOutbox[outboxID]
		stage.Go(func() error {
			pendingSlots[gi] = stageGroup(ctx, pool, keyID, outboxID, indices, items, results, cfg)
			return nil
		})
	}
	_ = stage.Wait() // stageGroup never returns an error; failure is a per-item Status value

	var pendingGroups []*pendingGroup
	for _, pg := range pendingSlots {
		if pg != nil {
			pendingGroups = append(pendingGroups, pg)
		}
	}
	if len(pendingGroups) == 0 {
		return results
	}

	type chunkRef struct {
		group *pendingGroup
		pos   int
	}
	var jobTexts []string
	var refs []chunkRef
	for _, pg := range pendingGroups {
		for pos, text := range pg.expectedTexts {
			jobTexts = append(jobTexts, text)
			refs = append(refs, chunkRef{group: pg, pos: pos})
		}
	}
	trustedVectors, err := embedInTEIMaxBatches(ctx, teiURL, cfg.TrustedTEIAPIKey, jobTexts)
	if err != nil {
		// Trusted TEI unreachable or erroring: every pending group stays
		// pending with no reputation change, exactly like a single
		// deferred check before this change — a later submission gets
		// another chance.
		log.Printf("submit: trusted TEI call for %d pending chunk(s) across %d job(s): %v (deferring all)",
			len(jobTexts), len(pendingGroups), err)
		return results
	}
	for i, ref := range refs {
		ref.group.trustedVectors[ref.pos] = trustedVectors[i]
	}

	var finish errgroup.Group
	finish.SetLimit(submitConcurrency)
	for _, pg := range pendingGroups {
		pg := pg
		finish.Go(func() error {
			finishChecked(ctx, pool, keyID, pg)
			return nil
		})
	}
	_ = finish.Wait()

	return results
}

// trustedTEIConcurrency bounds how many trustedTEIMaxBatch-sized sub-calls
// to the trusted TEI run at once. A second real incident right after the
// max-batch-size fix: dispatching sub-batches SEQUENTIALLY could add up
// to more wall-clock time than the volunteer client's own request
// timeout once several sub-batches were needed, so the client gave up
// mid-flight and every pending chunk was deferred anyway — mirrors
// hire's own embedBatch (internal/search/search/embed.go), which runs its
// TEI-sized chunks concurrently for the same reason.
const trustedTEIConcurrency = 8

// embedInTEIMaxBatches calls tei.EmbedBatch in sub-batches of at most
// trustedTEIMaxBatch texts, run with bounded concurrency
// (trustedTEIConcurrency), concatenating the results in order — the
// trusted TEI rejects a single call over its own --max-client-batch-size
// outright, so one shared call covering a whole Submit request's pending
// checks (the optimization this replaces a single unbounded call for)
// must still respect that ceiling itself.
func embedInTEIMaxBatches(ctx context.Context, teiURL, apiKey string, texts []string) ([][]float64, error) {
	if len(texts) <= trustedTEIMaxBatch {
		return tei.EmbedBatch(ctx, teiURL, apiKey, texts)
	}

	type span struct{ start, end int }
	var spans []span
	for start := 0; start < len(texts); start += trustedTEIMaxBatch {
		spans = append(spans, span{start, min(start+trustedTEIMaxBatch, len(texts))})
	}

	results := make([][][]float64, len(spans))
	var g errgroup.Group
	g.SetLimit(trustedTEIConcurrency)
	for i, sp := range spans {
		i, sp := i, sp
		g.Go(func() error {
			vecs, err := tei.EmbedBatch(ctx, teiURL, apiKey, texts[sp.start:sp.end])
			if err != nil {
				return err
			}
			results[i] = vecs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	vectors := make([][]float64, 0, len(texts))
	for _, r := range results {
		vectors = append(vectors, r...)
	}
	return vectors, nil
}

// stageGroup validates and lease-checks every chunk belonging to one
// outbox row, stages each valid one in volunteer_submissions, and then —
// only if every chunk the job's CURRENT description actually expects was
// staged, no more and no fewer — decides once whether this job's
// submission needs a spot-check. A skip decision is finished immediately
// (no TEI call needed at all); a check decision returns a *pendingGroup
// for the batched TEI call. An incomplete or otherwise undecidable group
// returns nil: every chunk that did stage is left "pending" for a later
// pass, same as any other verification hiccup.
func stageGroup(ctx context.Context, pool *pgxpool.Pool, keyID, outboxID int64, indices []int, items []Item, results []Result, cfg config.Config) *pendingGroup {
	jobID := items[indices[0]].JobID
	staged := map[int]stagedItem{} // by chunk_index

	for _, idx := range indices {
		item := items[idx]
		status, si := stageOne(ctx, pool, keyID, item)
		results[idx] = Result{JobID: item.JobID, Status: status}
		if si != nil {
			staged[item.ChunkIndex] = *si
		}
	}
	if len(staged) == 0 {
		return nil
	}

	// JobChunks and GetCounts hit unrelated tables for unrelated keys
	// (jobID vs keyID) — run them concurrently rather than one after the
	// other, since under this service's real load both round trips were
	// paying the host's full network latency sequentially for no reason.
	var expected []string
	var totalEvaluated, consecutivePasses int
	var expectedErr, countsErr error
	var fetch errgroup.Group
	fetch.Go(func() error {
		expected, expectedErr = hiredata.JobChunks(ctx, pool, jobID)
		return nil
	})
	fetch.Go(func() error {
		totalEvaluated, consecutivePasses, countsErr = reputation.GetCounts(ctx, pool, keyID)
		return nil
	})
	_ = fetch.Wait()

	if expectedErr != nil {
		log.Printf("submit: job %d: fetching current chunk set: %v (leaving %d staged chunk(s) pending)", jobID, expectedErr, len(staged))
		return nil // leave whatever staged for a later pass
	}
	if !matchesExpectedChunkSet(staged, len(expected)) {
		// Missing, extra, or duplicate chunk indices relative to the job's
		// CURRENT chunk set (its description may have changed since this
		// batch was leased) — leave every staged row pending rather than
		// promote a partial set.
		log.Printf("submit: job %d: submitted chunk set %v does not match current expected count %d (leaving pending)",
			jobID, stagedChunkIndices(staged), len(expected))
		return nil
	}

	if countsErr != nil {
		log.Printf("submit: key %d: fetching reputation counts: %v (leaving %d staged chunk(s) pending)", keyID, countsErr, len(staged))
		return nil
	}

	if !reputation.ShouldCheck(totalEvaluated, consecutivePasses, cfg) {
		finishSkipped(ctx, pool, keyID, outboxID, jobID, staged)
		return nil
	}

	pg := &pendingGroup{
		outboxID:       outboxID,
		jobID:          jobID,
		submissionIDs:  make([]int64, len(expected)),
		submittedVecs:  make([][]float64, len(expected)),
		expectedTexts:  expected,
		trustedVectors: make([][]float64, len(expected)),
	}
	for chunkIndex, si := range staged {
		pg.submissionIDs[chunkIndex] = si.submissionID
		pg.submittedVecs[chunkIndex] = si.vector
	}
	return pg
}

// stagedChunkIndices lists staged's keys, sorted, for diagnostic logging.
func stagedChunkIndices(staged map[int]stagedItem) []int {
	indices := make([]int, 0, len(staged))
	for i := range staged {
		indices = append(indices, i)
	}
	sort.Ints(indices)
	return indices
}

// matchesExpectedChunkSet reports whether staged's keys are exactly
// {0, ..., expectedCount-1} — no gaps, no duplicates (impossible by
// construction, staged is keyed by chunk_index), no out-of-range indices.
func matchesExpectedChunkSet(staged map[int]stagedItem, expectedCount int) bool {
	if len(staged) != expectedCount {
		return false
	}
	for i := 0; i < expectedCount; i++ {
		if _, ok := staged[i]; !ok {
			return false
		}
	}
	return true
}

// stageOne validates one chunk and, in a single round trip, inserts it
// into volunteer_submissions only if its lease is still held — the
// WHERE-EXISTS subquery folds in what used to be a separate leased-check
// SELECT before this INSERT, halving stageOne's own round trips. No rows
// returned means either the lease is gone/expired or it never existed;
// either way the caller should treat it as StatusNotLeased, since this
// function has no way (and no need) to tell those two cases apart.
func stageOne(ctx context.Context, pool *pgxpool.Pool, keyID int64, item Item) (Status, *stagedItem) {
	if !validVector(item.Vector) {
		return StatusInvalidVector, nil
	}

	var submissionID int64
	err := pool.QueryRow(ctx, `
		INSERT INTO volunteer_submissions (job_id, submitted_by_key_id, embedding, chunk_index, status)
		SELECT $1, $2, $3, $4, 'pending'
		WHERE EXISTS (
			SELECT 1 FROM volunteer_leases
			WHERE outbox_id = $5 AND leased_by_key_id = $2 AND expires_at > now()
		)
		RETURNING id
	`, item.JobID, keyID, pgvec.Format(item.Vector), item.ChunkIndex, item.OutboxID).Scan(&submissionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StatusNotLeased, nil
		}
		return StatusError, nil
	}

	return StatusAccepted, &stagedItem{chunkIndex: item.ChunkIndex, submissionID: submissionID, vector: item.Vector}
}

// finishSkipped records a submission trusted on track record, with no TEI
// call at all, and promotes the job's whole chunk set.
func finishSkipped(ctx context.Context, pool *pgxpool.Pool, keyID, outboxID, jobID int64, staged map[int]stagedItem) {
	if err := reputation.RecordSkipped(ctx, pool, keyID); err != nil {
		log.Printf("submit: record skipped for key %d, job %d: %v", keyID, jobID, err)
	}
	if err := promote.Replace(ctx, pool, outboxID, jobID, chunkVectors(staged)); err != nil {
		log.Printf("submit: promote job %d: %v", jobID, err)
		return // do not finalize: nothing actually reached job_semantic_chunks
	}
	finalizeGroup(ctx, pool, keyID, jobID, staged, "verified")
}

// finishChecked records the outcome of a job's chunk set that was
// re-embedded through the trusted TEI as part of this batch's single
// shared call. The whole job passes only if EVERY chunk matches; one
// mismatched chunk rejects the whole submission, same as hire's own
// completion has no notion of "half a job's chunks."
func finishChecked(ctx context.Context, pool *pgxpool.Pool, keyID int64, pg *pendingGroup) {
	allMatch := true
	for i := range pg.expectedTexts {
		if verify.Cosine(pg.submittedVecs[i], pg.trustedVectors[i]) < verify.MatchThreshold {
			allMatch = false
			break
		}
	}

	staged := make(map[int]stagedItem, len(pg.submissionIDs))
	for i := range pg.submissionIDs {
		staged[i] = stagedItem{chunkIndex: i, submissionID: pg.submissionIDs[i], vector: pg.submittedVecs[i]}
	}

	if allMatch {
		if err := reputation.RecordPassed(ctx, pool, keyID); err != nil {
			log.Printf("submit: record passed for key %d, job %d: %v", keyID, pg.jobID, err)
		}
		if err := promote.Replace(ctx, pool, pg.outboxID, pg.jobID, chunkVectors(staged)); err != nil {
			log.Printf("submit: promote job %d: %v", pg.jobID, err)
			return // do not finalize: nothing actually reached job_semantic_chunks
		}
		finalizeGroup(ctx, pool, keyID, pg.jobID, staged, "verified")
		return
	}

	justBanned, err := reputation.RecordFailed(ctx, pool, keyID)
	if err != nil {
		log.Printf("submit: record failed for key %d, job %d: %v", keyID, pg.jobID, err)
	}
	if justBanned {
		log.Printf("AUDIT ban key=%d reason=3_consecutive_spot_check_failures job=%d", keyID, pg.jobID)
	}
	if err := lease.Release(ctx, pool, pg.outboxID, keyID); err != nil {
		log.Printf("submit: release lease for outbox row %d (job %d), key %d: %v", pg.outboxID, pg.jobID, keyID, err)
	}
	finalizeGroup(ctx, pool, keyID, pg.jobID, staged, "rejected")
}

func chunkVectors(staged map[int]stagedItem) []promote.ChunkVector {
	out := make([]promote.ChunkVector, 0, len(staged))
	for chunkIndex, si := range staged {
		out = append(out, promote.ChunkVector{ChunkIndex: chunkIndex, Vector: si.vector})
	}
	return out
}

// finalizeGroup marks every staged chunk's submission with the same final
// status in one round trip (WHERE id = ANY) rather than one UPDATE per
// chunk — a multi-chunk job otherwise paid one extra round trip per chunk
// here for no reason, since every chunk in a group always finalizes
// together.
func finalizeGroup(ctx context.Context, pool *pgxpool.Pool, keyID, jobID int64, staged map[int]stagedItem, status string) {
	ids := make([]int64, 0, len(staged))
	for _, si := range staged {
		log.Printf("AUDIT verify key=%d job=%d submission=%d outcome=%s", keyID, jobID, si.submissionID, status)
		ids = append(ids, si.submissionID)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE volunteer_submissions SET status = $1, decided_at = now() WHERE id = ANY($2)`,
		status, ids,
	); err != nil {
		log.Printf("submit: finalize submissions %v as %s: %v", ids, status, err)
	}
}

func validVector(v []float64) bool {
	if len(v) != vectorDimension {
		return false
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return false
		}
	}
	return true
}
