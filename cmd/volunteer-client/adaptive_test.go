package main

import (
	"testing"
	"time"
)

func TestAdaptiveChunkSize_StartsAtSeed(t *testing.T) {
	a := newAdaptiveChunkSize(5, 1, 1000)
	if a.size() != 5 {
		t.Errorf("size() = %d, want 5 (the seed)", a.size())
	}
}

func TestAdaptiveChunkSize_FirstSuccess_EstablishesBaselineWithoutGrowing(t *testing.T) {
	a := newAdaptiveChunkSize(5, 1, 1000)
	a.recordSuccess(5, 100*time.Millisecond) // first ever sample: nothing to compare against yet

	if a.size() != 5 {
		t.Errorf("size() after the very first sample = %d, want unchanged 5", a.size())
	}
}

func TestAdaptiveChunkSize_RepeatedFastSuccess_Grows(t *testing.T) {
	a := newAdaptiveChunkSize(5, 1, 1000)
	a.recordSuccess(5, 100*time.Millisecond) // establishes baseline: 20ms/text

	a.recordSuccess(5, 100*time.Millisecond) // same pace again: healthy, should grow
	if a.size() <= 5 {
		t.Errorf("size() after a second equally-fast success = %d, want > 5 (growth)", a.size())
	}
}

func TestAdaptiveChunkSize_NeverExceedsMax(t *testing.T) {
	a := newAdaptiveChunkSize(5, 1, 8)
	a.recordSuccess(5, 10*time.Millisecond) // baseline

	for i := 0; i < 20; i++ {
		a.recordSuccess(a.size(), 10*time.Millisecond) // consistently fast: would grow forever without a cap
	}

	if a.size() > 8 {
		t.Errorf("size() = %d, want capped at max 8", a.size())
	}
}

func TestAdaptiveChunkSize_LatencySpike_Shrinks(t *testing.T) {
	a := newAdaptiveChunkSize(20, 1, 1000)
	a.recordSuccess(20, 100*time.Millisecond) // baseline: 5ms/text at size 20

	a.recordSuccess(20, 2*time.Second) // same size, suddenly 100ms/text: looks like thrashing
	if a.size() >= 20 {
		t.Errorf("size() after a latency spike = %d, want shrunk below 20", a.size())
	}
}

func TestAdaptiveChunkSize_Failure_Shrinks(t *testing.T) {
	a := newAdaptiveChunkSize(20, 1, 1000)
	a.recordFailure()

	if a.size() >= 20 {
		t.Errorf("size() after a failed call = %d, want shrunk below 20", a.size())
	}
}

// TestAdaptiveChunkSize_AbsoluteLatencyCeiling_ShrinksEvenIfBaselineDriftedUp
// guards against a gap a relative-only threshold has: if latency creeps up
// gradually (a little slower each call), the rolling baseline keeps
// re-centering on the new, worse "normal" and never trips the relative
// check — even once absolute latency is clearly bad. Here the baseline has
// already drifted exactly up to the ceiling, so a purely relative
// comparison would call this next sample "healthy" (it's not meaningfully
// slower than that drifted baseline) and grow further; the absolute
// ceiling must catch it anyway.
func TestAdaptiveChunkSize_AbsoluteLatencyCeiling_ShrinksEvenIfBaselineDriftedUp(t *testing.T) {
	a := newAdaptiveChunkSize(50, 1, 1000)
	a.baseline = adaptiveAbsoluteLatencyCeiling

	a.recordSuccess(1, adaptiveAbsoluteLatencyCeiling+100*time.Millisecond)

	if a.size() >= 50 {
		t.Errorf("size() = %d, want shrunk below seed 50 once per-text latency exceeds the absolute ceiling", a.size())
	}
}

func TestAdaptiveChunkSize_NeverShrinksBelowMin(t *testing.T) {
	a := newAdaptiveChunkSize(5, 3, 1000)

	for i := 0; i < 20; i++ {
		a.recordFailure() // repeated failures would shrink to 0 without a floor
	}

	if a.size() < 3 {
		t.Errorf("size() = %d, want floored at min 3", a.size())
	}
}
