package reputation

import (
	"context"
	"testing"

	"github.com/strelov1/freehire-volunteer-embed/internal/testdb"
)

func TestRecordPassed_IncrementsPasses_ResetsFails_LeavesTotal(t *testing.T) {
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
	if totalEvaluated != 5 || consecutivePasses != 3 || consecutiveFails != 0 {
		t.Errorf("got (%d, %d, %d), want (5, 3, 0) — TakeTurn, not RecordPassed, advances total_evaluated", totalEvaluated, consecutivePasses, consecutiveFails)
	}
}

func TestRecordFailed_IncrementsFails_ResetsPasses_NotYetBanned(t *testing.T) {
	pool := testdb.Setup(t)
	ctx := context.Background()

	if _, err := pool.Exec(ctx, `
		INSERT INTO volunteer_reputation (key_id, total_evaluated, consecutive_passes, consecutive_fails)
		VALUES (1, 5, 9, 1)
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	banned, err := RecordFailed(ctx, pool, 1)
	if err != nil {
		t.Fatalf("RecordFailed: %v", err)
	}
	if banned {
		t.Error("RecordFailed reported banned=true, want false (only the 2nd consecutive failure)")
	}

	var totalEvaluated, consecutivePasses, consecutiveFails int
	var bannedAt *string
	if err := pool.QueryRow(ctx,
		`SELECT total_evaluated, consecutive_passes, consecutive_fails, banned_at FROM volunteer_reputation WHERE key_id = 1`,
	).Scan(&totalEvaluated, &consecutivePasses, &consecutiveFails, &bannedAt); err != nil {
		t.Fatalf("querying row: %v", err)
	}
	if totalEvaluated != 5 || consecutivePasses != 0 || consecutiveFails != 2 {
		t.Errorf("got (%d, %d, %d), want (5, 0, 2) — TakeTurn, not RecordFailed, advances total_evaluated", totalEvaluated, consecutivePasses, consecutiveFails)
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

	justBanned, err := RecordFailed(ctx, pool, 1)
	if err != nil {
		t.Fatalf("RecordFailed: %v", err)
	}
	if !justBanned {
		t.Error("RecordFailed reported banned=false on the 3rd consecutive failure, want true")
	}

	banned, err := IsBanned(ctx, pool, 1)
	if err != nil {
		t.Fatalf("IsBanned: %v", err)
	}
	if !banned {
		t.Error("expected key to be banned after its 3rd consecutive failure")
	}
}
