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
		func(ctx context.Context, text string) ([]float64, error) {
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

func TestRunOnce_JobsLeased_EmbedsAndSubmitsEach(t *testing.T) {
	var embeddedTexts []string
	var submitted []client.Submission

	result, err := runOnce(context.Background(),
		func(ctx context.Context) ([]client.Job, error) {
			return []client.Job{
				{ID: 1, Text: "passage: A"},
				{ID: 2, Text: "passage: B"},
			}, nil
		},
		func(ctx context.Context, text string) ([]float64, error) {
			embeddedTexts = append(embeddedTexts, text)
			return []float64{0.1, 0.2}, nil
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

	if len(embeddedTexts) != 2 {
		t.Fatalf("expected 2 embed calls, got %d", len(embeddedTexts))
	}
	if len(submitted) != 2 || submitted[0].JobID != 1 || submitted[1].JobID != 2 {
		t.Errorf("unexpected submissions: %+v", submitted)
	}
	if result.Leased != 2 || result.Submitted != 2 {
		t.Errorf("expected Leased=2 Submitted=2, got %+v", result)
	}
}

func TestRunOnce_EmbedFailsForOneJob_StillSubmitsTheOthers(t *testing.T) {
	var submitted []client.Submission

	result, err := runOnce(context.Background(),
		func(ctx context.Context) ([]client.Job, error) {
			return []client.Job{
				{ID: 1, Text: "passage: A"},
				{ID: 2, Text: "passage: B"},
			}, nil
		},
		func(ctx context.Context, text string) ([]float64, error) {
			if text == "passage: A" {
				return nil, errors.New("local embedding server unreachable")
			}
			return []float64{0.1, 0.2}, nil
		},
		func(ctx context.Context, submissions []client.Submission) ([]client.Result, error) {
			submitted = submissions
			return []client.Result{{JobID: 2, Status: "accepted"}}, nil
		},
	)
	if err != nil {
		t.Fatalf("runOnce: %v", err)
	}

	if len(submitted) != 1 || submitted[0].JobID != 2 {
		t.Errorf("expected only job 2 to be submitted, got %+v", submitted)
	}
	if result.Leased != 2 || result.Submitted != 1 {
		t.Errorf("expected Leased=2 Submitted=1, got %+v", result)
	}
}
