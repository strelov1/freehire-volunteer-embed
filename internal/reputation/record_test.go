package reputation

import (
	"context"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestRecordSkipped_NewKey_CreatesRowWithOnlyTotalEvaluatedIncremented(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if err := RecordSkipped(ctx, pool, 1); err != nil {
		t.Fatalf("RecordSkipped: %v", err)
	}

	var totalEvaluated, consecutivePasses, consecutiveFails int
	if err := pool.QueryRow(ctx,
		`SELECT total_evaluated, consecutive_passes, consecutive_fails FROM volunteer_reputation WHERE key_id = 1`,
	).Scan(&totalEvaluated, &consecutivePasses, &consecutiveFails); err != nil {
		t.Fatalf("querying row: %v", err)
	}
	if totalEvaluated != 1 || consecutivePasses != 0 || consecutiveFails != 0 {
		t.Errorf("got (%d, %d, %d), want (1, 0, 0)", totalEvaluated, consecutivePasses, consecutiveFails)
	}
}

func TestRecordPassed_IncrementsTotalAndPasses_ResetsFails(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes, consecutive_fails)
		VALUES (1, 5, 2, 7)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := RecordPassed(ctx, pool, 1); err != nil {
		t.Fatalf("RecordPassed: %v", err)
	}

	var totalEvaluated, consecutivePasses, consecutiveFails int
	if err := pool.QueryRow(ctx,
		`SELECT total_evaluated, consecutive_passes, consecutive_fails FROM volunteer_reputation WHERE key_id = 1`,
	).Scan(&totalEvaluated, &consecutivePasses, &consecutiveFails); err != nil {
		t.Fatalf("querying row: %v", err)
	}
	if totalEvaluated != 6 || consecutivePasses != 3 || consecutiveFails != 0 {
		t.Errorf("got (%d, %d, %d), want (6, 3, 0)", totalEvaluated, consecutivePasses, consecutiveFails)
	}
}

func TestRecordFailed_IncrementsTotalAndFails_ResetsPasses_NotYetBanned(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes, consecutive_fails)
		VALUES (1, 5, 9, 1)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := RecordFailed(ctx, pool, 1); err != nil {
		t.Fatalf("RecordFailed: %v", err)
	}

	var totalEvaluated, consecutivePasses, consecutiveFails int
	var bannedAt *string
	if err := pool.QueryRow(ctx,
		`SELECT total_evaluated, consecutive_passes, consecutive_fails, banned_at FROM volunteer_reputation WHERE key_id = 1`,
	).Scan(&totalEvaluated, &consecutivePasses, &consecutiveFails, &bannedAt); err != nil {
		t.Fatalf("querying row: %v", err)
	}
	if totalEvaluated != 6 || consecutivePasses != 0 || consecutiveFails != 2 {
		t.Errorf("got (%d, %d, %d), want (6, 0, 2)", totalEvaluated, consecutivePasses, consecutiveFails)
	}
	if bannedAt != nil {
		t.Errorf("banned_at = %v, want nil (only 2 consecutive fails, ban threshold is 3)", *bannedAt)
	}
}

func TestRecordFailed_ThirdConsecutiveFailure_SetsBannedAt(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, consecutive_fails) VALUES (1, 2)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := RecordFailed(ctx, pool, 1); err != nil {
		t.Fatalf("RecordFailed: %v", err)
	}

	banned, err := IsBanned(ctx, pool, 1)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if !banned {
		t.Error("expected key to be banned after its 3rd consecutive failure")
	}
}
