package teiproxy

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestDispatcher_HighPriorityJumpsAheadOfQueuedLowPriority is the whole
// point of this package: when both lanes have work waiting, a single
// worker must drain the high lane before touching the low lane, even
// though the low-priority work arrived first.
func TestDispatcher_HighPriorityJumpsAheadOfQueuedLowPriority(t *testing.T) {
	d := NewDispatcher(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.RunWorkers(ctx, 1) // one worker: makes processing order deterministic

	var mu sync.Mutex
	var order []string
	record := func(label string) { mu.Lock(); order = append(order, label); mu.Unlock() }

	release := make(chan struct{})
	firstStarted := make(chan struct{})

	// Occupy the single worker with one low-priority item that blocks until
	// the test says to proceed — this gives the test a window to queue up
	// more low-priority work AND a high-priority item before anything else
	// is processed.
	go func() {
		_, _ = Do(ctx, d, LaneLow, func() ([]byte, error) {
			record("low-in-flight")
			close(firstStarted)
			<-release
			return nil, nil
		})
	}()
	<-firstStarted

	var wg sync.WaitGroup
	for _, label := range []string{"low-queued-1", "low-queued-2"} {
		wg.Add(1)
		label := label
		go func() {
			defer wg.Done()
			_, _ = Do(ctx, d, LaneLow, func() ([]byte, error) { record(label); return nil, nil })
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = Do(ctx, d, LaneHigh, func() ([]byte, error) { record("high"); return nil, nil })
	}()

	// Give the queued goroutines time to actually reach the channel send
	// before releasing the worker — otherwise the race is between "did they
	// enqueue yet" rather than the priority logic under test.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 4 {
		t.Fatalf("processed %d items, want 4: %v", len(order), order)
	}
	if order[0] != "low-in-flight" {
		t.Fatalf("order[0] = %q, want the item already in flight before anything else was queued", order[0])
	}
	if order[1] != "high" {
		t.Errorf("order = %v, want \"high\" processed right after the in-flight item, ahead of the two low items that were queued earlier", order)
	}
}

func TestDispatcher_NoHighPriorityWork_LowPriorityStillRuns(t *testing.T) {
	d := NewDispatcher(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.RunWorkers(ctx, 1)

	body, err := Do(ctx, d, LaneLow, func() ([]byte, error) { return []byte("ok"), nil })
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestDispatcher_ContextCancelled_DoReturnsPromptly(t *testing.T) {
	d := NewDispatcher(256)
	// No RunWorkers call at all: nothing will ever drain the queue, so Do
	// must return via ctx cancellation rather than hang forever.
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := Do(ctx, d, LaneHigh, func() ([]byte, error) { return nil, nil })
		done <- err
	}()

	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected a context-cancellation error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Do did not return after its context was cancelled")
	}
}

// TestDo_QueueFull_RespectsContextInsteadOfBlockingForever guards against a
// real production hazard: if a lane's queue fills up (e.g. workers have
// died, or a burst outpaces capacity) and nothing is draining it, a caller
// whose context gets cancelled (its own HTTP request timed out or the
// client disconnected) must not be stuck forever trying to enqueue — that
// would leak a goroutine per abandoned request forever.
func TestDo_QueueFull_RespectsContextInsteadOfBlockingForever(t *testing.T) {
	d := NewDispatcher(1) // tiny buffer: trivial to fill without workers draining it
	fillerCtx := context.Background()
	go func() { _, _ = Do(fillerCtx, d, LaneLow, func() ([]byte, error) { return nil, nil }) }()
	time.Sleep(20 * time.Millisecond) // let it actually land in the channel buffer

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Do(ctx, d, LaneLow, func() ([]byte, error) { return nil, nil })
		done <- err
	}()
	time.Sleep(20 * time.Millisecond) // let it actually block trying to enqueue
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected a context-cancellation error when the queue is full, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Do blocked forever trying to enqueue into a full lane, ignoring context cancellation")
	}
}

// TestWorkerLoop_PanicInWork_WorkerSurvivesAndKeepsProcessing guards
// against a single bad work item (e.g. an unexpected upstream response)
// permanently killing a worker goroutine. Since this proxy sits in front
// of a shared resource both this project and hire's own production
// backfill worker depend on, workers quietly dying one panic at a time
// would eventually degrade capacity to zero for everyone.
func TestWorkerLoop_PanicInWork_WorkerSurvivesAndKeepsProcessing(t *testing.T) {
	d := NewDispatcher(16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.RunWorkers(ctx, 1)

	_, err := Do(ctx, d, LaneLow, func() ([]byte, error) { panic("boom") })
	if err == nil {
		t.Error("expected an error back from a panicking work function, got nil")
	}

	body, err := Do(ctx, d, LaneLow, func() ([]byte, error) { return []byte("still alive"), nil })
	if err != nil {
		t.Fatalf("Do after a panic: %v (worker did not survive)", err)
	}
	if string(body) != "still alive" {
		t.Errorf("body = %q, want %q", body, "still alive")
	}
}

// TestWorkerLoop_SustainedHighLoad_LowLaneIsNotStarvedForever is the
// anti-starvation guarantee this package's doc comment promises: the low
// lane (hire's real production worker) must still make progress even
// while the high lane (volunteer verification) keeps arriving
// continuously, not just in isolated bursts.
// TestWorkerLoop_HighBacklogExceedsBurstLimit_LowStillGetsServedWithinBound
// deliberately bypasses Do and pushes directly onto the unexported
// channels (this file is `package teiproxy`, so it can): a live producer
// loop calling the synchronous Do can't actually keep the high lane
// continuously non-empty — it blocks waiting for each item's result
// before submitting the next, so the worker's non-blocking high check
// routinely finds a momentarily-empty channel regardless of whether the
// anti-starvation logic works at all, which made an earlier version of
// this test pass identically against the old, buggy strict-priority
// code. Pre-filling the channel directly removes that false signal: the
// high lane is genuinely, deterministically backlogged from the first
// tick, so this only passes if highBurstLimit's accounting is real.
func TestWorkerLoop_HighBacklogExceedsBurstLimit_LowStillGetsServedWithinBound(t *testing.T) {
	// Deliberately independent of the production highBurstLimit constant:
	// a test whose own pass/fail threshold is derived from the exact value
	// it's trying to verify is tautological — inflating that constant to
	// "disable" the mechanism would inflate this test's bound right along
	// with it, so it would keep passing no matter what. These are fixed,
	// literal expectations instead.
	const testHighBacklog = 40     // far more than any sane burst limit
	const maxAcceptableLowPosition = 15 // generous slack above the real highBurstLimit=8

	d := NewDispatcher(testHighBacklog + 1)

	var mu sync.Mutex
	var order []string
	record := func(label string) { mu.Lock(); order = append(order, label); mu.Unlock() }

	for i := 0; i < testHighBacklog; i++ {
		d.high <- func() { record("high") }
	}
	d.low <- func() { record("low") }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d.RunWorkers(ctx, 1) // single worker: the strictest case for starvation

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n >= testHighBacklog+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker did not drain the pre-filled backlog in time")
		}
		time.Sleep(2 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	lowIndex := -1
	for i, label := range order {
		if label == "low" {
			lowIndex = i
			break
		}
	}
	if lowIndex == -1 {
		t.Fatal("low item was never processed")
	}
	if lowIndex > maxAcceptableLowPosition {
		t.Errorf("low item was processed at position %d (0-indexed) among %d high items queued ahead of it, want at most position %d — anti-starvation bound violated",
			lowIndex, testHighBacklog, maxAcceptableLowPosition)
	}
}
