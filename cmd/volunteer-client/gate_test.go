package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRestAfter_ScalesBusyTimeToTheLoad(t *testing.T) {
	for percent, want := range map[int]time.Duration{100: 0, 50: 2 * time.Second, 25: 6 * time.Second, 80: 500 * time.Millisecond} {
		if got := restAfter(2*time.Second, percent); got != want {
			t.Errorf("restAfter(2s, %d) = %s, want %s", percent, got, want)
		}
	}
}

// Workers share one local embedder: two batches must never be on it at
// once, or each one's latency includes the other's and the adaptive chunk
// size reads contention as a slow machine.
func TestEmbedGate_SerializesConcurrentBatches(t *testing.T) {
	var inFlight, maxInFlight atomic.Int32
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		inFlight.Add(-1)
		return make([][]float64, len(texts)), nil
	}
	embed := newEmbedGate(100, sleepCtx).wrap(inner)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := embed(context.Background(), []string{"x"}); err != nil {
				t.Errorf("embed: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got != 1 {
		t.Errorf("%d batches were on the embedder at once, want 1", got)
	}
}

// The rest is taken while the gate is still held: otherwise another
// worker's batch fills it and the machine runs at 100% whatever was asked.
func TestEmbedGate_RestsBeforeTheNextBatch(t *testing.T) {
	var rests []time.Duration
	var mu sync.Mutex
	sleep := func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		rests = append(rests, d)
		mu.Unlock()
		return nil
	}
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		time.Sleep(20 * time.Millisecond)
		return make([][]float64, len(texts)), nil
	}
	embed := newEmbedGate(50, sleep).wrap(inner)

	if _, err := embed(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("embed: %v", err)
	}

	if len(rests) != 1 || rests[0] < 20*time.Millisecond {
		t.Errorf("rests = %v, want one rest at least as long as the 20ms batch (LOAD=50)", rests)
	}
}

func TestEmbedGate_HardTakesNoRest(t *testing.T) {
	slept := false
	sleep := func(ctx context.Context, d time.Duration) error {
		slept = true
		return nil
	}
	inner := func(ctx context.Context, texts []string) ([][]float64, error) {
		return make([][]float64, len(texts)), nil
	}

	if _, err := newEmbedGate(100, sleep).wrap(inner)(context.Background(), []string{"x"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if slept {
		t.Error("LOAD=100 rested after a batch, want no rest")
	}
}
