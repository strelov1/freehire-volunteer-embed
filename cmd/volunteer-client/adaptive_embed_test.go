package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeClock lets tests control elapsed time deterministically instead of
// relying on real sleeps. Once exhausted it keeps repeating the last
// provided value rather than panicking — a shrinking chunk size takes more
// iterations than a test author sized the list for is a normal outcome,
// not a test bug.
func fakeClock(times ...time.Time) func() time.Time {
	i := -1
	return func() time.Time {
		if i < len(times)-1 {
			i++
		}
		return times[i]
	}
}

// noMemorySignal stands in for a platform where the OS memory probe isn't
// available at all — tests that aren't specifically about the memory
// signal use this so they only exercise the latency-based path.
func noMemorySignal() (float64, error) {
	return 0, errors.New("no memory signal in this test")
}

func TestAdaptiveEmbedBatch_FirstCall_UsesSeedSize(t *testing.T) {
	var calls [][]string
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		calls = append(calls, texts)
		return make([][]float64, len(texts)), nil
	}

	sizer := newAdaptiveChunkSize(2, 1, 1000)
	wrapped := adaptiveEmbedBatch(inner, sizer, fakeClock(
		time.Unix(0, 0), time.Unix(0, 0), // call 1: start, end (instant)
		time.Unix(0, 0), time.Unix(0, 0), // call 2: start, end
	), noMemorySignal)

	texts := []string{"a", "b", "c", "d"} // seed size 2 -> 2 calls of 2
	_, err := wrapped(context.Background(), texts)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	if len(calls) != 2 || len(calls[0]) != 2 || len(calls[1]) != 2 {
		t.Fatalf("expected 2 calls of 2 texts each, got %+v", calls)
	}
}

func TestAdaptiveEmbedBatch_FastHealthyCalls_GrowsBetweenInvocations(t *testing.T) {
	var sizes []int
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		sizes = append(sizes, len(texts))
		return make([][]float64, len(texts)), nil
	}

	sizer := newAdaptiveChunkSize(5, 1, 1000)
	// Each chunk takes the same fast 1ms/text — healthy, should grow.
	clockTimes := make([]time.Time, 0, 40)
	t0 := time.Unix(0, 0)
	for i := 0; i < 20; i++ {
		clockTimes = append(clockTimes, t0, t0.Add(5*time.Millisecond))
	}
	wrapped := adaptiveEmbedBatch(inner, sizer, fakeClock(clockTimes...), noMemorySignal)

	texts := make([]string, 200)
	for i := range texts {
		texts[i] = "x"
	}
	if _, err := wrapped(context.Background(), texts); err != nil {
		t.Fatalf("wrapped: %v", err)
	}

	if sizes[len(sizes)-1] <= sizes[0] {
		t.Errorf("chunk sizes did not grow across the call: first=%d last=%d, all=%v", sizes[0], sizes[len(sizes)-1], sizes)
	}
}

func TestAdaptiveEmbedBatch_InnerFails_ShrinksAndPropagatesError(t *testing.T) {
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		return nil, errors.New("local embedder unreachable")
	}

	sizer := newAdaptiveChunkSize(20, 1, 1000)
	wrapped := adaptiveEmbedBatch(inner, sizer, fakeClock(time.Unix(0, 0), time.Unix(0, 0)), noMemorySignal)

	_, err := wrapped(context.Background(), []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("expected the inner error to propagate")
	}
	if sizer.size() >= 20 {
		t.Errorf("sizer.size() after a failure = %d, want shrunk below seed 20", sizer.size())
	}
}

// TestAdaptiveEmbedBatch_LowMemory_ShrinksBetweenChunksEvenThoughFast
// proves the wrapper actually samples the injected memory signal and feeds
// it to the sizer — not just that adaptiveChunkSize itself can react to it
// in isolation (already covered in adaptive_test.go).
func TestAdaptiveEmbedBatch_LowMemory_ShrinksBetweenChunksEvenThoughFast(t *testing.T) {
	var sizes []int
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		sizes = append(sizes, len(texts))
		return make([][]float64, len(texts)), nil
	}

	sizer := newAdaptiveChunkSize(10, 1, 1000)
	clockTimes := make([]time.Time, 0, 20)
	t0 := time.Unix(0, 0)
	for i := 0; i < 10; i++ {
		clockTimes = append(clockTimes, t0, t0.Add(time.Millisecond)) // every call is fast
	}
	criticallyLowMemory := func() (float64, error) { return 0.02, nil }
	wrapped := adaptiveEmbedBatch(inner, sizer, fakeClock(clockTimes...), criticallyLowMemory)

	texts := make([]string, 100)
	for i := range texts {
		texts[i] = "x"
	}
	if _, err := wrapped(context.Background(), texts); err != nil {
		t.Fatalf("wrapped: %v", err)
	}

	if sizes[len(sizes)-1] >= sizes[0] {
		t.Errorf("chunk sizes did not shrink despite critically low memory on every call: first=%d last=%d, all=%v",
			sizes[0], sizes[len(sizes)-1], sizes)
	}
}

// TestAdaptiveEmbedBatch_MemoryProbeErrors_StillWorksLatencyOnly guards a
// probe failure (unsupported platform, a transient read error) from
// breaking embedding entirely -- it must fall back to the latency-only
// path, not propagate the probe's own error.
func TestAdaptiveEmbedBatch_MemoryProbeErrors_StillWorksLatencyOnly(t *testing.T) {
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		return make([][]float64, len(texts)), nil
	}

	sizer := newAdaptiveChunkSize(5, 1, 1000)
	wrapped := adaptiveEmbedBatch(inner, sizer, fakeClock(time.Unix(0, 0), time.Unix(0, 0)), noMemorySignal)

	if _, err := wrapped(context.Background(), []string{"a", "b", "c"}); err != nil {
		t.Fatalf("wrapped: %v (a failing memory probe must not break embedding)", err)
	}
}

func TestAdaptiveEmbedBatch_PreservesResultOrder(t *testing.T) {
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		vecs := make([][]float64, len(texts))
		for i, text := range texts {
			vecs[i] = []float64{float64(len(text))}
		}
		return vecs, nil
	}

	sizer := newAdaptiveChunkSize(2, 1, 1000)
	wrapped := adaptiveEmbedBatch(inner, sizer, fakeClock(
		time.Unix(0, 0), time.Unix(0, 0),
		time.Unix(0, 0), time.Unix(0, 0),
		time.Unix(0, 0), time.Unix(0, 0),
	), noMemorySignal)

	texts := []string{"a", "bb", "ccc", "dddd", "e"}
	vectors, err := wrapped(context.Background(), texts)
	if err != nil {
		t.Fatalf("wrapped: %v", err)
	}
	for i, text := range texts {
		if vectors[i][0] != float64(len(text)) {
			t.Errorf("vectors[%d] does not match texts[%d] (%q) — out of order", i, i, text)
		}
	}
}
