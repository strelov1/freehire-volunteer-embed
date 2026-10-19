package main

import (
	"context"
	"sync"
	"time"
)

// embedGate is the one door to the local embedder that every worker goes
// through. It serves two purposes at once.
//
// It serializes: a worker's batch has the embedder to itself, so the
// adaptive chunk size underneath times the embedder alone and not the
// queue in front of it. Workers exist to overlap the network — one leases
// or submits while another embeds — not to put two batches on one GPU.
//
// It paces: after a batch it keeps the door shut for restAfter(busy) of
// rest, so the embedder is busy loadPercent of the time however many
// workers are waiting. The rest has to be taken while the gate is still
// held — released early, another worker's batch fills it and LOAD stops
// meaning anything.
type embedGate struct {
	mu          sync.Mutex
	loadPercent int
	sleep       func(ctx context.Context, d time.Duration) error
}

func newEmbedGate(loadPercent int, sleep func(ctx context.Context, d time.Duration) error) *embedGate {
	return &embedGate{loadPercent: loadPercent, sleep: sleep}
}

func (g *embedGate) wrap(inner embedBatchFunc) embedBatchFunc {
	return func(ctx context.Context, texts []string) ([][]float64, error) {
		g.mu.Lock()
		defer g.mu.Unlock()

		begin := time.Now()
		vectors, err := inner(ctx, texts)
		if rest := restAfter(time.Since(begin), g.loadPercent); rest > 0 {
			if sleepErr := g.sleep(ctx, rest); sleepErr != nil && err == nil {
				err = sleepErr
			}
		}
		return vectors, err
	}
}

// restAfter is how long to stay idle after busy so that busy is
// loadPercent of the whole: at 50 the rest equals the work, at 100 there
// is none.
func restAfter(busy time.Duration, loadPercent int) time.Duration {
	return busy * time.Duration(100-loadPercent) / time.Duration(loadPercent)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
