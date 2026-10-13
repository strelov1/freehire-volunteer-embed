package main

import (
	"context"
	"log"

	"github.com/strelov1/freehire-volunteer-embed/internal/client"
)

type leaseFunc func(ctx context.Context) ([]client.Job, error)
type embedFunc func(ctx context.Context, text string) ([]float64, error)
type submitFunc func(ctx context.Context, submissions []client.Submission) ([]client.Result, error)

type runResult struct {
	Leased    int
	Submitted int
}

// runOnce leases one batch, embeds each job locally, and submits whatever
// embedded successfully. A failure to embed one job (e.g. the local TEI
// server hiccups) does not block the others — each job stands alone.
func runOnce(ctx context.Context, lease leaseFunc, embed embedFunc, submit submitFunc) (runResult, error) {
	jobs, err := lease(ctx)
	if err != nil {
		return runResult{}, err
	}
	if len(jobs) == 0 {
		return runResult{}, nil
	}

	submissions := make([]client.Submission, 0, len(jobs))
	for _, job := range jobs {
		vector, err := embed(ctx, job.Text)
		if err != nil {
			log.Printf("embed job %d: %v (skipping, will be re-leased later)", job.ID, err)
			continue
		}
		submissions = append(submissions, client.Submission{OutboxID: job.OutboxID, JobID: job.ID, Vector: vector})
	}

	result := runResult{Leased: len(jobs)}
	if len(submissions) == 0 {
		return result, nil
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
