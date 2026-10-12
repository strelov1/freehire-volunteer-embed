package reputation

import (
	"context"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestGetCounts_NoRow_ReturnsZeros(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	totalEvaluated, consecutivePasses, err := GetCounts(ctx, pool, 999)
	if err != nil {
		t.Fatalf("GetCounts: %v", err)
	}
	if totalEvaluated != 0 || consecutivePasses != 0 {
		t.Errorf("GetCounts = (%d, %d), want (0, 0) for a key with no reputation row yet", totalEvaluated, consecutivePasses)
	}
}

func TestGetCounts_ExistingRow_ReturnsStoredValues(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes) VALUES (1, 42, 7)
	`); err != nil {
		t.Fatalf("seed reputation row: %v", err)
	}

	totalEvaluated, consecutivePasses, err := GetCounts(ctx, pool, 1)
	if err != nil {
		t.Fatalf("GetCounts: %v", err)
	}
	if totalEvaluated != 42 || consecutivePasses != 7 {
		t.Errorf("GetCounts = (%d, %d), want (42, 7)", totalEvaluated, consecutivePasses)
	}
}
