package main

import (
	"context"
	"time"
)

// degradedLatencyFactor is how much slower a call's per-text latency has to
// be than the rolling baseline before it's treated as a sign of local
// resource contention (the machine starting to thrash) rather than normal
// jitter.
const degradedLatencyFactor = 1.5

// growthFactor/backoffFactor follow the same additive-increase,
// multiplicative-decrease shape TCP congestion control uses: ramp up
// gently while things look healthy, back off hard at the first sign of
// trouble.
const growthFactor = 1.5
const backoffFactor = 0.5

// baselineWeight controls how much a single new sample moves the rolling
// latency baseline — low enough that one slow call doesn't permanently
// redefine "normal", but shrink already happened independently via
// degradedLatencyFactor by the time this runs.
const baselineWeight = 0.3

// adaptiveSeedChunkSize/adaptiveMinChunkSize are deliberately small: a
// volunteer's local embedder is unknown hardware the first time it runs,
// so the client starts conservatively and earns a bigger chunk size by
// proving it's fast, rather than risking the thrash a large first call
// caused in practice. The ceiling is LocalEmbedChunkSizeMax (clientconfig),
// not a constant here — unlike latency, a sudden OOM kill of the local
// embedder gives no slow-call warning to react to (confirmed in practice:
// growth outran one volunteer's memory before any error or latency spike
// could trip grow()'s single-call backoff), so the ceiling itself has to be
// something a volunteer can pin to their own machine's real limit.
const adaptiveSeedChunkSize = 5
const adaptiveMinChunkSize = 1

// adaptiveAbsoluteLatencyCeiling catches sustained multi-step latency drift
// that a baseline-relative comparison alone would miss: if each call is
// only a little slower than the last, the rolling baseline keeps
// re-centering on the new, worse "normal" and never trips the relative
// degradedLatencyFactor check, even as absolute latency climbs a long way.
// This is a hard backstop, independent of wherever the baseline has drifted.
const adaptiveAbsoluteLatencyCeiling = 2 * time.Second

// lowMemoryShrinkFraction/lowMemoryHoldFraction gate growth on a second,
// independent signal: available system memory, sampled directly from the
// OS (see memstat_*.go). This exists because a sudden OOM kill gives no
// slow-call warning the latency-based signal above could ever react to —
// confirmed in practice, growth outran a volunteer's memory while every
// individual call still looked fast right up to the moment the process
// died. Below lowMemoryShrinkFraction, shrink immediately regardless of
// latency. Between the two, hold the current size rather than grow
// further. Above lowMemoryHoldFraction, this signal has nothing to add.
const lowMemoryShrinkFraction = 0.10
const lowMemoryHoldFraction = 0.25

// unknownMemoryFraction is the zero-value sentinel meaning "no memory
// sample has been reported yet" — distinct from any real fraction, which
// is always in [0, 1]. Platforms where the OS probe fails fall back to
// latency-only gating rather than being stuck unable to grow forever.
const unknownMemoryFraction = -1

// adaptiveChunkSize tracks how many texts the local embedder is currently
// trusted to handle in one call, growing it while calls stay fast and
// shrinking it at the first sign of trouble (an error, a call that's much
// slower per text than recent history, or low available system memory) —
// the same pattern used to avoid overwhelming a shared resource without
// knowing its exact capacity up front.
type adaptiveChunkSize struct {
	current        int
	min            int
	max            int
	baseline       time.Duration
	memoryFraction float64
}

func newAdaptiveChunkSize(seed, min, max int) *adaptiveChunkSize {
	return &adaptiveChunkSize{current: seed, min: min, max: max, memoryFraction: unknownMemoryFraction}
}

func (a *adaptiveChunkSize) size() int {
	return a.current
}

// recordSuccess reports that a call embedding textCount texts took elapsed
// time. textCount must be positive — adaptiveEmbedBatch, its only caller,
// never slices an empty chunk. The very first call only establishes the
// latency baseline — there is nothing yet to compare it against.
func (a *adaptiveChunkSize) recordSuccess(textCount int, elapsed time.Duration) {
	perText := elapsed / time.Duration(textCount)

	if perText > adaptiveAbsoluteLatencyCeiling {
		a.shrink()
		a.baseline = perText
		return
	}

	if a.baseline == 0 {
		a.baseline = perText
		return
	}

	if perText > time.Duration(float64(a.baseline)*degradedLatencyFactor) {
		a.shrink()
		a.baseline = perText
		return
	}

	if a.memoryFraction == unknownMemoryFraction || a.memoryFraction >= lowMemoryHoldFraction {
		a.grow()
	}
	a.baseline = time.Duration(float64(a.baseline)*(1-baselineWeight) + float64(perText)*baselineWeight)
}

func (a *adaptiveChunkSize) recordFailure() {
	a.shrink()
}

// recordMemory reports the OS's current available-memory fraction,
// independent of any call's latency. Below lowMemoryShrinkFraction this
// shrinks immediately — the one signal in this whole mechanism that can
// act before a slow call ever happens, which matters because an OOM kill
// doesn't produce a slow call to react to; it just ends the process.
func (a *adaptiveChunkSize) recordMemory(availableFraction float64) {
	a.memoryFraction = availableFraction
	if availableFraction < lowMemoryShrinkFraction {
		a.shrink()
	}
}

func (a *adaptiveChunkSize) grow() {
	next := int(float64(a.current) * growthFactor)
	if next <= a.current {
		next = a.current + 1
	}
	a.current = min(next, a.max)
}

func (a *adaptiveChunkSize) shrink() {
	next := int(float64(a.current) * backoffFactor)
	a.current = max(min(next, a.max), a.min)
}

// adaptiveEmbedBatch wraps an embedBatchFunc so each sub-call's size is
// whatever sizer currently trusts the local embedder with, feeding each
// call's outcome back into sizer so later calls — within this batch, and
// across every batch for the life of the process — keep adjusting. now is
// injected so tests can control elapsed time deterministically. memFraction
// samples the OS's available-memory fraction directly (see memstat_*.go);
// an error from it (unsupported platform, a transient read failure) just
// means this call falls back to the latency-only signal, not a failure of
// embedding itself.
func adaptiveEmbedBatch(inner embedBatchFunc, sizer *adaptiveChunkSize, now func() time.Time, memFraction func() (float64, error)) embedBatchFunc {
	return func(ctx context.Context, texts []string) ([][]float64, error) {
		vectors := make([][]float64, 0, len(texts))
		for start := 0; start < len(texts); {
			if frac, err := memFraction(); err == nil {
				sizer.recordMemory(frac)
			}

			end := min(start+sizer.size(), len(texts))
			chunk := texts[start:end]

			begin := now()
			chunkVectors, err := inner(ctx, chunk)
			elapsed := now().Sub(begin)
			if err != nil {
				sizer.recordFailure()
				return nil, err
			}
			sizer.recordSuccess(len(chunk), elapsed)

			vectors = append(vectors, chunkVectors...)
			start = end
		}
		return vectors, nil
	}
}
