package main

import (
	"context"
	"errors"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/client"
)

func TestRunOnce_NoJobs_DoesNotCallEmbedOrSubmit(t *testing.T) {
	leaseCalled := false
	embedCalled := false
	submitCalled := false

	result, err := runOnce(context.Background(),
		func(ctx context.Context) ([]client.Job, error) {
			leaseCalled = true
			return nil, nil
		},
		func(ctx context.Context, texts []string) ([][]float64, error) {
			embedCalled = true
			return nil, nil
		},
		func(ctx context.Context, submissions []client.Submission) ([]client.Result, error) {
			submitCalled = true
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if !leaseCalled {
		t.Error("expected lease to be called")
	}
	if embedCalled || submitCalled {
		t.Error("expected embed/submit NOT to be called when there are no jobs")
	}
	if result.Leased != 0 || result.Submitted != 0 {
		t.Errorf("expected zero counts, got %+v", result)
	}
}

// TestRunOnce_JobsLeased_EmbedsAllAsOneBatchAndSubmitsEach is the whole
// point of batching: all of a lease's texts go through embedBatch in ONE
// call (not one call per job) — a TEI-compatible server batches the
// forward pass across inputs, which measured roughly 20x faster for a
// batch of 5 against a local Mac TEI server than calling it once per text.
func TestRunOnce_JobsLeased_EmbedsAllAsOneBatchAndSubmitsEach(t *testing.T) {
	var embedBatchCallCount int
	var embeddedTexts []string
	var submitted []client.Submission

	result, err := runOnce(context.Background(),
		func(ctx context.Context) ([]client.Job, error) {
			return []client.Job{
				{OutboxID: 10, ID: 1, Text: "passage: A"},
				{OutboxID: 20, ID: 2, Text: "passage: B"},
			}, nil
		},
		func(ctx context.Context, texts []string) ([][]float64, error) {
			embedBatchCallCount++
			embeddedTexts = texts
			return [][]float64{{0.1, 0.2}, {0.3, 0.4}}, nil
		},
		func(ctx context.Context, submissions []client.Submission) ([]client.Result, error) {
			submitted = submissions
			results := make([]client.Result, len(submissions))
			for i, s := range submissions {
				results[i] = client.Result{JobID: s.JobID, Status: "accepted"}
			}
			return results, nil
		},
	)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}

	if embedBatchCallCount != 1 {
		t.Errorf("expected exactly 1 embedBatch call for the whole lease, got %d", embedBatchCallCount)
	}
	if len(embeddedTexts) != 2 || embeddedTexts[0] != "passage: A" || embeddedTexts[1] != "passage: B" {
		t.Errorf("unexpected texts sent to embedBatch: %+v", embeddedTexts)
	}
	if len(submitted) != 2 {
		t.Fatalf("expected 2 submissions, got %d", len(submitted))
	}
	if submitted[0].OutboxID != 10 || submitted[0].JobID != 1 || submitted[0].Vector[0] != 0.1 {
		t.Errorf("submissions[0] = %+v, want OutboxID=10 JobID=1 Vector[0]=0.1", submitted[0])
	}
	if submitted[1].OutboxID != 20 || submitted[1].JobID != 2 || submitted[1].Vector[0] != 0.3 {
		t.Errorf("submissions[1] = %+v, want OutboxID=20 JobID=2 Vector[0]=0.3", submitted[1])
	}
	if result.Leased != 2 || result.Submitted != 2 {
		t.Errorf("expected Leased=2 Submitted=2, got %+v", result)
	}
}

// TestRunOnce_EmbedBatchFails_SkipsSubmitThisRound guards the trade-off
// batching accepts: a single HTTP call either embeds the whole lease or
// none of it (unlike the old one-call-per-job design, where one bad text
// couldn't block its siblings). A whole-batch failure must not panic or
// submit garbage — it leaves every job's semantic_outbox claim to expire
// and be re-leased later, same as any other "could not reach local
// embedder" failure.
func TestRunOnce_EmbedBatchFails_SkipsSubmitThisRound(t *testing.T) {
	submitCalled := false

	result, err := runOnce(context.Background(),
		func(ctx context.Context) ([]client.Job, error) {
			return []client.Job{
				{OutboxID: 10, ID: 1, Text: "passage: A"},
				{OutboxID: 20, ID: 2, Text: "passage: B"},
			}, nil
		},
		func(ctx context.Context, texts []string) ([][]float64, error) {
			return nil, errors.New("local embedding server unreachable")
		},
		func(ctx context.Context, submissions []client.Submission) ([]client.Result, error) {
			submitCalled = true
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}
	if submitCalled {
		t.Error("expected submit NOT to be called when the whole embed batch failed")
	}
	if result.Leased != 2 || result.Submitted != 0 {
		t.Errorf("expected Leased=2 Submitted=0, got %+v", result)
	}
}
