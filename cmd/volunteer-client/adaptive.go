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
// caused in practice. adaptiveMaxChunkSize is a generous backstop; the
// real ceiling on a single call is however many texts were leased
// (BATCH_SIZE) in the first place.
const adaptiveSeedChunkSize = 5
const adaptiveMinChunkSize = 1
const adaptiveMaxChunkSize = 2000

// adaptiveAbsoluteLatencyCeiling catches sustained multi-step latency drift
// that a baseline-relative comparison alone would miss: if each call is
// only a little slower than the last, the rolling baseline keeps
// re-centering on the new, worse "normal" and never trips the relative
// degradedLatencyFactor check, even as absolute latency climbs a long way.
// This is a hard backstop, independent of wherever the baseline has drifted.
const adaptiveAbsoluteLatencyCeiling = 2 * time.Second

// adaptiveChunkSize tracks how many texts the local embedder is currently
// trusted to handle in one call, growing it while calls stay fast and
// shrinking it at the first sign of trouble (an error, or a call that's
// much slower per text than recent history) — the same pattern used to
// avoid overwhelming a shared resource without knowing its exact capacity
// up front.
type adaptiveChunkSize struct {
	current  int
	min      int
	max      int
	baseline time.Duration
}

func newAdaptiveChunkSize(seed, min, max int) *adaptiveChunkSize {
	return &adaptiveChunkSize{current: seed, min: min, max: max}
}

func (a *adaptiveChunkSize) size() int {
	return a.current
}

// recordSuccess reports that a call embedding textCount texts took elapsed
// time. The very first call only establishes the latency baseline — there
// is nothing yet to compare it against.
func (a *adaptiveChunkSize) recordSuccess(textCount int, elapsed time.Duration) {
	if textCount == 0 {
		return
	}
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

	a.grow()
	a.baseline = time.Duration(float64(a.baseline)*(1-baselineWeight) + float64(perText)*baselineWeight)
}

func (a *adaptiveChunkSize) recordFailure() {
	a.shrink()
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
// injected so tests can control elapsed time deterministically.
func adaptiveEmbedBatch(inner embedBatchFunc, sizer *adaptiveChunkSize, now func() time.Time) embedBatchFunc {
	return func(ctx context.Context, texts []string) ([][]float64, error) {
		vectors := make([][]float64, 0, len(texts))
		for start := 0; start < len(texts); {
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
