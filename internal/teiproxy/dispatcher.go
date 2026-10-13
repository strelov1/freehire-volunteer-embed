// Package teiproxy schedules work ahead of a shared, contended upstream:
// specifically, freehire's single trusted TEI server, which this
// volunteer-embed project's own spot-check verification competes for
// against hire's own production backfill worker. Dispatcher gives one lane
// (volunteer verification) priority over the other (the backfill worker)
// without the backfill worker being starved outright even under sustained
// high-lane load — see workerLoop's burst limit for the exact guarantee.
package teiproxy

import (
	"context"
	"fmt"
)

type Lane int

const (
	LaneHigh Lane = iota
	LaneLow
)

// highBurstLimit bounds how many consecutive high-lane items a worker will
// take before forcing a check of the low lane, even if high still has work
// waiting. Without this, continuous high-lane traffic (not just bursts)
// would starve the low lane completely — exactly the failure mode this
// proxy exists to prevent for hire's production worker.
const highBurstLimit = 8

type Dispatcher struct {
	high chan func()
	low  chan func()
}

// NewDispatcher creates a Dispatcher whose lanes each buffer up to
// queueSize pending items before a caller's Do blocks trying to enqueue.
func NewDispatcher(queueSize int) *Dispatcher {
	return &Dispatcher{
		high: make(chan func(), queueSize),
		low:  make(chan func(), queueSize),
	}
}

// RunWorkers starts n worker goroutines; each prefers the high lane, but
// never for more than highBurstLimit items in a row while low has work
// waiting. Workers exit when ctx is done.
func (d *Dispatcher) RunWorkers(ctx context.Context, n int) {
	for i := 0; i < n; i++ {
		go d.workerLoop(ctx)
	}
}

// workerLoop never calls a work item's fn directly without going through
// Do's own panic recovery (see Do below) — every fn queued here is one Do
// built, and its recover lives inside fn's own closure so a panic both (a)
// cannot kill this goroutine and (b) still reports a prompt error back to
// whoever is waiting on it, instead of leaving them blocked until their
// own context times out.
func (d *Dispatcher) workerLoop(ctx context.Context) {
	consecutiveHigh := 0
	for {
		if consecutiveHigh < highBurstLimit {
			// Non-blocking check first: if high-priority work is already
			// sitting in the channel, take it before even considering low —
			// a plain `select` with both cases ready picks pseudo-randomly,
			// which would not give high lane a real priority guarantee.
			select {
			case fn := <-d.high:
				fn()
				consecutiveHigh++
				continue
			default:
			}
		}

		// Either the burst limit was hit, or high was empty: give low a
		// chance to run without waiting on high.
		select {
		case fn := <-d.low:
			fn()
			consecutiveHigh = 0
			continue
		default:
		}

		select {
		case fn := <-d.high:
			fn()
			consecutiveHigh++
		case fn := <-d.low:
			fn()
			consecutiveHigh = 0
		case <-ctx.Done():
			return
		}
	}
}

// Do submits work to the given lane and blocks for its result, or returns
// ctx's error if ctx is cancelled before any worker gets to it — whether
// that cancellation happens while still trying to enqueue (lane full) or
// while waiting for a worker to finish. A free function, not a method,
// because Go methods cannot carry their own type parameter — T lets a
// caller get back whatever shape of result it needs (the HTTP layer wants
// a status code alongside the body) without this package committing to one.
func Do[T any](ctx context.Context, d *Dispatcher, lane Lane, work func() (T, error)) (T, error) {
	type outcome struct {
		val T
		err error
	}
	resultCh := make(chan outcome, 1)
	fn := func() {
		defer func() {
			if r := recover(); r != nil {
				var zero T
				resultCh <- outcome{zero, fmt.Errorf("teiproxy: work panicked: %v", r)}
			}
		}()
		val, err := work()
		resultCh <- outcome{val, err}
	}

	queue := d.low
	if lane == LaneHigh {
		queue = d.high
	}

	select {
	case queue <- fn:
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}

	select {
	case o := <-resultCh:
		return o.val, o.err
	case <-ctx.Done():
		var zero T
		return zero, ctx.Err()
	}
}
