package reputation

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestTakeTurn_NewKey_ReturnsTurnZeroAndCreatesRow(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	turn, consecutivePasses, err := TakeTurn(ctx, pool, 999)
	if err != nil {
		t.Fatalf("TakeTurn: %v", err)
	}
	if turn != 0 || consecutivePasses != 0 {
		t.Errorf("TakeTurn = (%d, %d), want (0, 0) for a key that has never submitted", turn, consecutivePasses)
	}

	var totalEvaluated int
	if err := pool.QueryRow(ctx, `SELECT total_evaluated FROM volunteer_reputation WHERE key_id = 999`).Scan(&totalEvaluated); err != nil {
		t.Fatalf("querying row: %v", err)
	}
	if totalEvaluated != 1 {
		t.Errorf("total_evaluated = %d, want 1", totalEvaluated)
	}
}

func TestTakeTurn_ExistingRow_ReturnsValueBeforeAdvancing(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes) VALUES (1, 42, 7)
	`); err != nil {
		t.Fatalf("seed reputation row: %v", err)
	}

	turn, consecutivePasses, err := TakeTurn(ctx, pool, 1)
	if err != nil {
		t.Fatalf("TakeTurn: %v", err)
	}
	if turn != 42 || consecutivePasses != 7 {
		t.Errorf("TakeTurn = (%d, %d), want (42, 7)", turn, consecutivePasses)
	}
}

// Concurrent callers must each get a distinct turn: that is the whole point.
func TestTakeTurn_Concurrent_EveryCallerGetsADistinctTurn(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()
	const callers = 20

	turns := make([]int, callers)
	var wg sync.WaitGroup
	for i := range turns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			turn, _, err := TakeTurn(ctx, pool, 1)
			if err != nil {
				t.Errorf("TakeTurn: %v", err)
			}
			turns[i] = turn
		}()
	}
	wg.Wait()

	sort.Ints(turns)
	for i, turn := range turns {
		if turn != i {
			t.Fatalf("turns = %v, want exactly 0..%d once each", turns, callers-1)
		}
	}
}
