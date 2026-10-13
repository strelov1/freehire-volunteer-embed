package main

import (
	"context"
	"log"

	"github.com/strelov1/freehire-volunteer-embed/internal/client"
)

type leaseFunc func(ctx context.Context) ([]client.Job, error)
type embedBatchFunc func(ctx context.Context, texts []string) ([][]float64, error)
type submitFunc func(ctx context.Context, submissions []client.Submission) ([]client.Result, error)

type runResult struct {
	Leased    int
	Submitted int
}

// runOnce leases one batch and embeds every job's text in a single
// embedBatch call, not one call per job — a TEI-compatible server batches
// the forward pass across inputs, which is substantially faster per item
// than calling it once per text (see internal/tei.EmbedBatch). The
// trade-off: a whole-batch failure (e.g. the local embedder is down)
// skips the whole lease this round rather than isolating it to one bad
// job, unlike the old per-item design — accepted because a single HTTP
// call to one's own local embedder either works or doesn't; it has no
// per-item failure mode worth preserving isolation for.
func runOnce(ctx context.Context, lease leaseFunc, embedBatch embedBatchFunc, submit submitFunc) (runResult, error) {
	jobs, err := lease(ctx)
	if err != nil {
		return runResult{}, err
	}
	if len(jobs) == 0 {
		return runResult{}, nil
	}
	result := runResult{Leased: len(jobs)}

	texts := make([]string, len(jobs))
	for i, job := range jobs {
		texts[i] = job.Text
	}
	vectors, err := embedBatch(ctx, texts)
	if err != nil {
		log.Printf("embed batch of %d: %v (skipping, will be re-leased later)", len(jobs), err)
		return result, nil
	}

	submissions := make([]client.Submission, len(jobs))
	for i, job := range jobs {
		submissions[i] = client.Submission{OutboxID: job.OutboxID, JobID: job.ID, Vector: vectors[i]}
	}

	results, err := submit(ctx, submissions)
	if err != nil {
		return result, err
	}
	for _, r := range results {
		log.Printf("submit job %d: %s", r.JobID, r.Status)
	}
	result.Submitted = len(submissions)
	return result, nil
}
