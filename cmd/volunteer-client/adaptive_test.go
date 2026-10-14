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

// TestAdaptiveChunkSize_LowMemory_ShrinksEvenThoughLatencyLooksFine guards
// the real incident: an OOM kill gives no slow-call warning at all, since
// the process simply disappears before any latency-based signal can react.
// recordMemory is an independent, non-latency signal fed in from the real
// OS, and it must act even when every recent call has been fast.
func TestAdaptiveChunkSize_LowMemory_ShrinksEvenThoughLatencyLooksFine(t *testing.T) {
	a := newAdaptiveChunkSize(50, 1, 1000)
	a.recordSuccess(50, 10*time.Millisecond) // establishes a fast, healthy baseline

	a.recordMemory(0.05) // 5% available: critically low

	if a.size() >= 50 {
		t.Errorf("size() = %d, want shrunk below seed 50 under critically low available memory", a.size())
	}
}

// TestAdaptiveChunkSize_ComfortableMemory_DoesNotShrink guards against the
// opposite mistake: this signal must not misfire under ordinary memory
// pressure that isn't actually a problem.
func TestAdaptiveChunkSize_ComfortableMemory_DoesNotShrink(t *testing.T) {
	a := newAdaptiveChunkSize(50, 1, 1000)
	a.recordMemory(0.6) // 60% available: plenty

	if a.size() != 50 {
		t.Errorf("size() = %d, want unchanged 50 under comfortable available memory", a.size())
	}
}

// TestAdaptiveChunkSize_LowButNotCriticalMemory_HoldsInsteadOfGrowing
// guards the middle zone: memory isn't in immediate danger, but there also
// isn't comfortable headroom to keep growing into. A fast, healthy call in
// this zone should hold the current size rather than grow further.
func TestAdaptiveChunkSize_LowButNotCriticalMemory_HoldsInsteadOfGrowing(t *testing.T) {
	a := newAdaptiveChunkSize(50, 1, 1000)
	a.recordSuccess(50, 10*time.Millisecond) // establishes baseline
	a.recordMemory(0.20)                     // below the comfortable threshold, above the critical one

	a.recordSuccess(50, 10*time.Millisecond) // same healthy pace as before

	if a.size() != 50 {
		t.Errorf("size() = %d, want held at 50 (not grown) while memory headroom is tight but not critical", a.size())
	}
}

// TestAdaptiveChunkSize_UnknownMemory_FallsBackToLatencyOnly guards
// platforms/errors where the memory probe can't report anything: growth
// must not get stuck forever just because this signal is unavailable.
func TestAdaptiveChunkSize_UnknownMemory_FallsBackToLatencyOnly(t *testing.T) {
	a := newAdaptiveChunkSize(5, 1, 1000)
	a.recordSuccess(5, 100*time.Millisecond) // baseline, no recordMemory call at all

	a.recordSuccess(5, 100*time.Millisecond) // same healthy pace
	if a.size() <= 5 {
		t.Errorf("size() = %d, want > 5 (growth) when no memory signal has ever been reported", a.size())
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
